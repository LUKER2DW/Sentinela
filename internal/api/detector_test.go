package api

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func detectorReq(t *testing.T, srv *Server, base, method, path, token, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, base+path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

func TestDetectorIntegration(t *testing.T) {
	ts, srv := newTestServer(t)
	tok := srv.det.token
	if len(tok) < 32 {
		t.Fatalf("token not created: %q", tok)
	}

	// Token required (the test server listens on loopback).
	for _, bad := range []string{"", "wrong"} {
		if code, _ := detectorReq(t, srv, ts.URL, "GET", "/internal/detector/config", bad, ""); code != 403 {
			t.Fatalf("token %q accepted: %d", bad, code)
		}
	}
	code, body := detectorReq(t, srv, ts.URL, "GET", "/internal/detector/config", tok, "")
	if code != 200 || !strings.Contains(body, `"stream":"rtsp://127.0.0.1:8554/cam1_sub"`) || !strings.Contains(body, `"zones":[]`) {
		t.Fatalf("config %d %s", code, body)
	}

	// Zones through the user API.
	admin := newClient(t, ts.URL)
	admin.do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`)
	bad := `{"enabled":true,"min_score":0.5,"zones":[{"name":"x","points":[[0,0],[1,0]],"objects":["person"]}]}`
	if code, _, _ := admin.do("PUT", "/api/cameras/cam1/detection", bad); code != 400 {
		t.Fatalf("2-point zone accepted: %d", code)
	}
	good := `{"enabled":true,"min_score":0.5,"zones":[{"name":"Portão","points":[[0.2,0.5],[0.8,0.5],[0.8,1],[0.2,1]],"objects":["person","person","vehicle"]}]}`
	if code, _, body := admin.do("PUT", "/api/cameras/cam1/detection", good); code != 200 {
		t.Fatalf("put zones %d %s", code, body)
	}
	_, body = detectorReq(t, srv, ts.URL, "GET", "/internal/detector/config", tok, "")
	if !strings.Contains(body, `"name":"Portão"`) || !strings.Contains(body, `"objects":["person","vehicle"]`) {
		t.Fatalf("zones not in config: %s", body)
	}

	// Camera motion wakes the detector.
	srv.HandleEvent("cam1", "motion", 1, true, time.Now())
	if _, body := detectorReq(t, srv, ts.URL, "GET", "/internal/detector/motion", tok, ""); !strings.Contains(body, `"cam1":true`) {
		t.Fatalf("motion %s", body)
	}

	// A person: event with the detector's picture as snapshot; repeats ignored.
	jpg := base64.StdEncoding.EncodeToString([]byte{0xFF, 0xD8, 0xFF, 0xD9})
	start := `{"camera":"cam1","kind":"person","active":true,"objects":[{"x":0.5,"y":0.6,"w":0.1,"h":0.3,"score":0.8,"zones":["Portão"]}],"jpeg":"` + jpg + `"}`
	code, body = detectorReq(t, srv, ts.URL, "POST", "/internal/detector/event", tok, start)
	var r struct{ Event int64 }
	_ = json.Unmarshal([]byte(body), &r)
	if code != 200 || r.Event == 0 {
		t.Fatalf("event %d %s", code, body)
	}
	var again struct{ Event int64 }
	_, body2 := detectorReq(t, srv, ts.URL, "POST", "/internal/detector/event", tok, start)
	if _ = json.Unmarshal([]byte(body2), &again); again.Event != r.Event {
		t.Fatalf("repeat start opened another event: %s vs %s", body2, body)
	}
	e, err := srv.Store.EventByID(t.Context(), r.Event)
	if err != nil || e.Type != "human" || !srv.hasSnapshot(*e) || !strings.Contains(e.Meta, "Portão") {
		t.Fatalf("stored event %+v %v", e, err)
	}
	detectorReq(t, srv, ts.URL, "POST", "/internal/detector/event", tok, `{"camera":"cam1","kind":"person","active":false,"objects":[]}`)
	if e, _ := srv.Store.EventByID(t.Context(), r.Event); e.End == nil {
		t.Fatal("stop did not close the event")
	}
	if code, _ := detectorReq(t, srv, ts.URL, "POST", "/internal/detector/event", tok, `{"camera":"cam9","kind":"person","active":true}`); code != 400 {
		t.Fatalf("unknown camera accepted: %d", code)
	}

	// Status makes it healthy.
	if srv.detectorHealthy() {
		t.Fatal("healthy before any status")
	}
	detectorReq(t, srv, ts.URL, "POST", "/internal/detector/status", tok, `{"inference_ms":150}`)
	if !srv.detectorHealthy() {
		t.Fatal("not healthy after status")
	}
}

func TestDetectorSettingsProbeAndOverlay(t *testing.T) {
	ts, srv := newTestServer(t)
	tok := srv.det.token
	admin := newClient(t, ts.URL)
	admin.do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`)

	// New fields have defaults, and a body without them (older app) keeps them.
	_, _, body := admin.do("GET", "/api/cameras/cam1/detection", "")
	if !strings.Contains(body, `"objects":["person"]`) || !strings.Contains(body, `"ignore_parked":true`) || !strings.Contains(body, `"min_size":0`) {
		t.Fatalf("defaults %s", body)
	}
	admin.do("PUT", "/api/cameras/cam1/detection", `{"enabled":true,"min_score":0.45,"zones":[],"objects":["vehicle","person","person"],"ignore_parked":false,"min_size":0.05}`)
	if code, _, body := admin.do("PUT", "/api/cameras/cam1/detection", `{"enabled":true,"min_score":0.6,"zones":[]}`); code != 200 ||
		!strings.Contains(body, `"objects":["person","vehicle"]`) || !strings.Contains(body, `"ignore_parked":false`) || !strings.Contains(body, `"min_size":0.05`) {
		t.Fatalf("old-app put %d %s", code, body)
	}
	for _, bad := range []string{`{"objects":[]}`, `{"objects":["tree"]}`, `{"min_size":0.5}`} {
		if code, _, _ := admin.do("PUT", "/api/cameras/cam1/detection", bad); code != 400 {
			t.Fatalf("%s accepted: %d", bad, code)
		}
	}
	if _, body := detectorReq(t, srv, ts.URL, "GET", "/internal/detector/config", tok, ""); !strings.Contains(body, `"ignore_parked":false`) || !strings.Contains(body, `"min_size":0.05`) {
		t.Fatalf("config %s", body)
	}

	// Probe: the detector analyses without motion for a while.
	if _, body := detectorReq(t, srv, ts.URL, "GET", "/internal/detector/motion", tok, ""); strings.Contains(body, `"probe":{"cam1"`) {
		t.Fatalf("probe before asking: %s", body)
	}
	if code, _, _ := admin.do("POST", "/api/cameras/cam1/detection/probe", ""); code != 200 {
		t.Fatalf("probe %d", code)
	}
	if _, body := detectorReq(t, srv, ts.URL, "GET", "/internal/detector/motion", tok, ""); !strings.Contains(body, `"probe":{"cam1":true}`) {
		t.Fatalf("probe %s", body)
	}

	// Boxes are relayed to subscribers as "objects".
	c := srv.hub.subscribe()
	defer srv.hub.unsubscribe(c)
	if code, _ := detectorReq(t, srv, ts.URL, "POST", "/internal/detector/objects", tok, `{"camera":"nope","objects":[]}`); code != 400 {
		t.Fatalf("unknown camera accepted: %d", code)
	}
	msg := `{"camera":"cam1","at":1,"objects":[{"kind":"person","x":0.1,"y":0.2,"w":0.1,"h":0.3,"score":0.8,"counted":true}]}`
	if code, _ := detectorReq(t, srv, ts.URL, "POST", "/internal/detector/objects", tok, msg); code != 200 {
		t.Fatalf("objects %d", code)
	}
	select {
	case m := <-c:
		if m.event != "objects" || m.cameraID != "cam1" || string(m.data) != msg {
			t.Fatalf("relayed %+v", m)
		}
	case <-time.After(time.Second):
		t.Fatal("objects not relayed")
	}
}
