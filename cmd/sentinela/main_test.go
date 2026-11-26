package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/LUKER2DW/sentinela/internal/config"
	"github.com/LUKER2DW/sentinela/internal/store"
)

func TestSplitRoleFlag(t *testing.T) {
	tests := []struct {
		in       []string
		wantArgs []string
		wantRole string
	}{
		{[]string{"add", "-role", "admin", "bob"}, []string{"add", "bob"}, "admin"},
		{[]string{"add", "bob", "--role=operator"}, []string{"add", "bob"}, "operator"},
		{[]string{"list"}, []string{"list"}, "viewer"},
		{[]string{"del", "--", "-role"}, []string{"del", "-role"}, "viewer"},
	}
	for _, tt := range tests {
		args, role, err := splitRoleFlag(tt.in, "viewer")
		if err != nil || !reflect.DeepEqual(args, tt.wantArgs) || role != tt.wantRole {
			t.Errorf("%v -> %v %q %v", tt.in, args, role, err)
		}
	}
	if _, _, err := splitRoleFlag([]string{"add", "bob", "-role"}, "viewer"); err == nil {
		t.Error("dangling -role accepted")
	}
}

// scripts/add-camera.sh asks check-camera-id before adding a camera: an
// id in use, or one a removed camera had, is refused; a new one passes.
func TestCheckCameraID(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	cfg := "data_dir: " + filepath.ToSlash(dir) + "\ncameras:\n  - {id: cam1, host: 192.168.1.20, user_env: U, password_env: P}\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, filepath.Join(dir, "db", "sentinela.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := st.SyncCameraIDs(ctx, []string{"cam1", "cam2"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := st.SyncCameraIDs(ctx, []string{"cam1"}, nil); err != nil {
		t.Fatal(err)
	}
	st.Close()
	for _, tc := range []struct {
		args []string
		ok   bool
	}{
		{[]string{"cam3"}, true},
		{[]string{"cam2"}, false},      // retired
		{[]string{"cam1"}, false},      // configured
		{[]string{"cam1_sub"}, false},  // cam1's substream in go2rtc
		{[]string{"cam1_talk"}, false}, // cam1's talk stream
		{nil, false},
	} {
		if err := checkCameraID(ctx, cfgPath, tc.args); (err == nil) != tc.ok {
			t.Errorf("%v: err %v, want ok=%v", tc.args, err, tc.ok)
		}
	}
	// No database to ask: refused, never assumed free.
	if err := checkCameraID(ctx, filepath.Join(dir, "missing.yaml"), []string{"cam3"}); err == nil {
		t.Error("an unreadable config must fail closed")
	}
}

// A camera-id sync that failed at start is tried again until it works.
func TestRetryUntil(t *testing.T) {
	calls, fails := 0, 0
	retryUntil(context.Background(), time.Millisecond, 4*time.Millisecond, func(context.Context) error {
		calls++
		if calls < 3 {
			return errors.New("database busy")
		}
		return nil
	}, func(error) { fails++ })
	if calls != 3 || fails != 2 {
		t.Fatalf("calls %d, failures %d; want 3, 2", calls, fails)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	retryUntil(ctx, time.Hour, time.Hour, func(context.Context) error { t.Fatal("tried after the end"); return nil }, func(error) {})
}

func TestCredentialNames(t *testing.T) {
	tests := []struct {
		name, config, want string
	}{
		{"no cameras", "cameras: []\n", ""},
		// An XMEye camera on a custom path without {user}/{password} and
		// with talk off: go2rtc's URLs name no credential, the camera does.
		{"names outside the stream URLs", `cameras:
  - {id: gate, host: 192.168.1.20, rtsp_main: /live/main, features: {talk: false}, user_env: GATE_USER, password_env: GATE_PASS}
  - {id: yard, host: 192.168.1.21, vendor: generic, rtsp_main: /stream1, user_env: CAMS_USER, password_env: CAMS_PASS}
  - {id: shed, host: 192.168.1.22, vendor: generic, rtsp_main: /stream1, user_env: CAMS_USER, password_env: CAMS_PASS}
`, "CAMS_PASS\nCAMS_USER\nGATE_PASS\nGATE_USER\n"},
	}
	for _, tt := range tests {
		p := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(p, []byte(tt.config), 0o600); err != nil {
			t.Fatal(err)
		}
		var out strings.Builder
		if err := credentialNames(&out, p); err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if out.String() != tt.want {
			t.Errorf("%s: printed %q, want %q", tt.name, out.String(), tt.want)
		}
		// What apply-config.sh used to read: go2rtc's streams leave GATE_* out.
		cfg, err := config.LoadLayout(p)
		if err != nil {
			t.Fatal(err)
		}
		var streams strings.Builder
		if err := cfg.WriteGo2RTC(&streams); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(streams.String(), "GATE_") {
			t.Errorf("%s: the fixture's gate camera now appears in go2rtc's URLs", tt.name)
		}
	}
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte("cameras: [{id: \"Bad id\", host: 192.168.1.20}]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := credentialNames(io.Discard, p); err == nil {
		t.Error("an invalid config printed names")
	}
}

func TestProxyPort(t *testing.T) {
	tests := []struct {
		name, config, want string
	}{
		{"off", "cameras: []\n", ""},
		{"default listen", "remote:\n  proxy: {url: https://cam.example.com, kind: cloudflare}\n", "7444\n"},
		{"chosen port", "remote:\n  proxy: {url: https://cam.example.com, listen: \"127.0.0.1:9444\", kind: cloudflare}\n", "9444\n"},
		{"ipv6 loopback", "remote:\n  proxy: {url: https://cam.example.com, listen: \"[::1]:9445\", kind: other}\n", "9445\n"},
	}
	for _, tt := range tests {
		p := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(p, []byte(tt.config), 0o600); err != nil {
			t.Fatal(err)
		}
		var out strings.Builder
		if err := proxyPort(&out, p); err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if out.String() != tt.want {
			t.Errorf("%s: printed %q, want %q", tt.name, out.String(), tt.want)
		}
	}
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte("remote:\n  proxy: {url: https://cam.example.com, listen: \"0.0.0.0:7444\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := proxyPort(io.Discard, p); err == nil {
		t.Error("a proxy listener off loopback was accepted")
	}
}

// The index and the private database are pruned separately: one prefix
// must never count (or delete) the other's copies.
func TestPruneBackupsKeepsEachFile(t *testing.T) {
	dir := t.TempDir()
	for d := 1; d <= 9; d++ {
		for _, p := range []string{"sentinela-", "sentinela-private-"} {
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%s2026010%d.db", p, d)), nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	pruneBackups(dir, "sentinela-", 7)
	pruneBackups(dir, "sentinela-private-", 7)
	for _, tc := range []struct {
		pattern string
		want    int
		oldest  string
	}{
		{"sentinela-[0-9]*.db", 7, "sentinela-20260103.db"},
		{"sentinela-private-[0-9]*.db", 7, "sentinela-private-20260103.db"},
	} {
		m, _ := filepath.Glob(filepath.Join(dir, tc.pattern))
		if len(m) != tc.want || filepath.Base(m[0]) != tc.oldest {
			t.Errorf("%s: %d left, oldest %v", tc.pattern, len(m), m)
		}
	}
}
