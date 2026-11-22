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
  - {id: r, host: 10.0.0.6, vendor: reolink, onvif: false, user_env: U, password_env: P}
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
		{"h", true, false, true},       // PTZ over ONVIF; talk uses the RTSP backchannel
		{"d", false, false, false},     // other vendors: view and record only by default
		{"r", false, false, false},     // ONVIF off: no ONVIF PTZ either
	}
	for _, tc := range cases {
		cam := c.Camera(tc.id)
		if cam.HasPTZ() != tc.ptz || cam.HasLight() != tc.light || cam.HasTalk() != tc.talk {
			t.Errorf("%s: ptz/light/talk = %v/%v/%v, want %v/%v/%v", tc.id, cam.HasPTZ(), cam.HasLight(), cam.HasTalk(), tc.ptz, tc.light, tc.talk)
		}
	}
	for id, want := range map[string]string{"x": PTZDVRIP, "fixed": "", "h": PTZONVIF, "d": "", "r": ""} {
		if got := c.Camera(id).PTZDriver(); got != want {
			t.Errorf("%s: PTZ driver %q, want %q", id, got, want)
		}
	}
	if c.Camera("r").UsesONVIF() || !c.Camera("d").UsesONVIF() {
		t.Error("onvif switch not honoured")
	}
	for id, port := range map[string]int{"x": 8899, "h": 80, "r": 8000} {
		if got := c.Camera(id).ONVIFPort; got != port {
			t.Errorf("%s: ONVIF port %d, want %d", id, got, port)
		}
	}
	if got := c.Camera("r").Go2RTCURL(0); got != "rtsp://${U}:${P}@10.0.0.6:554/Preview_01_main" {
		t.Errorf("reolink main: %s", got)
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

func TestStreamPathRejectsEnvExpansion(t *testing.T) {
	cases := []struct {
		path string
		ok   bool
	}{
		{"/stream1", true},
		{"/live?x=1&y=2#a", true},
		{"/Streaming/Channels/{channel}01", true},
		{"/cam/realmonitor?channel={channel}&subtype=0", true},
		{"/u={user}&p={password}", true},
		{"/${SECRET}", false},
		{"/a$b", false},
		{"/a{b", false},
		{"/a}b", false},
		{"/{channel", false},
		{"/{chan{channel}nel}", false},
		{"/x?q=${P}", false},
	}
	for _, c := range cases {
		for _, field := range []string{"rtsp_main", "rtsp_sub"} {
			extra := ""
			if field == "rtsp_sub" {
				extra = "rtsp_main: /m, "
			}
			yml := "cameras:\n  - {id: a, host: 10.0.0.2, vendor: generic, " + extra + field + ": '" + c.path + "', user_env: U, password_env: P}\n"
			_, err := Parse([]byte(yml), env(map[string]string{"U": "admin", "P": "Pa55word"}))
			if (err == nil) != c.ok {
				t.Errorf("%s %q: err = %v, want ok=%v", field, c.path, err, c.ok)
			}
		}
	}
}

func TestCredentialEnvNames(t *testing.T) {
	long64, long65 := strings.Repeat("A", 64), strings.Repeat("A", 65)
	vars := map[string]string{"U": "admin", "P": "Pa55word", "front_user": "admin", "A-B": "x", "CAM_2_USER": "admin", "CAM_2_PASS": "Pa55word",
		"${X}": "x", "A B": "x", long65: "x", long64: "x", "0_USER": "x"}
	cases := []struct {
		user, pass string
		ok         bool
	}{
		{"U", "P", true},
		{"CAM_2_USER", "CAM_2_PASS", true},
		{"0_USER", "P", true},
		{long64, "P", true},
		{"front_user", "P", false},
		{"A-B", "P", false},
		{"U", "${X}", false},
		{"A B", "P", false},
		{long65, "P", false},
	}
	for _, c := range cases {
		yml := "cameras:\n  - {id: a, host: 10.0.0.2, user_env: '" + c.user + "', password_env: '" + c.pass + "'}\n"
		_, err := Parse([]byte(yml), env(vars))
		if (err == nil) != c.ok {
			t.Errorf("%q/%q: err = %v, want ok=%v", c.user, c.pass, err, c.ok)
		}
	}
}

func TestONVIFSettings(t *testing.T) {
	creds := env(map[string]string{"U": "admin", "P": "Pa55word"})
	for name, tc := range map[string]struct {
		yaml string
		ok   bool
	}{
		"custom path":             {"{id: a, host: 10.0.0.2, vendor: axis, onvif_path: /onvif/services, user_env: U, password_env: P}", true},
		"path without slash":      {"{id: a, host: 10.0.0.2, vendor: axis, onvif_path: onvif, user_env: U, password_env: P}", false},
		"path with a query":       {"{id: a, host: 10.0.0.2, vendor: axis, onvif_path: /x?y=1, user_env: U, password_env: P}", false},
		"path with an escape":     {"{id: a, host: 10.0.0.2, vendor: axis, onvif_path: /x%2f, user_env: U, password_env: P}", false},
		"path with a host":        {"{id: a, host: 10.0.0.2, vendor: axis, onvif_path: 'http://h/x', user_env: U, password_env: P}", false},
		"camera motion, no onvif": {"{id: a, host: 10.0.0.2, vendor: tapo, onvif: false, motion: camera, user_env: U, password_env: P}", false},
		"video motion, no onvif":  {"{id: a, host: 10.0.0.2, vendor: tapo, onvif: false, motion: video, user_env: U, password_env: P}", true},
		"onvif ptz, no onvif":     {"{id: a, host: 10.0.0.2, vendor: tapo, onvif: false, features: {ptz: true}, user_env: U, password_env: P}", false},
	} {
		c, err := Parse([]byte("cameras:\n  - "+tc.yaml+"\n"), creds)
		if (err == nil) != tc.ok {
			t.Errorf("%s: err=%v", name, err)
			continue
		}
		if err == nil && c.Cameras[0].ONVIFPath == "" {
			t.Errorf("%s: no default onvif path", name)
		}
	}
}
