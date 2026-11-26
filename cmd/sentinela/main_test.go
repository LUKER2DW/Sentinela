package main

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/LUKER2DW/sentinela/internal/config"
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
