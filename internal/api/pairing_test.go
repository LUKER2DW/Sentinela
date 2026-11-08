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
	srv.lanIP = func() string { return "192.168.1.10" }
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
	if len(opts) != 3 {
		t.Fatalf("options: %s", body)
	}
	// The home network: the TLS listener's own port on the LAN address, same pin.
	lan := opts[2].(map[string]any)
	if lan["kind"] != "lan" || lan["url"] != "https://192.168.1.10:7443" || lan["pin"] != strings.Repeat("ab", 32) {
		t.Errorf("lan option: %v", lan)
	}
	tail, pub := opts[0].(map[string]any), opts[1].(map[string]any)
	if tail["link"] != "sentinela://pair?url=https%3A%2F%2Fsentinela.example.ts.net" || tail["pin"] != nil {
		t.Errorf("tailnet option: %v", tail)
	}
	want := "sentinela://pair?url=https%3A%2F%2F203.0.113.7%3A18443&pin=" + strings.Repeat("ab", 32)
	if pub["link"] != want || !strings.HasPrefix(pub["qr_svg"].(string), "<svg") {
		t.Errorf("public option: %v", pub["link"])
	}

	// A publicly trusted certificate is checked by name: no LAN address then.
	srv.Cfg.Remote.Public.SelfSigned = false
	if _, m, body := c.do("GET", "/api/app/pairing", ""); len(m["options"].([]any)) != 2 || strings.Contains(body, "192.168.1.10") {
		t.Errorf("LAN offered without a pinned certificate: %s", body)
	}
}

func TestHomeCode(t *testing.T) {
	pin := "c1d9719aca1eb59f2b8febf6b764fe1aa4028905fdc1f132a22b6504562c6ea9"
	if got, want := homeCode(pin), "C1D9-719A-CA1E-B59F-2B8F"; got != want {
		t.Fatalf("homeCode = %q, want %q", got, want)
	}
	for _, bad := range []string{"", "abc", pin[:63], pin + "0", strings.Repeat("g", 64)} {
		if got := homeCode(bad); got != "" {
			t.Errorf("homeCode(%q) = %q, want empty", bad, got)
		}
	}
}
