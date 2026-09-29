package api

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LUKER2DW/sentinela/internal/auth"
)

// newPublicServer serves srv on a second listener in a public zone. The
// cookie jar ignores ports, so a client's cookies from the private test
// server reach this one too — like a stolen home cookie would.
func newPublicServer(t *testing.T, srv *Server, zone string) *httptest.Server {
	ts := httptest.NewServer(srv.PublicHandler(zone))
	t.Cleanup(ts.Close)
	return ts
}

func TestPublicZoneTwoStepLogin(t *testing.T) {
	priv, srv := newTestServer(t)
	pub := newPublicServer(t, srv, ZoneFunnel)

	home := newClient(t, priv.URL)
	if code, _, body := home.do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`); code != 200 {
		t.Fatalf("setup = %d %s", code, body)
	}

	out := newClient(t, pub.URL)
	if code, m, _ := out.do("GET", "/api/setup", ""); code != 200 || m["zone"] != ZoneFunnel || m["mfa"] != true {
		t.Fatalf("public setup status %d %v", code, m)
	}
	// Closed door: nothing but login without a 2FA session.
	for _, p := range []string{"/api/cameras", "/api/me", "/healthz", "/stream/ws?ticket=x", "/api/nope"} {
		if code, _, _ := out.do("GET", p, ""); code != 404 {
			t.Errorf("GET %s from outside = %d, want 404", p, code)
		}
	}
	if code, _, _ := out.do("POST", "/api/setup", `{"username":"x","password":"long enough pw"}`); code != 404 {
		t.Errorf("setup from outside = %d", code)
	}
	// A session made at home does not open the public door.
	pubAsHome := &client{t: t, base: pub.URL, hc: home.hc}
	if code, _, _ := pubAsHome.do("GET", "/api/cameras", ""); code != 404 {
		t.Fatalf("home session honoured outside: %d", code)
	}
	// No 2FA enrolled: refused even with the right password.
	if code, _, _ := out.do("POST", "/api/login", `{"username":"admin","password":"long enough pw"}`); code != 403 {
		t.Fatalf("login without 2FA enrolled = %d", code)
	}

	// Enroll at home.
	if code, _, _ := home.do("POST", "/api/me/totp/setup", `{"password":"wrong password"}`); code != 403 {
		t.Fatalf("totp setup with wrong password = %d", code)
	}
	code, m, body := home.do("POST", "/api/me/totp/setup", `{"password":"long enough pw"}`)
	secret, _ := m["secret"].(string)
	if code != 200 || secret == "" || !strings.HasPrefix(m["qr_svg"].(string), "<svg") {
		t.Fatalf("totp setup = %d %s", code, body)
	}
	now := auth.TOTPStep(time.Now())
	c0, _ := auth.TOTPCode(secret, now)
	if code, _, body := home.do("POST", "/api/me/totp/enable", `{"code":"`+c0+`"}`); code != 200 {
		t.Fatalf("totp enable = %d %s", code, body)
	}

	if code, m, _ := out.do("POST", "/api/login", `{"username":"admin","password":"long enough pw"}`); code != 401 || m["mfa"] != true {
		t.Fatalf("login without code = %d %v", code, m)
	}
	if code, _, _ := out.do("POST", "/api/login", `{"username":"admin","password":"long enough pw","code":"`+c0+`"}`); code != 401 {
		t.Fatalf("reused enrollment code accepted: %d", code)
	}
	c1, _ := auth.TOTPCode(secret, now+1)
	if code, m, body := out.do("POST", "/api/login", `{"username":"admin","password":"long enough pw","code":"`+c1+`"}`); code != 200 || m["zone"] != ZoneFunnel {
		t.Fatalf("login with code = %d %s", code, body)
	}
	if code, _, _ := out.do("GET", "/api/cameras", ""); code != 200 {
		t.Fatalf("cameras after 2FA login = %d", code)
	}
	// Granting access stays a home-only action, even with a 2FA session.
	if code, _, _ := out.do("POST", "/api/me/totp/disable", `{"password":"long enough pw"}`); code != 403 {
		t.Fatalf("totp disable from outside = %d", code)
	}
	if code, _, _ := out.do("POST", "/api/users", `{"username":"evil","password":"long enough pw","role":"admin"}`); code != 403 {
		t.Fatalf("user create from outside = %d", code)
	}
	// The same code cannot open a second session.
	again := newClient(t, pub.URL)
	if code, _, _ := again.do("POST", "/api/login", `{"username":"admin","password":"long enough pw","code":"`+c1+`"}`); code != 401 {
		t.Fatalf("replayed code accepted: %d", code)
	}

	// Turning 2FA off at home closes the outside session.
	if code, _, body := home.do("POST", "/api/me/totp/disable", `{"password":"long enough pw"}`); code != 200 {
		t.Fatalf("totp disable = %d %s", code, body)
	}
	if code, _, _ := out.do("GET", "/api/cameras", ""); code != 404 {
		t.Fatalf("2FA session survived disable: %d", code)
	}
}

func TestPublicAccountLockout(t *testing.T) {
	_, srv := newTestServer(t)
	pub := newPublicServer(t, srv, ZonePublic)
	srv.loginRL = auth.NewLimiter(1000, time.Minute) // isolate the per-account limit
	c := newClient(t, pub.URL)
	for i := 0; i < 10; i++ {
		c.do("POST", "/api/login", `{"username":"admin","password":"wrong password"}`)
	}
	if code, _, _ := c.do("POST", "/api/login", `{"username":"Admin","password":"wrong password"}`); code != 429 {
		t.Fatalf("11th attempt = %d, want 429", code)
	}
}
