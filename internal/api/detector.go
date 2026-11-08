package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/LUKER2DW/sentinela/internal/auth"
)

// Object detector integration (ADR-0016). The detector sidecar runs on the
// same host and talks to /internal/detector/* over loopback with a bearer
// token kept in <data>/detector/token (same uid). It is woken by the
// camera's motion events and reports people, vehicles and animals that are
// inside the zones configured here.

// detectorKinds maps what the detector reports to event types.
var detectorKinds = map[string]string{"person": "human", "vehicle": "vehicle", "animal": "animal"}

const (
	detectorStale    = 90 * time.Second // status older than this = detector down
	maxZones         = 10
	maxZonePoints    = 32
	detectorEventMax = 3 << 20
)

// Zone is a polygon (normalised 0..1 coordinates) that counts the listed
// object kinds when their base (feet, wheels) is inside it.
type Zone struct {
	Name    string       `json:"name"`
	Points  [][2]float64 `json:"points"`
	Objects []string     `json:"objects"`
}

// DetectionSettings is stored per camera in the settings table. Fields
// added later get their defaults from defaultDetection: stored JSON and PUT
// bodies are decoded over it (or over the current settings).
type DetectionSettings struct {
	Enabled  bool    `json:"enabled"`
	MinScore float64 `json:"min_score"`
	Zones    []Zone  `json:"zones"`
	// Objects are the kinds watched over the whole picture when there are
	// no zones.
	Objects []string `json:"objects"`
	// IgnoreParked: objects that stay in place (a parked car) stop counting.
	IgnoreParked bool `json:"ignore_parked"`
	// MinSize ignores objects shorter than this share of the picture
	// height (far away, on the street); 0 = no limit.
	MinSize float64 `json:"min_size"`
}

func defaultDetection() DetectionSettings {
	return DetectionSettings{Enabled: true, MinScore: 0.45, Zones: []Zone{}, Objects: []string{"person"}, IgnoreParked: true}
}

func (d DetectionSettings) validate() error {
	if d.MinScore < 0.25 || d.MinScore > 0.9 {
		return errors.New("min_score must be between 0.25 and 0.9")
	}
	if d.MinSize < 0 || d.MinSize > 0.3 {
		return errors.New("min_size must be between 0 and 0.3")
	}
	if len(d.Objects) == 0 {
		return errors.New("choose at least one kind of object")
	}
	for _, o := range d.Objects {
		if _, ok := detectorKinds[o]; !ok {
			return fmt.Errorf("unknown object %q", o)
		}
	}
	if len(d.Zones) > maxZones {
		return fmt.Errorf("at most %d zones", maxZones)
	}
	for i, z := range d.Zones {
		if strings.TrimSpace(z.Name) == "" || len([]rune(z.Name)) > 40 {
			return fmt.Errorf("zone %d: name must have 1 to 40 characters", i+1)
		}
		if len(z.Points) < 3 || len(z.Points) > maxZonePoints {
			return fmt.Errorf("zone %q: 3 to %d points", z.Name, maxZonePoints)
		}
		for _, p := range z.Points {
			if p[0] < 0 || p[0] > 1 || p[1] < 0 || p[1] > 1 {
				return fmt.Errorf("zone %q: points must be inside the picture", z.Name)
			}
		}
		if len(z.Objects) == 0 {
			return fmt.Errorf("zone %q: choose at least one kind of object", z.Name)
		}
		for _, o := range z.Objects {
			if _, ok := detectorKinds[o]; !ok {
				return fmt.Errorf("zone %q: unknown object %q", z.Name, o)
			}
		}
	}
	return nil
}

type detectorState struct {
	mu       sync.Mutex
	token    string
	motion   map[string]bool
	probe    map[string]time.Time // camera -> analyse without motion until (zone editor)
	open     map[string]int64     // camera|type -> open event id
	status   json.RawMessage
	statusAt time.Time
}

// probeFor is how long one probe request keeps the detector analysing.
const probeFor = 20 * time.Second

func detectionKey(cameraID string) string { return "detection." + cameraID }

func (s *Server) detection(ctx context.Context, cameraID string) DetectionSettings {
	d := defaultDetection()
	if raw := s.Store.Setting(ctx, detectionKey(cameraID), ""); raw != "" {
		_ = json.Unmarshal([]byte(raw), &d)
	}
	if d.Zones == nil {
		d.Zones = []Zone{}
	}
	if len(d.Objects) == 0 {
		d.Objects = []string{"person"}
	}
	return d
}

// loadDetectorToken reads or creates the shared secret (0640, same uid as
// the detector container).
func loadDetectorToken(dataDir string) (string, error) {
	return loadToken(filepath.Join(dataDir, "detector", "token"))
}

// detectorHealthy: the sidecar reported within detectorStale.
func (s *Server) detectorHealthy() bool {
	s.det.mu.Lock()
	defer s.det.mu.Unlock()
	return !s.det.statusAt.IsZero() && time.Since(s.det.statusAt) < detectorStale
}

