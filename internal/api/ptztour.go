package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/LUKER2DW/sentinela/internal/camera/dvrip"
)

// PTZ tours: the camera visits saved presets in turn, waiting at each one,
// until stopped, until its time is up, or until someone takes manual
// control (idea from the legacy Sentinela tour manager). Only GotoPreset is
// sent, the same allow-listed command as the preset buttons.

// Tour limits.
const (
	tourMaxPresets   = 16
	tourMinDwell     = 5 * time.Second
	tourMaxDwell     = 10 * time.Minute
	tourMinDuration  = time.Minute
	tourMaxDuration  = 12 * time.Hour
	tourDefaultHours = 1
	// tourMaxFailures consecutive failed moves end the tour.
	tourMaxFailures = 3
)

// TourState is what GET /api/cameras/{id}/tour answers.
type TourState struct {
	Running      bool      `json:"running"`
	Presets      []int     `json:"presets,omitempty"`
	Current      int       `json:"current,omitempty"`
	DwellSeconds int       `json:"dwell_seconds,omitempty"`
	StartedBy    string    `json:"started_by,omitempty"`
	StartedAt    time.Time `json:"started_at,omitzero"`
	EndsAt       time.Time `json:"ends_at,omitzero"`
	StoppedAt    time.Time `json:"stopped_at,omitzero"`
	StopReason   string    `json:"stop_reason,omitempty"`
	LastError    string    `json:"last_error,omitempty"`
}

type tourRun struct {
	state  TourState
	cancel context.CancelFunc
	done   chan struct{}
}

// ptzTours holds at most one tour per camera.
type ptzTours struct {
	mu sync.Mutex
	m  map[string]*tourRun
	wg sync.WaitGroup
}

func (t *ptzTours) get(cam string) TourState {
	t.mu.Lock()
	defer t.mu.Unlock()
	if r := t.m[cam]; r != nil {
		st := r.state
		st.Presets = append([]int(nil), r.state.Presets...)
		return st
	}
	return TourState{}
}

// stop ends a running tour (no-op otherwise) and waits for it to finish.
func (t *ptzTours) stop(cam, reason string) bool {
	t.mu.Lock()
	r := t.m[cam]
	if r == nil || !r.state.Running {
		t.mu.Unlock()
		return false
	}
	if r.state.StopReason == "" {
		r.state.StopReason = reason
	}
	r.cancel()
	t.mu.Unlock()
	<-r.done
	return true
}

// stopAll ends every tour (server shutdown).
func (t *ptzTours) stopAll() {
	t.mu.Lock()
	for _, r := range t.m {
		if r.state.Running {
			r.state.StopReason = "server stopping"
			r.cancel()
		}
	}
	t.mu.Unlock()
	t.wg.Wait()
}

func (t *ptzTours) update(cam string, fn func(*TourState)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if r := t.m[cam]; r != nil {
		fn(&r.state)
	}
}

// tourGoto moves the camera to one preset; replaced in tests.
type tourGoto func(ctx context.Context, cam string, preset int) error

func (s *Server) tourMove(ctx context.Context, cam string, preset int) error {
	s.ptzQuiet.hold(cam, presetSettle)
	cctx, cancel := context.WithTimeout(ctx, 2*dvrip.DefaultTimeout)
	defer cancel()
	ctrl := s.ctrl[cam]
	if ctrl == nil {
		return errors.New("this camera has no remote control")
	}
	return ctrl.Preset(cctx, "goto", preset)
}

// validTour checks a start request against the limits.
func validTour(presets []int, dwell, duration time.Duration) error {
	if len(presets) < 2 || len(presets) > tourMaxPresets {
		return fmt.Errorf("choose 2 to %d positions", tourMaxPresets)
	}
	seen := map[int]bool{}
	for _, p := range presets {
		if p < 1 || p > 255 || seen[p] {
			return errors.New("positions must be distinct numbers from 1 to 255")
		}
		seen[p] = true
	}
	if dwell < tourMinDwell || dwell > tourMaxDwell {
		return fmt.Errorf("wait at each position: %s to %s", tourMinDwell, tourMaxDwell)
	}
	if duration < tourMinDuration || duration > tourMaxDuration {
		return fmt.Errorf("tour length: %s to %s", tourMinDuration, tourMaxDuration)
	}
	return nil
}

