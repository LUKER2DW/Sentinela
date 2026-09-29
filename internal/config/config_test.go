package config

import (
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestParseDefaultsAndSecrets(t *testing.T) {
	c, err := Parse([]byte(`
cameras:
  - {id: cam1, host: 192.168.1.20, user_env: U, password_env: P}
`), env(map[string]string{"U": "admin", "P": "Pa@ss"}))
	if err != nil {
		t.Fatal(err)
	}
	cam := c.Camera("cam1")
	if cam.ONVIFPort != 8899 || cam.DVRIPPort != 34567 || cam.RetentionDays != 15 || !cam.Recording() || !cam.AudioRecording() {
		t.Fatalf("defaults not applied: %+v", cam)
	}
	if got := cam.RTSPURL(1); got != "rtsp://192.168.1.20:554/user=admin&password=Pa@ss&channel=1&stream=1.sdp?real_stream" {
		t.Fatalf("RTSPURL = %s", got)
	}
}

func TestValidate(t *testing.T) {
	cases := map[string]string{
		"missing creds": `cameras: [{id: cam1, host: 10.0.0.2, user_env: X, password_env: Y}]`,
		"bad id":        `cameras: [{id: "Cam 1", host: 10.0.0.2, user_env: U, password_env: P}]`,
		"dup id":        `cameras: [{id: a, host: 10.0.0.2, user_env: U, password_env: P}, {id: a, host: 10.0.0.3, user_env: U, password_env: P}]`,
		"bad segment":   "recording: {segment_seconds: 5}\ncameras: []",
		"ntfy http":     "ntfy: {public_url: 'http://s.ts.net:8443'}",
		"ntfy path":     "ntfy: {public_url: 'https://s.ts.net:8443/ntfy'}",
		"funnel on lan": "remote: {funnel_listen: '0.0.0.0:8081'}",
		"public ip":     "remote: {public: {domain: '203.0.113.7'}}",
		"public tag":    "remote: {app_tag: 'sentinela-app'}",
	}
	for name, y := range cases {
		if _, err := Parse([]byte(y), env(map[string]string{"U": "admin", "P": "pw"})); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	_, err := Parse([]byte(`cameras: [{id: c, host: 10.0.0.2, user_env: U, password_env: P}]`), env(map[string]string{"U": "admin", "P": "a&b"}))
	if err == nil || !strings.Contains(err.Error(), "must not contain") {
		t.Errorf("'&' in password: err=%v", err)
	}
}
