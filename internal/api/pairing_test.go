package api

import (
	"strings"
	"testing"

	"github.com/LUKER2DW/sentinela/internal/config"
)

func TestPublicOrigin(t *testing.T) {
	for _, c := range []struct {
		p    config.PublicTLS
		want string
	}{
		{config.PublicTLS{}, ""},
		{config.PublicTLS{Domain: "casa.example.com", Listen: ":443"}, "https://casa.example.com"},
		{config.PublicTLS{Domain: "203.0.113.7", Listen: ":7443"}, "https://203.0.113.7:7443"},
		{config.PublicTLS{Domain: "203.0.113.7", Listen: ":7443", URL: "https://203.0.113.7:18443/"}, "https://203.0.113.7:18443"},
	} {
		if got := publicOrigin(c.p); got != c.want {
			t.Errorf("%+v: %q, want %q", c.p, got, c.want)
		}
	}
}

func TestAppPairing(t *testing.T) {
	ts, srv := newTestServer(t)
	srv.Cfg.Remote.TailnetURL = "https://sentinela.example.ts.net/"
	srv.Cfg.Remote.Public = config.PublicTLS{Domain: "203.0.113.7", Listen: ":7443", URL: "https://203.0.113.7:18443", SelfSigned: true}
	srv.SetPublicCertPin(strings.Repeat("AB", 32))
	c := newClient(t, ts.URL)
	if code, _, _ := c.do("GET", "/api/app/pairing", ""); code != 401 {
		t.Fatalf("anonymous: %d", code)
	}
	c.do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`)
	code, m, body := c.do("GET", "/api/app/pairing", "")
	if code != 200 {
		t.Fatalf("pairing: %d %s", code, body)
	}
	opts, _ := m["options"].([]any)
	if len(opts) != 2 {
		t.Fatalf("options: %s", body)
	}
	tail, pub := opts[0].(map[string]any), opts[1].(map[string]any)
	if tail["link"] != "sentinela://pair?url=https%3A%2F%2Fsentinela.example.ts.net" || tail["pin"] != nil {
		t.Errorf("tailnet option: %v", tail)
	}
	want := "sentinela://pair?url=https%3A%2F%2F203.0.113.7%3A18443&pin=" + strings.Repeat("ab", 32)
	if pub["link"] != want || !strings.HasPrefix(pub["qr_svg"].(string), "<svg") {
		t.Errorf("public option: %v", pub["link"])
	}
}
