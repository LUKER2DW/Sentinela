package config

import (
	"fmt"
	"path/filepath"
	"slices"
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
		"go2rtc secret": `cameras: [{id: cam1, host: 10.0.0.2, user_env: U, password_env: GO2RTC_API_PASSWORD}]`,
	}
	for name, y := range cases {
		if _, err := Parse([]byte(y), env(map[string]string{"U": "admin", "P": "pw", "GO2RTC_API_PASSWORD": "pw"})); err == nil {
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

func TestProtectedCaps(t *testing.T) {
	cases := []struct {
		yml         string
		total, cam  int
		wantInvalid bool
	}{
		{"", 10, 5, false},
		{"recording: {max_protected_percent: 3}", 3, 3, false},
		{"recording: {max_protected_percent: 20, max_protected_camera_percent: 20}", 20, 20, false},
		{"recording: {max_protected_percent: 41}", 0, 0, true},
		{"recording: {max_protected_percent: 10, max_protected_camera_percent: 11}", 0, 0, true},
		{"recording: {max_protected_percent: -1}", 0, 0, true},
	}
	for _, tc := range cases {
		c, err := Parse([]byte(tc.yml+"\n"), env(nil))
		if tc.wantInvalid {
			if err == nil {
				t.Errorf("%q: accepted", tc.yml)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: %v", tc.yml, err)
			continue
		}
		if c.Recording.MaxProtectedPercent != tc.total || c.Recording.MaxProtectedCameraPercent != tc.cam {
			t.Errorf("%q: caps %d/%d, want %d/%d", tc.yml, c.Recording.MaxProtectedPercent, c.Recording.MaxProtectedCameraPercent, tc.total, tc.cam)
		}
	}
}

// A camera id that is another camera's "<id>_sub" or "<id>_talk" would share
// that go2rtc stream (its viewers could get the other's picture or speaker).
// An installed config with such ids still loads (recording must go on): the
// shared name is left out of go2rtc and only a new camera is refused.
func TestCameraIDsClashingWithStreams(t *testing.T) {
	cam := func(id string, talk bool) string {
		return fmt.Sprintf("  - {id: %s, host: 192.168.1.20, user_env: U, password_env: P, features: {talk: %v}}\n", id, talk)
	}
	parse := func(cams ...string) *Config {
		t.Helper()
		c, err := Parse([]byte("cameras:\n"+strings.Join(cams, "")), env(map[string]string{"U": "admin", "P": "Pa@ss"}))
		if err != nil {
			t.Fatalf("an existing clash must not stop the config loading: %v", err)
		}
		return c
	}
	cases := []struct {
		cams  []string
		clash []string
	}{
		{[]string{cam("cam1", true), cam("cam2", true)}, nil},
		{[]string{cam("cam1_talk", true)}, nil}, // no camera "cam1"
		{[]string{cam("cam1", true), cam("cam1_talk", false)}, []string{"cam1_talk"}},
		{[]string{cam("cam1", false), cam("cam1_talk", false)}, nil}, // cam1 has no talk stream
		{[]string{cam("cam1_sub", false), cam("cam1", false)}, []string{"cam1_sub"}},
		{[]string{cam("front", false), cam("front-sub", false)}, nil},
	}
	for _, tc := range cases {
		c := parse(tc.cams...)
		var got []string
		for name := range c.StreamClashes() {
			got = append(got, name)
		}
		slices.Sort(got)
		if !slices.Equal(got, tc.clash) {
			t.Errorf("%v: clashes %v, want %v", tc.cams, got, tc.clash)
		}
		if err := NewStreamClash(c, nil); (err == nil) != (len(tc.clash) == 0) {
			t.Errorf("%v: adding them = %v", tc.cams, err)
		}
	}

	// x and x_sub: the shared name goes from go2rtc, the rest stays; x_sub's
	// talk stream (picture from its clashing main stream) goes too.
	c := parse(cam("x", false), cam("x_sub", true))
	var b strings.Builder
	if err := c.WriteGo2RTC(&b); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, name := range []string{"x", "x_sub_sub"} {
		if !strings.Contains(out, "\n  "+name+":\n") {
			t.Errorf("stream %s missing:\n%s", name, out)
		}
	}
	for _, name := range []string{"x_sub", "x_sub_talk"} {
		if strings.Contains(out, "\n  "+name+":") || !c.Go2RTCStreamOff(name) {
			t.Errorf("stream %s should be left out:\n%s", name, out)
		}
	}
	// Installed already: only warned about. Added now: refused.
	if err := NewStreamClash(c, c); err != nil {
		t.Errorf("existing clash refused: %v", err)
	}
	if err := NewStreamClash(c, parse(cam("x", false))); err == nil || !strings.Contains(err.Error(), `"x_sub"`) {
		t.Errorf("new clashing camera: %v", err)
	}
}

// The Compose helper holds the Docker socket: by default it runs an image
// pinned by digest, never a local tag a build on the host could move.
func TestDefaultComposeImageIsPinned(t *testing.T) {
	c, err := Parse([]byte("apps: {enabled: true}\n"), env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.Apps.ComposeImage != DefaultComposeImage || !imagePattern.MatchString(DefaultComposeImage) ||
		!strings.Contains(DefaultComposeImage, "@sha256:") {
		t.Fatalf("compose image %q is not pinned by digest", c.Apps.ComposeImage)
	}
}
