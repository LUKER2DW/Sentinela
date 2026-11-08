package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/LUKER2DW/sentinela/internal/config"
)

// Where motion comes from: the camera's ONVIF events while they flow, the
// video otherwise (auto), or what the camera's motion setting forces.
func TestVideoMotionDecision(t *testing.T) {
	_, srv := newTestServer(t)
	cam := srv.Cfg.Camera("cam1")
	srv.Started = time.Now()
	if srv.videoMotion(cam) {
		t.Fatal("auto, just started: wait for the camera's events first")
	}
	srv.Started = time.Now().Add(-eventWatchGrace - time.Minute)
	if !srv.videoMotion(cam) {
		t.Fatal("auto, no ONVIF events after the grace period (RTSP only): the video")
	}
	srv.NoteEventWatch("cam1", true, "")
	if srv.videoMotion(cam) {
		t.Fatal("auto, events flowing: the camera")
	}
	srv.NoteEventWatch("cam1", false, "unreachable address")
	if !srv.videoMotion(cam) {
		t.Fatal("auto, events stopped: the video")
	}
	cam.Motion = config.MotionCamera
	if srv.videoMotion(cam) {
		t.Fatal("motion: camera never uses the video")
	}
	cam.Motion = config.MotionVideo
	srv.NoteEventWatch("cam1", true, "")
	if !srv.videoMotion(cam) {
		t.Fatal("motion: video always uses the video")
	}
}

func TestVideoMotionEvents(t *testing.T) {
	ts, srv := newTestServer(t)
	tok := srv.det.token
	srv.Cfg.Camera("cam1").Motion = config.MotionVideo
	admin := newClient(t, ts.URL)
	admin.do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`)

	// "motion" is an event the detector reports, not a kind of object to watch.
	if code, _, _ := admin.do("PUT", "/api/cameras/cam1/detection", `{"enabled":true,"min_score":0.45,"zones":[],"objects":["motion"]}`); code != 400 {
		t.Fatalf("motion accepted as an object kind: %d", code)
	}
	// Object detection off: the camera is still watched, for motion only.
	if code, _, body := admin.do("PUT", "/api/cameras/cam1/detection", `{"enabled":false,"min_score":0.45,"zones":[],"objects":["person"]}`); code != 200 {
		t.Fatalf("disable detection: %d %s", code, body)
	}
	_, body := detectorReq(t, srv, ts.URL, "GET", "/internal/detector/config", tok, "")
	if !strings.Contains(body, `"id":"cam1"`) || !strings.Contains(body, `"detect":false`) || !strings.Contains(body, `"video_motion":true`) {
		t.Fatalf("config: %s", body)
	}

	start := `{"camera":"cam1","kind":"motion","active":true,"objects":[]}`
	code, body := detectorReq(t, srv, ts.URL, "POST", "/internal/detector/event", tok, start)
	var r struct {
		Event int64 `json:"event"`
	}
	_ = json.Unmarshal([]byte(body), &r)
	if code != 200 || r.Event == 0 {
		t.Fatalf("motion start: %d %s", code, body)
	}
	ev, err := srv.Store.EventByID(t.Context(), r.Event)
	if err != nil || ev.Type != "motion" || !strings.Contains(ev.Meta, `"source":"video"`) || ev.End != nil {
		t.Fatalf("event %+v err %v", ev, err)
	}
	detectorReq(t, srv, ts.URL, "POST", "/internal/detector/event", tok, `{"camera":"cam1","kind":"motion","active":false,"objects":[]}`)
	if ev, _ = srv.Store.EventByID(t.Context(), r.Event); ev.End == nil {
		t.Fatal("motion stop did not close the event")
	}

	// While a PTZ command turns the camera, its own movement is not motion.
	srv.ptzQuiet.hold("cam1", time.Minute)
	if _, body := detectorReq(t, srv, ts.URL, "GET", "/internal/detector/motion", tok, ""); !strings.Contains(body, `"quiet":{"cam1":true}`) {
		t.Fatalf("quiet not reported: %s", body)
	}
	_, body = detectorReq(t, srv, ts.URL, "POST", "/internal/detector/event", tok, start)
	if r.Event = -1; json.Unmarshal([]byte(body), &r) != nil || r.Event != 0 {
		t.Fatalf("motion opened during a PTZ move: %s", body)
	}
}
