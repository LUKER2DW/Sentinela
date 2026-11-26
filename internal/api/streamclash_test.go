package api

import (
	"encoding/json"
	"strings"
	"testing"
)

// An installed config with cameras "cam1" and "cam1_sub" keeps running:
// the go2rtc name both would have is refused for live view (neither camera's
// viewers get the other's picture); the other streams stay.
func TestClashingLiveStreamIsRefused(t *testing.T) {
	ts, srv := newTestServer(t)
	srv.Cfg.Cameras = append(srv.Cfg.Cameras, srv.Cfg.Cameras[0])
	srv.Cfg.Cameras[1].ID, srv.Cfg.Cameras[1].Name = "cam1_sub", "Garagem"
	admin := newClient(t, ts.URL)
	admin.do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`)
	for _, tc := range []struct {
		cam, stream string
		want        int
	}{
		{"cam1", "main", 200},
		{"cam1", "sub", 409},      // go2rtc "cam1_sub" is shared
		{"cam1_sub", "main", 409}, // the same name
		{"cam1_sub", "sub", 200},  // "cam1_sub_sub"
	} {
		if code, body := ticket(t, admin, tc.cam, tc.stream); code != tc.want {
			t.Errorf("%s %s ticket = %d %s, want %d", tc.cam, tc.stream, code, body, tc.want)
		}
	}
	if _, err := srv.grabFrame(t.Context(), "cam1"); err == nil {
		t.Error("a snapshot must not read the shared substream")
	}
}

// The settings editor refuses a new camera whose id clashes with another
// camera's go2rtc stream.
func TestSettingsRefuseClashingNewCamera(t *testing.T) {
	c, _ := settingsServer(t)
	_, m, _ := c.do("GET", "/api/settings", "")
	var doc SettingsDoc
	b, _ := json.Marshal(m["settings"])
	_ = json.Unmarshal(b, &doc)
	doc.Cameras = append(doc.Cameras, SettingsCamera{ID: "cam1_sub", Name: "Nova", Host: "192.168.1.31", Vendor: "hikvision", Record: true, User: "admin", Password: "Garage55pass"})
	req, _ := json.Marshal(doc)
	if code, _, body := c.do("PUT", "/api/settings", string(req)); code != 400 || !strings.Contains(body, "go2rtc stream") {
		t.Fatalf("new camera clashing with cam1's substream: %d %s", code, body)
	}
}
