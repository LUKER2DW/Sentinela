package api

import "testing"

func TestParseProbe(t *testing.T) {
	stderr := `Input #0, rtsp, from 'rtsp://192.168.1.21:554/user=REDACTED':
  Stream #0:0: Video: hevc (Main), yuv420p(tv), 2560x1440, 12 fps, 12 tbr, 90k tbn
  Stream #0:1: Audio: pcm_alaw, 8000 Hz, mono, s16, 64 kb/s`
	v, a := parseProbe(stderr)
	if v != "hevc 2560x1440 12 fps" || a != "pcm_alaw 8000 Hz" {
		t.Fatalf("video %q audio %q", v, a)
	}
	if v, a := parseProbe("Connection refused"); v != "" || a != "" {
		t.Fatalf("parsed garbage: %q %q", v, a)
	}
}

func TestTestCameraValidation(t *testing.T) {
	ts, _ := newTestServer(t)
	admin := newClient(t, ts.URL)
	admin.do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`)
	for _, body := range []string{
		`{"host":"8.8.8.8","user":"admin","password":"x"}`,
		`{"host":"camera.local","user":"admin","password":"x"}`,
		`{"host":"192.168.1.9","user":"admin","password":"a&b"}`,
		`{"host":"192.168.1.9","user":"","password":"x"}`,
	} {
		if code, _, _ := admin.do("POST", "/api/cameras/test", body); code != 400 {
			t.Errorf("%s accepted: %d", body, code)
		}
	}
}
