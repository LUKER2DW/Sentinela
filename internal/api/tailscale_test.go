package api

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestTailscaleCheck(t *testing.T) {
	up := &tailscaleStatus{State: "Running", Serve: true, DNSName: "sentinela.example.ts.net"}
	for _, tc := range []struct {
		name, configured string
		st               *tailscaleStatus
		want             string
	}{
		{"no status", "https://sentinela.example.ts.net", nil, "unknown"},
		{"nothing configured", "", up, "unknown"},
		{"same", "https://sentinela.example.ts.net/", up, "ok"},
		{"other host", "https://old.example.ts.net", up, "mismatch"},
		{"not published", "https://sentinela.example.ts.net", &tailscaleStatus{State: "Running", DNSName: "sentinela.example.ts.net"}, "unknown"},
		{"logged out", "https://sentinela.example.ts.net", &tailscaleStatus{State: "NeedsLogin", Serve: true, DNSName: "sentinela.example.ts.net"}, "unknown"},
	} {
		if got := tailscaleCheck(tc.configured, tc.st); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestTailscaleAPI(t *testing.T) {
	ts, srv := newTestServer(t)
	host := t.TempDir()
	srv.Cfg.UpdateStatusFile = filepath.Join(host, "update.json")
	c := newClient(t, ts.URL)
	c.do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`)

	// Without the host agent the page explains the manual way.
	if code, m, body := c.do("GET", "/api/tailscale", ""); code != 200 || m["host"] != false || m["check"] != "unknown" {
		t.Fatalf("get: %d %s", code, body)
	}
	if code, _, _ := c.do("POST", "/api/tailscale", `{"action":"status"}`); code != 409 {
		t.Fatalf("no agent: %d", code)
	}
	if err := os.WriteFile(filepath.Join(host, "tailscale-units"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	status := `{"state":"Running","serve":true,"dns_name":"sentinela.example.ts.net","enable_url":"https://evil.example/x"}`
	if err := os.WriteFile(filepath.Join(host, "tailscale.json"), []byte(status), 0o644); err != nil {
		t.Fatal(err)
	}
	code, m, body := c.do("GET", "/api/tailscale", "")
	if code != 200 || m["suggested"] != "https://sentinela.example.ts.net" || strings.Contains(body, "evil.example") {
		t.Fatalf("status: %d %s", code, body)
	}

	for _, bad := range []string{`{"action":"connect","auth_key":"hunter2"}`, `{"action":"connect"}`, `{"action":"rm -rf"}`} {
		if code, _, body := c.do("POST", "/api/tailscale", bad); code != 400 {
			t.Fatalf("%s: %d %s", bad, code, body)
		}
	}
	// A made-up key in the auth key format (no real key exists in this
	// repository); built here so secret scanners do not flag a literal.
	key := "tskey-auth-" + strings.Repeat("example0", 3)
	if code, _, body := c.do("POST", "/api/tailscale", `{"action":"connect","auth_key":" `+key+` "}`); code != 202 {
		t.Fatalf("connect: %d %s", code, body)
	}
	dir := filepath.Join(srv.Cfg.DataDir, "tailscale")
	b, err := os.ReadFile(filepath.Join(dir, "authkey"))
	if err != nil || strings.TrimSpace(string(b)) != key {
		t.Fatalf("auth key file: %v %q", err, b)
	}
	if fi, _ := os.Stat(filepath.Join(dir, "authkey")); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Fatalf("auth key mode %v", fi.Mode().Perm())
	}
	if req, _ := os.ReadFile(filepath.Join(dir, "request")); !strings.HasPrefix(string(req), "connect ") {
		t.Fatalf("request %q", req)
	}
	if _, m, body := c.do("GET", "/api/tailscale", ""); m["pending"] != true || strings.Contains(body, key) {
		t.Fatalf("pending / key leak: %s", body)
	}
}
