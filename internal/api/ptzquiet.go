package api

import (
	"sync"
	"time"
)

// While a camera pans, tilts or zooms, the whole picture changes and its
// motion detector fires: those "events" are the camera itself, not
// something in front of it. Opening them took a snapshot, woke the person
// detector and sent notifications in the middle of the movement, adding
// server load exactly while the operator is watching the live picture.
const (
	// ptzSettle covers a held move (each keepalive extends it) plus the
	// time the image needs to settle after the stop.
	ptzSettle = 3 * time.Second
	// presetSettle covers a goto, which runs on its own for several seconds.
	presetSettle = 15 * time.Second
)

// quietWindows tracks, per camera, until when motion is ignored.
type quietWindows struct {
	mu    sync.Mutex
	until map[string]time.Time
	now   func() time.Time
}

func (q *quietWindows) clock() time.Time {
	if q.now != nil {
		return q.now()
	}
	return time.Now()
}

// hold ignores motion of cameraID for d from now (never shortens).
func (q *quietWindows) hold(cameraID string, d time.Duration) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.until == nil {
		q.until = map[string]time.Time{}
	}
	if t := q.clock().Add(d); t.After(q.until[cameraID]) {
		q.until[cameraID] = t
	}
}

// Quiet reports whether cameraID's motion is being ignored right now.
func (q *quietWindows) Quiet(cameraID string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.clock().Before(q.until[cameraID])
}

// MotionQuiet is the ONVIF watcher's hook: true while cameraID is moving.
func (s *Server) MotionQuiet(cameraID string) bool { return s.ptzQuiet.Quiet(cameraID) }
