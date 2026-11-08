package config

import (
	"strings"
	"testing"
)

func TestVendorURLs(t *testing.T) {
	c, err := Parse([]byte(`
cameras:
  - {id: x, host: 10.0.0.2, user_env: U, password_env: P}
  - {id: h, host: 10.0.0.3, vendor: hikvision, channel: 2, user_env: U, password_env: P}
  - {id: d, host: 10.0.0.4, vendor: dahua, user_env: U, password_env: P}
  - {id: g, host: 10.0.0.5, vendor: generic, rtsp_main: /live, rtsp_port: 8554, preload: false, user_env: U, password_env: P}
`), env(map[string]string{"U": "admin", "P": "Pa55word"}))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][2]string{
		"x": {"rtsp://10.0.0.2:554/user=admin&password=Pa55word&channel=1&stream=0.sdp?real_stream",
			"rtsp://10.0.0.2:554/user=admin&password=Pa55word&channel=1&stream=1.sdp?real_stream"},
		"h": {"rtsp://admin:Pa55word@10.0.0.3:554/Streaming/Channels/201", "rtsp://admin:Pa55word@10.0.0.3:554/Streaming/Channels/202"},
		"d": {"rtsp://admin:Pa55word@10.0.0.4:554/cam/realmonitor?channel=1&subtype=0", "rtsp://admin:Pa55word@10.0.0.4:554/cam/realmonitor?channel=1&subtype=1"},
		"g": {"rtsp://admin:Pa55word@10.0.0.5:8554/live", "rtsp://admin:Pa55word@10.0.0.5:8554/live"},
	}
	for id, w := range want {
		cam := c.Camera(id)
		if got := cam.RTSPURL(0); got != w[0] {
			t.Errorf("%s main = %s", id, got)
		}
		if got := cam.RTSPURL(1); got != w[1] {
			t.Errorf("%s sub = %s", id, got)
		}
	}
	if !c.Camera("x").HasDVRIP() || c.Camera("h").HasDVRIP() || c.Camera("g").HasSubstream() {
		t.Error("capabilities")
	}
	if c.Camera("x").ONVIFPort != 8899 || c.Camera("h").ONVIFPort != 80 {
		t.Error("ONVIF port defaults")
	}
	var b strings.Builder
	if err := c.WriteGo2RTC(&b); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, s := range []string{
		`"rtsp://10.0.0.2:554/user=${U}&password=${P}&channel=1&stream=0.sdp?real_stream"`,
		`"dvrip://${U}:${P}@10.0.0.2:34567?channel=0&subtype=0&backchannel=1"`,
		`"rtsp://${U}:${P}@10.0.0.3:554/Streaming/Channels/202"`,
		"  x: \"video&audio\"",
	} {
		if !strings.Contains(out, s) {
			t.Errorf("go2rtc config lacks %s:\n%s", s, out)
		}
	}
	if strings.Contains(out, "Pa55word") || strings.Contains(out, "  g: \"video&audio\"") {
		t.Errorf("secret or unwanted preload in go2rtc config:\n%s", out)
	}
}

func TestVendorValidation(t *testing.T) {
	for name, yml := range map[string]string{
		"vendor":       `{id: a, host: 10.0.0.2, vendor: acme, user_env: U, password_env: P}`,
		"generic path": `{id: a, host: 10.0.0.2, vendor: generic, user_env: U, password_env: P}`,
		"bad path":     `{id: a, host: 10.0.0.2, vendor: generic, rtsp_main: "live@x", user_env: U, password_env: P}`,
		"channel":      `{id: a, host: 10.0.0.2, channel: 99, user_env: U, password_env: P}`,
	} {
		if _, err := Parse([]byte("cameras:\n  - "+yml+"\n"), env(map[string]string{"U": "admin", "P": "Pa55word"})); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// '@' is fine in the XMEye path, not in a URL userinfo.
	for vendor, ok := range map[string]bool{"xmeye": true, "hikvision": false} {
		_, err := Parse([]byte("cameras:\n  - {id: a, host: 10.0.0.2, vendor: "+vendor+", user_env: U, password_env: P}\n"),
			env(map[string]string{"U": "admin", "P": "Pa@55word"}))
		if (err == nil) != ok {
			t.Errorf("%s with '@': err=%v", vendor, err)
		}
	}
}

func TestCameraFeatures(t *testing.T) {
	c, err := Parse([]byte(`
cameras:
  - {id: x, host: 10.0.0.2, user_env: U, password_env: P}
  - {id: fixed, host: 10.0.0.3, user_env: U, password_env: P, features: {ptz: false, light: false, talk: false}}
  - {id: h, host: 10.0.0.4, vendor: hikvision, user_env: U, password_env: P, features: {ptz: true, talk: true}}
  - {id: d, host: 10.0.0.5, vendor: dahua, user_env: U, password_env: P}
`), env(map[string]string{"U": "admin", "P": "Pa55word"}))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		id               string
		ptz, light, talk bool
	}{
		{"x", true, true, true},        // XMEye: everything by default
		{"fixed", false, false, false}, // XMEye bullet without motor, light or speaker
		{"h", false, false, true},      // PTZ needs the DVRIP driver; talk uses the RTSP backchannel
		{"d", false, false, false},     // other vendors: view and record only by default
	}
	for _, tc := range cases {
		cam := c.Camera(tc.id)
		if cam.HasPTZ() != tc.ptz || cam.HasLight() != tc.light || cam.HasTalk() != tc.talk {
			t.Errorf("%s: ptz/light/talk = %v/%v/%v, want %v/%v/%v", tc.id, cam.HasPTZ(), cam.HasLight(), cam.HasTalk(), tc.ptz, tc.light, tc.talk)
		}
	}
	if c.Camera("fixed").Go2RTCBackchannel() != "" {
		t.Error("no speaker: go2rtc must not open the DVRIP backchannel")
	}
	if c.Camera("x").Go2RTCBackchannel() == "" {
		t.Error("XMEye with talk: backchannel expected")
	}
}

func TestCameraMotionSource(t *testing.T) {
	base := "cameras:\n  - {id: x, host: 10.0.0.2, user_env: U, password_env: P%s}\n"
	creds := env(map[string]string{"U": "admin", "P": "Pa55word"})
	c, err := Parse([]byte(strings.Replace(base, "%s", "", 1)), creds)
	if err != nil || c.Camera("x").MotionSource() != MotionAuto {
		t.Fatalf("default: %v %v", err, c)
	}
	for _, m := range []string{"auto", "camera", "video"} {
		if c, err := Parse([]byte(strings.Replace(base, "%s", ", motion: "+m, 1)), creds); err != nil || c.Camera("x").MotionSource() != m {
			t.Errorf("motion %s: %v", m, err)
		}
	}
	if _, err := Parse([]byte(strings.Replace(base, "%s", ", motion: sometimes", 1)), creds); err == nil {
		t.Error("an unknown motion source was accepted")
	}
}
