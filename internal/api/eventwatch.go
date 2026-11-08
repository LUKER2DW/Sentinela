package api

import (
	"sync"
	"time"

	"github.com/LUKER2DW/sentinela/internal/config"
)

// eventWatch remembers whether each camera's ONVIF event subscription is
// delivering, for the System page. Motion events (and so the person
// detector, which waits for motion) depend on it: from 2026-11-03 the
// camera answered with an unreachable address and no motion was recorded
// for three days without anything on screen saying so.
type eventWatch struct {
	mu   sync.Mutex
	cams map[string]eventWatchState
}

type eventWatchState struct {
	OK    bool      `json:"ok"`
	Since time.Time `json:"since"`
	Error string    `json:"error,omitempty"`
}

// NoteEventWatch is the ONVIF watcher's health hook.
func (s *Server) NoteEventWatch(cameraID string, ok bool, reason string) {
	s.evWatch.mu.Lock()
	defer s.evWatch.mu.Unlock()
	if s.evWatch.cams == nil {
		s.evWatch.cams = map[string]eventWatchState{}
	}
	if len(reason) > 200 {
		reason = reason[:200]
	}
	s.evWatch.cams[cameraID] = eventWatchState{OK: ok, Since: time.Now().UTC(), Error: reason}
	if !ok {
		s.Log.Warn("camera events not flowing", "camera", cameraID)
	} else {
		s.Log.Info("camera events flowing", "camera", cameraID)
	}
}

// eventWatchGrace: how long after start a camera may take to deliver its
// first ONVIF events before auto falls back to motion from the video.
const eventWatchGrace = 2 * time.Minute

// videoMotion reports whether the detector should find this camera's
// motion in the video: always with motion: video, never with motion:
// camera; with auto, while its ONVIF events are not flowing (an RTSP-only
// camera never has them; an ONVIF one may lose them, as on 2026-11-03).
func (s *Server) videoMotion(cam *config.Camera) bool {
	switch cam.MotionSource() {
	case config.MotionVideo:
		return true
	case config.MotionCamera:
		return false
	}
	s.evWatch.mu.Lock()
	st, known := s.evWatch.cams[cam.ID]
	s.evWatch.mu.Unlock()
	if known {
		return !st.OK
	}
	return time.Since(s.Started) > eventWatchGrace
}

func (s *Server) eventWatchInfo() map[string]eventWatchState {
	s.evWatch.mu.Lock()
	defer s.evWatch.mu.Unlock()
	out := make(map[string]eventWatchState, len(s.evWatch.cams))
	for k, v := range s.evWatch.cams {
		out[k] = v
	}
	return out
}