// noteMotion records the camera's motion state (fed by the ONVIF watcher).
func (s *Server) noteMotion(cameraID string, active bool) {
	s.det.mu.Lock()
	s.det.motion[cameraID] = active
	s.det.mu.Unlock()
}

// ---------- internal endpoints (loopback + token) ----------

func (s *Server) detectorOnly(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !fromLoopback(r) || s.det.token == "" || !ok || !auth.EqualToken(tok, s.det.token) {
			writeErr(w, http.StatusForbidden, "forbidden")
			return
		}
		h(w, r)
	})
}

// detectorConfig: cameras to watch, their substream on go2rtc's loopback
// RTSP restream, zones and sensitivity. A camera is listed when object
// detection is on for it, or when its motion must come from the video
// (detect false: motion events only).
func (s *Server) detectorConfig(w http.ResponseWriter, r *http.Request) {
	type camDTO struct {
		ID           string   `json:"id"`
		Stream       string   `json:"stream"`
		Detect       bool     `json:"detect"`
		VideoMotion  bool     `json:"video_motion"`
		MinScore     float64  `json:"min_score"`
		Zones        []Zone   `json:"zones"`
		Objects      []string `json:"objects"`
		IgnoreParked bool     `json:"ignore_parked"`
		MinSize      float64  `json:"min_size"`
	}
	out := []camDTO{}
	for i := range s.Cfg.Cameras {
		c := &s.Cfg.Cameras[i]
		d := s.detection(r.Context(), c.ID)
		video := s.videoMotion(c)
		if !d.Enabled && !video {
			continue
		}
		out = append(out, camDTO{
			ID: c.ID, Stream: "rtsp://127.0.0.1:8554/" + c.ID + "_sub", Detect: d.Enabled, VideoMotion: video,
			MinScore: d.MinScore, Zones: d.Zones, Objects: d.Objects, IgnoreParked: d.IgnoreParked, MinSize: d.MinSize,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"cameras": out})
}

func (s *Server) detectorMotion(w http.ResponseWriter, _ *http.Request) {
	s.det.mu.Lock()
	m := make(map[string]bool, len(s.det.motion))
	for k, v := range s.det.motion {
		m[k] = v
	}
	probe := map[string]bool{}
	now := time.Now()
	for k, until := range s.det.probe {
		if now.Before(until) {
			probe[k] = true
		} else {
			delete(s.det.probe, k)
		}
	}
	s.det.mu.Unlock()
	// Cameras turning on a PTZ command: motion from their video is the
	// camera's own movement, so the detector ignores it meanwhile.
	quiet := map[string]bool{}
	for _, c := range s.Cfg.Cameras {
		if s.ptzQuiet.Quiet(c.ID) {
			quiet[c.ID] = true
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"motion": m, "probe": probe, "quiet": quiet})
}

// detectorObjects relays what the detector sees in each analysis to the
// browsers (live overlay, zone editor). Nothing is stored.
func (s *Server) detectorObjects(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	var body struct {
		Camera string `json:"camera"`
	}
	if err != nil || json.Unmarshal(raw, &body) != nil || s.Cfg.Camera(body.Camera) == nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	s.hub.PublishObjects(body.Camera, raw)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) detectorStatus(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	if err != nil || !json.Valid(raw) {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	s.det.mu.Lock()
	s.det.status, s.det.statusAt = raw, time.Now()
	s.det.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// detectorEvent opens or closes an event for one kind of object. A start
// carries the analysed frame as JPEG: it becomes the event snapshot.
func (s *Server) detectorEvent(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Camera  string            `json:"camera"`
		Kind    string            `json:"kind"`
		Active  bool              `json:"active"`
		Objects []json.RawMessage `json:"objects"`
		JPEG    string            `json:"jpeg"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, detectorEventMax))
	if err := dec.Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	typ, ok := detectorKinds[body.Kind]
	source := "detector"
	if body.Kind == "motion" { // motion found in the video (cameras that report none)
		typ, ok, source = "motion", true, "video"
	}
	if !ok || s.Cfg.Camera(body.Camera) == nil || len(body.Objects) > 50 {
		writeErr(w, http.StatusBadRequest, "unknown camera or kind")
		return
	}
	now := time.Now()
	key := body.Camera + "|" + typ
	ctx := r.Context()
	s.det.mu.Lock()
	id, isOpen := s.det.open[key]
	s.det.mu.Unlock()
	switch {
	case body.Active && !isOpen && typ == "motion" && s.ptzQuiet.Quiet(body.Camera):
		// The camera is turning: not motion in the scene.
	case body.Active && !isOpen:
		meta, _ := json.Marshal(map[string]any{"source": source, "objects": body.Objects})
		var err error
		if id, err = s.Store.OpenEvent(ctx, body.Camera, typ, now, string(meta)); err != nil {
			writeErr(w, http.StatusInternalServerError, "database error")
			return
		}
		s.det.mu.Lock()
		s.det.open[key] = id
		s.det.mu.Unlock()
		haveSnap := false
		if jpg, err := base64.StdEncoding.DecodeString(body.JPEG); err == nil && len(jpg) > 2 && jpg[0] == 0xFF && jpg[1] == 0xD8 {
			haveSnap = s.saveSnapshot(body.Camera, id, jpg) == nil
		}
		s.eventStarted(body.Camera, typ, id, now, haveSnap)
	case !body.Active && isOpen:
		_ = s.Store.CloseEvent(ctx, id, now)
		s.det.mu.Lock()
		delete(s.det.open, key)
		s.det.mu.Unlock()
		s.hub.Publish(LiveEvent{CameraID: body.Camera, Type: typ, Active: false, At: now.UTC()})
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "event": id})
}

// closeStaleDetections ends events left open by a detector that stopped
// reporting (restart, crash). Called periodically by the server.
func (s *Server) closeStaleDetections(ctx context.Context) {
	if s.detectorHealthy() {
		return
	}
	s.det.mu.Lock()
	open := s.det.open
	s.det.open = map[string]int64{}
	s.det.mu.Unlock()
	for _, id := range open {
		_ = s.Store.CloseEvent(ctx, id, time.Now())
	}
}

// WatchDetector closes detections left open by a vanished detector.
func (s *Server) WatchDetector(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.closeStaleDetections(ctx)
		}
	}
}

// ---------- user endpoints ----------

func (s *Server) getDetection(w http.ResponseWriter, r *http.Request) {
	cam := s.camera(w, r)
	if cam == nil {
		return
	}
	writeJSON(w, http.StatusOK, s.detection(r.Context(), cam.ID))
}

func (s *Server) putDetection(w http.ResponseWriter, r *http.Request) {
	cam := s.camera(w, r)
	if cam == nil {
		return
	}
	d := s.detection(r.Context(), cam.ID) // fields an older app does not send stay
	if err := readJSON(r, &d); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if d.Zones == nil {
		d.Zones = []Zone{}
	}
	for i := range d.Zones {
		d.Zones[i].Name = strings.TrimSpace(d.Zones[i].Name)
		slices.Sort(d.Zones[i].Objects)
		d.Zones[i].Objects = slices.Compact(d.Zones[i].Objects)
	}
	slices.Sort(d.Objects)
	d.Objects = slices.Compact(d.Objects)
	if err := d.validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	raw, _ := json.Marshal(d)
	if err := s.Store.SetSetting(r.Context(), detectionKey(cam.ID), string(raw)); err != nil {
		writeErr(w, http.StatusInternalServerError, "database error")
		return
	}
	s.audit(r, "detection.update", cam.ID, true, fmt.Sprintf("enabled=%t zones=%d min=%.2f objects=%s parked=%t size=%.2f",
		d.Enabled, len(d.Zones), d.MinScore, strings.Join(d.Objects, "+"), !d.IgnoreParked, d.MinSize))
	writeJSON(w, http.StatusOK, d)
}

// probeDetection keeps the detector analysing a camera for a while without
// motion, so the live overlay and the zone editor can show what it sees
// (the camera's own motion detection fires rarely). It never opens events.
func (s *Server) probeDetection(w http.ResponseWriter, r *http.Request) {
	cam := s.camera(w, r)
	if cam == nil {
		return
	}
	s.det.mu.Lock()
	s.det.probe[cam.ID] = time.Now().Add(probeFor)
	s.det.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "seconds": int(probeFor.Seconds())})
}

// frame returns a current substream picture for the zone editor (one
// go2rtc keyframe decode, ADR-0015), cached for a few seconds per camera.
func (s *Server) frame(w http.ResponseWriter, r *http.Request) {
	cam := s.camera(w, r)
	if cam == nil {
		return
	}
	s.frames.mu.Lock()
	defer s.frames.mu.Unlock()
	if s.frames.m == nil {
		s.frames.m = map[string]cachedFrame{}
	}
	f, ok := s.frames.m[cam.ID]
	if !ok || time.Since(f.at) > 5*time.Second {
		ctx, cancel := context.WithTimeout(r.Context(), snapshotTimeout)
		defer cancel()
		b, err := s.grabFrame(ctx, cam.ID)
		if err != nil {
			writeErr(w, http.StatusBadGateway, "could not get a picture from the camera")
			return
		}
		f = cachedFrame{at: time.Now(), jpeg: b}
		s.frames.m[cam.ID] = f
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(f.jpeg)
}

type cachedFrame struct {
	at   time.Time
	jpeg []byte
}

type frameCache struct {
	mu sync.Mutex
	m  map[string]cachedFrame
}

// detectorInfo is the System page's view of the sidecar.
func (s *Server) detectorInfo() map[string]any {
	s.det.mu.Lock()
	defer s.det.mu.Unlock()
	out := map[string]any{"healthy": !s.det.statusAt.IsZero() && time.Since(s.det.statusAt) < detectorStale}
	if !s.det.statusAt.IsZero() {
		out["last_seen"] = s.det.statusAt.UTC()
		out["status"] = s.det.status
	}
	return out
}