// start launches a tour, replacing a finished one. It refuses while
// another tour of the same camera runs.
func (t *ptzTours) start(cam, user string, presets []int, dwell, duration time.Duration, move tourGoto, now func() time.Time) (TourState, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.m == nil {
		t.m = map[string]*tourRun{}
	}
	if r := t.m[cam]; r != nil && r.state.Running {
		return TourState{}, errTourBusy
	}
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	start := now()
	r := &tourRun{cancel: cancel, done: make(chan struct{}), state: TourState{
		Running: true, Presets: append([]int(nil), presets...), DwellSeconds: int(dwell / time.Second),
		StartedBy: user, StartedAt: start.UTC(), EndsAt: start.Add(duration).UTC(),
	}}
	t.m[cam] = r
	t.wg.Add(1)
	go func() {
		defer t.wg.Done()
		defer close(r.done)
		defer cancel()
		reason := t.loop(ctx, cam, presets, dwell, move)
		t.update(cam, func(st *TourState) {
			st.Running, st.Current, st.StoppedAt = false, 0, now().UTC()
			if st.StopReason == "" {
				st.StopReason = reason
			}
		})
	}()
	return r.state, nil
}

var errTourBusy = errors.New("a tour is already running on this camera")

// loop visits the presets until ctx ends; it returns why it stopped.
func (t *ptzTours) loop(ctx context.Context, cam string, presets []int, dwell time.Duration, move tourGoto) string {
	failures := 0
	for {
		for _, p := range presets {
			if ctx.Err() != nil {
				return tourEndReason(ctx)
			}
			t.update(cam, func(st *TourState) { st.Current = p })
			if err := move(ctx, cam, p); err != nil {
				if ctx.Err() != nil {
					return tourEndReason(ctx)
				}
				failures++
				msg := err.Error()
				t.update(cam, func(st *TourState) { st.LastError = msg })
				if failures >= tourMaxFailures {
					return "the camera did not answer"
				}
			} else {
				failures = 0
			}
			select {
			case <-ctx.Done():
				return tourEndReason(ctx)
			case <-time.After(dwell):
			}
		}
	}
}

func tourEndReason(ctx context.Context) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "time is up"
	}
	return "stopped"
}

// ---------- handlers ----------

func (s *Server) getTour(w http.ResponseWriter, r *http.Request) {
	cam := s.camera(w, r)
	if cam == nil {
		return
	}
	writeJSON(w, http.StatusOK, s.tours.get(cam.ID))
}

// putTour starts a tour: {"presets":[1,2,3],"dwell_seconds":20,"hours":1}.
func (s *Server) putTour(w http.ResponseWriter, r *http.Request) {
	cam := s.camera(w, r)
	if cam == nil {
		return
	}
	var body struct {
		Presets      []int   `json:"presets"`
		DwellSeconds int     `json:"dwell_seconds"`
		Hours        float64 `json:"hours"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.Hours == 0 {
		body.Hours = tourDefaultHours
	}
	dwell := time.Duration(body.DwellSeconds) * time.Second
	duration := time.Duration(body.Hours * float64(time.Hour))
	if err := validTour(body.Presets, dwell, duration); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	move := s.tourGoto
	if move == nil {
		move = s.tourMove
	}
	st, err := s.tours.start(cam.ID, userFrom(r).Username, body.Presets, dwell, duration, move, time.Now)
	detail := fmt.Sprintf("%v every %s for %s", body.Presets, dwell, duration)
	s.audit(r, "camera.tour.start", cam.ID, err == nil, detail)
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) deleteTour(w http.ResponseWriter, r *http.Request) {
	cam := s.camera(w, r)
	if cam == nil {
		return
	}
	if s.tours.stop(cam.ID, "stopped by "+userFrom(r).Username) {
		s.audit(r, "camera.tour.stop", cam.ID, true, "")
	}
	writeJSON(w, http.StatusOK, s.tours.get(cam.ID))
}

// takeControl ends a tour when someone moves the camera by hand.
func (s *Server) takeControl(r *http.Request, cam string) {
	if u := userFrom(r); u != nil {
		s.tours.stop(cam, "manual control by "+u.Username)
	}
}
