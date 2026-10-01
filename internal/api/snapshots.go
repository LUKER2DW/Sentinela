package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/LUKER2DW/sentinela/internal/store"
)

// Event snapshots (ADR-0015): one substream keyframe from go2rtc per event,
// at most one per camera every snapshotEvery.
const (
	snapshotEvery   = 10 * time.Second
	snapshotTimeout = 12 * time.Second
	snapshotMax     = 2 << 20
)

type snapshotter struct {
	mu   sync.Mutex
	last map[string]time.Time
}

// HandleEvent receives ONVIF event transitions: live push to browsers, a
// snapshot when an event starts, then a notification (ADR-0013). Motion
// also wakes the object detector (ADR-0016).
func (s *Server) HandleEvent(cameraID, typ string, eventID int64, active bool, at time.Time) {
	if typ == "motion" {
		s.noteMotion(cameraID, active)
	}
	if !active {
		s.hub.Publish(LiveEvent{CameraID: cameraID, Type: typ, Active: false, At: at.UTC()})
		return
	}
	s.eventStarted(cameraID, typ, eventID, at, false)
}

// eventStarted publishes a new event, takes a snapshot unless the caller
// already stored one, then notifies.
func (s *Server) eventStarted(cameraID, typ string, eventID int64, at time.Time, haveSnapshot bool) {
	s.hub.Publish(LiveEvent{CameraID: cameraID, Type: typ, Active: true, At: at.UTC()})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), snapshotTimeout+5*time.Second)
		defer cancel()
		if !haveSnapshot {
			s.takeSnapshot(ctx, cameraID, eventID)
		}
		s.notify(ctx, cameraID, typ, eventID, at)
	}()
}

func (s *Server) snapshotPath(cameraID string, eventID int64) string {
	return filepath.Join(s.Cfg.DataDir, "snapshots", cameraID, strconv.FormatInt(eventID, 10)+".jpg")
}

func (s *Server) takeSnapshot(ctx context.Context, cameraID string, eventID int64) {
	s.snaps.mu.Lock()
	if s.snaps.last == nil {
		s.snaps.last = map[string]time.Time{}
	}
	if time.Since(s.snaps.last[cameraID]) < snapshotEvery {
		s.snaps.mu.Unlock()
		return
	}
	s.snaps.last[cameraID] = time.Now()
	s.snaps.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, snapshotTimeout)
	defer cancel()
	b, err := s.grabFrame(ctx, cameraID)
	if err == nil {
		err = s.saveSnapshot(cameraID, eventID, b)
	}
	if err != nil {
		s.Log.Warn("event snapshot failed", "camera", cameraID, "event", eventID, "err", err)
	}
}

// grabFrame asks go2rtc for one substream keyframe as JPEG.
func (s *Server) grabFrame(ctx context.Context, cameraID string) ([]byte, error) {
	u := s.go2rtc.String() + "/api/frame.jpeg?src=" + cameraID + "_sub"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, errors.New("go2rtc unreachable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("go2rtc: HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, snapshotMax+1))
	if err != nil {
		return nil, err
	}
	if len(b) == 0 || len(b) > snapshotMax {
		return nil, fmt.Errorf("unexpected snapshot size %d", len(b))
	}
	return b, nil
}

// saveSnapshot stores an event picture atomically.
func (s *Server) saveSnapshot(cameraID string, eventID int64, jpg []byte) error {
	if len(jpg) == 0 || len(jpg) > snapshotMax {
		return fmt.Errorf("unexpected snapshot size %d", len(jpg))
	}
	dst := s.snapshotPath(cameraID, eventID)
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".snap-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(jpg)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}

func (s *Server) hasSnapshot(e store.Event) bool {
	_, err := os.Stat(s.snapshotPath(e.CameraID, e.ID))
	return err == nil
}

func (s *Server) eventSnapshot(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid event id")
		return
	}
	e, err := s.Store.EventByID(r.Context(), id)
	if err != nil || !s.canSee(r, e.CameraID) {
		writeErr(w, http.StatusNotFound, "event not found")
		return
	}
	f, err := os.Open(s.snapshotPath(e.CameraID, e.ID))
	if err != nil {
		writeErr(w, http.StatusNotFound, "no snapshot for this event")
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		writeErr(w, http.StatusNotFound, "no snapshot for this event")
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=86400, immutable")
	http.ServeContent(w, r, "", info.ModTime(), f)
}

// PruneCaches trims the disk caches the API keeps on the recordings disk:
// prepared timelapses (hours), Nuvem thumbnails unused for 90 days, and the
// record of finished timelapse jobs. Called hourly.
func (s *Server) PruneCaches() {
	pruneTimelapses(s.timelapseDir(), time.Now())
	s.tl.pruneJobs()
	if s.thumbs != nil {
		s.thumbs.Prune(90 * 24 * time.Hour)
	}
}

// PurgeSnapshots deletes snapshot files older than maxAge (called hourly;
// events of that age are gone too).
func (s *Server) PurgeSnapshots(maxAge time.Duration) {
	root := filepath.Join(s.Cfg.DataDir, "snapshots")
	cut := time.Now().Add(-maxAge)
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil && info.ModTime().Before(cut) {
			_ = os.Remove(p)
		}
		return nil
	})
}
