package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LUKER2DW/sentinela/internal/config"
)

func TestCloudflaredAPI(t *testing.T) {
	ts, srv := newTestServer(t)
	host := t.TempDir()
	srv.Cfg.UpdateStatusFile = filepath.Join(host, "update.json")
	c := newClient(t, ts.URL)
	c.do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`)

	if code, _, _ := c.do("POST", "/api/cloudflared", `{"action":"status"}`); code != 409 {
		t.Fatalf("no agent: %d", code)
	}
	if err := os.WriteFile(filepath.Join(host, "cloudflared-units"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if code, m, body := c.do("GET", "/api/cloudflared", ""); code != 200 || m["service"] != "http://127.0.0.1:7444" {
		t.Fatalf("get: %d %s", code, body)
	}
	if code, _, body := c.do("POST", "/api/cloudflared", `{"action":"connect","token":"not a token"}`); code != 400 {
		t.Fatalf("bad token: %d %s", code, body)
	}
	// A made-up token (built here so secret scanners do not flag a literal).
	token := "eyJ" + strings.Repeat("dGVzdA", 12)
	// Cloudflare's whole install line works too: the token is taken from it.
	req, _ := json.Marshal(map[string]string{"action": "connect", "token": "sudo cloudflared service install " + token})
	if code, _, body := c.do("POST", "/api/cloudflared", string(req)); code != 202 {
		t.Fatalf("connect: %d %s", code, body)
	}
	b, err := os.ReadFile(filepath.Join(srv.Cfg.DataDir, "cloudflared", "token"))
	if err != nil || strings.TrimSpace(string(b)) != token {
		t.Fatalf("token file: %v %q", err, b)
	}
	if _, _, body := c.do("GET", "/api/cloudflared", ""); strings.Contains(body, token) {
		t.Fatal("the token leaks in the status")
	}
}

func TestWellKnownAndCheckOff(t *testing.T) {
	ts, srv := newTestServer(t)
	c := newClient(t, ts.URL)
	c.do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`)
	code, m, _ := c.do("GET", "/.well-known/sentinela", "")
	if code != 200 || m["id"] != srv.instance || len(srv.instance) != 32 {
		t.Fatalf("well-known: %d %v", code, m)
	}
	// Nothing configured: the check says so without going out.
	if code, m, body := c.do("GET", "/api/remote/check?target=proxy", ""); code != 200 || m["reason"] != "off" {
		t.Fatalf("check off: %d %s", code, body)
	}
	if code, _, _ := c.do("GET", "/api/remote/check?target=http://evil.example", ""); code != 400 {
		t.Fatalf("arbitrary target: %d", code)
	}
}

func TestApplyRemote(t *testing.T) {
	remote := map[string]any{"funnel_listen": "127.0.0.1:8081", "public": map[string]any{"domain": "old.example.com", "email": "x@example.com", "http01_listen": ":7080"}}
	err := applyRemote(remote, &SettingsRemote{
		Public: SettingsPublic{Domain: "203.0.113.7", Port: 18443, URL: "https://203.0.113.7:18443/", SelfSigned: true},
		Proxy:  SettingsProxy{URL: "https://cam.example.com", Port: 7444, Kind: "cloudflare"},
	})
	if err != nil {
		t.Fatal(err)
	}
	pub := remote["public"].(map[string]any)
	if pub["listen"] != ":18443" || pub["url"] != "https://203.0.113.7:18443" || pub["self_signed"] != true || pub["http01_listen"] != nil || pub["email"] != nil {
		t.Fatalf("public: %v", pub)
	}
	if remote["funnel_listen"] != "127.0.0.1:8081" {
		t.Fatal("unknown keys must be kept")
	}
	if px := remote["proxy"].(map[string]any); px["listen"] != "127.0.0.1:7444" || px["kind"] != "cloudflare" {
		t.Fatalf("proxy: %v", px)
	}
	// Off: both blocks go away.
	if err := applyRemote(remote, &SettingsRemote{Public: SettingsPublic{Port: 7443}, Proxy: SettingsProxy{Port: 7444}}); err != nil || remote["public"] != nil || remote["proxy"] != nil {
		t.Fatalf("off: %v %v", err, remote)
	}
	if err := applyRemote(map[string]any{}, &SettingsRemote{Public: SettingsPublic{Domain: "cam.example.com", Port: 80}}); err == nil {
		t.Fatal("a privileged port must be refused")
	}
}

func TestPublicPortsAndProxyValidation(t *testing.T) {
	c := &config.Config{}
	c.Remote.Public = config.PublicTLS{Domain: "cam.example.com", Listen: ":18443", HTTP01Listen: ":7080"}
	if got := strings.Join(c.PublicPorts(), " "); got != "18443/tcp 7080/tcp 8555/tcp 8555/udp" {
		t.Fatalf("ports %q", got)
	}
	c.Remote.Public.Domain = ""
	if len(c.PublicPorts()) != 0 {
		t.Fatal("off must open nothing")
	}
}
