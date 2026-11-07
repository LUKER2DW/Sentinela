package config

import (
	"path/filepath"
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
	if cam.ONVIFPort != 8899 || cam.DVRIPPort != 34567 || cam.RetentionDays != 0 || c.Recording.MaxDiskPercent != 95 || !cam.Recording() || !cam.AudioRecording() {
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
		"neg retention": "recording: {default_retention_days: -1}\ncameras: []",
		"funnel on lan": "remote: {funnel_listen: '0.0.0.0:8081'}",
		"public lan ip": "remote: {public: {domain: '192.168.1.10'}}",
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

func TestVolumeMarker(t *testing.T) {
	if p := (Recording{VolumeMarker: DefaultVolumeMarker}).VolumeMarkerPath("/data"); p != filepath.Join("/data", DefaultVolumeMarker) {
		t.Fatalf("default marker = %q", p)
	}
	if p := (Recording{VolumeMarker: "none"}).VolumeMarkerPath("/data"); p != "" {
		t.Fatalf("none = %q", p)
	}
	for _, m := range []string{"/etc/passwd", "../x"} {
		c := Config{Recording: Recording{SegmentSeconds: 300, MaxDiskPercent: 95, VolumeMarker: m}, Timezone: "UTC"}
		if err := c.validate(); err == nil || !strings.Contains(err.Error(), "volume_marker") {
			t.Errorf("%s: %v", m, err)
		}
	}
}
