package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/LUKER2DW/sentinela/internal/config"
	"github.com/LUKER2DW/sentinela/internal/store"
)

// Each camera gets the PTZ driver its brand and features call for: DVRIP
// for XMEye, ONVIF PTZ for another brand that says it has PTZ, none
// otherwise (and none with ONVIF off).
func TestPTZDriverPerCamera(t *testing.T) {
	cfg, err := config.Parse([]byte(`
data_dir: `+filepath.ToSlash(t.TempDir())+`
go2rtc_api: http://127.0.0.1:1
cameras:
  - {id: xm, host: 127.0.0.1, dvrip_port: 1, user_env: U, password_env: P}
  - {id: xmfixed, host: 127.0.0.1, dvrip_port: 1, user_env: U, password_env: P, features: {ptz: false}}
  - {id: hik, host: 127.0.0.1, vendor: hikvision, user_env: U, password_env: P, features: {ptz: true}}
  - {id: dahua, host: 127.0.0.1, vendor: dahua, user_env: U, password_env: P}
  - {id: tapo, host: 127.0.0.1, vendor: tapo, onvif: false, motion: video, user_env: U, password_env: P}
`), func(k string) string { return map[string]string{"U": "admin", "P": "Sup3rSecret"}[k] })
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv, err := New(cfg, st, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	for id, want := range map[string]string{"xm": "dvrip", "xmfixed": "", "hik": "onvif", "dahua": "", "tapo": ""} {
		drv := srv.ptzDrv[id]
		got := ""
		switch {
		case drv == nil:
		case srv.onvifPTZ[id] != nil:
			got = "onvif"
		default:
			got = "dvrip"
		}
		if got != want {
			t.Errorf("%s: driver %q, want %q", id, got, want)
		}
	}
	// A camera without PTZ answers 501 on the PTZ routes, even for an operator.
	r := httptest.NewRequest("POST", "/api/cameras/dahua/ptz", nil)
	r.SetPathValue("id", "dahua")
	w := httptest.NewRecorder()
	srv.ptz(w, r.WithContext(context.WithValue(r.Context(), userKey, &store.User{Username: "op", Role: "operator"})))
	if w.Code != http.StatusNotImplemented {
		t.Errorf("no PTZ: %d %s", w.Code, w.Body)
	}
	if !srv.videoMotion(cfg.Camera("tapo")) {
		t.Error("a camera with ONVIF off gets its motion from the video")
	}
}
