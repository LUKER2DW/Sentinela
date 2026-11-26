package api

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LUKER2DW/sentinela/internal/auth"
)

func TestOutsideLoginNoticeRequiresOptIn(t *testing.T) {
	_, srv := newTestServer(t)
	ctx := context.Background()
	id, err := srv.Store.CreateFirstUser(ctx, "admin", "hash")
	if err != nil {
		t.Fatal(err)
	}
	user, err := srv.Store.UserByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	stream := srv.push.subscribe(id)
	defer srv.push.unsubscribe(stream)
	srv.alertOutsideLogin(user, "192.0.2.1", ZonePublic)
	select {
	case <-stream:
		t.Fatal("unselected login notice was sent")
	default:
	}
	if err := srv.Store.SetUserNotify(ctx, id, `{"follow_mode":true,"types":["login"]}`); err != nil {
		t.Fatal(err)
	}
	user, err = srv.Store.UserByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	srv.alertOutsideLogin(user, "192.0.2.1", ZonePublic)
	select {
	case <-stream:
	default:
		t.Fatal("selected login notice was not sent")
	}
}

// A code lock means someone else may hold the password, so its alert does
// not wait for the login-notice opt-in (off by default): it reaches the
// account and every active admin, and no one else.
func TestCodeLockAlertNeedsNoOptIn(t *testing.T) {
	_, srv := newTestServer(t)
	ctx := context.Background()
	admin, err := srv.Store.CreateFirstUser(ctx, "admin", "hash")
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]int64{"admin": admin}
	for _, u := range []struct{ name, role string }{
		{"ana", auth.RoleViewer}, {"other", auth.RoleViewer}, {"old-admin", auth.RoleAdmin},
	} {
		if ids[u.name], err = srv.Store.CreateUser(ctx, u.name, "hash", u.role); err != nil {
			t.Fatal(err)
		}
	}
	if err := srv.Store.SetUserDisabled(ctx, ids["old-admin"], true); err != nil {
		t.Fatal(err)
	}
	streams := map[string]chan []byte{}
	for name, id := range ids {
		streams[name] = srv.push.subscribe(id)
		defer srv.push.unsubscribe(streams[name])
	}
	ana, err := srv.Store.UserByID(ctx, ids["ana"])
	if err != nil {
		t.Fatal(err)
	}
	srv.alertCodeLocked(ana, "203.0.113.5", ZoneProxy, auth.CodeLockAfter)
	for name, want := range map[string]bool{"ana": true, "admin": true, "other": false, "old-admin": false} {
		select {
		case msg := <-streams[name]:
			if !want {
				t.Errorf("%s was alerted: %s", name, msg)
			} else if !strings.Contains(string(msg), "sentinela-code-locked") {
				t.Errorf("%s got %s", name, msg)
			}
		default:
			if want {
				t.Errorf("%s was not alerted", name)
			}
		}
	}
}

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
	if code, m, _ := out.do("GET", "/api/setup", ""); code != 200 || m["zone"] != ZoneFunnel || m["needs_setup"] != false {
		t.Fatalf("public setup status %d %v", code, m)
	}
	// Closed door: nothing but login without a session.
	for _, p := range []string{"/api/cameras", "/api/me", "/healthz", "/stream/ws?ticket=x", "/api/nope"} {
		if code, _, _ := out.do("GET", p, ""); code != 404 {
			t.Errorf("GET %s from outside = %d, want 404", p, code)
		}
	}
	if code, _, _ := out.do("POST", "/api/setup", `{"username":"x","password":"long enough pw"}`); code != 404 {
		t.Errorf("setup from outside = %d", code)
	}
	// Two-step login is optional: without it the password alone signs in.
	noCode := newClient(t, pub.URL)
	if code, _, body := noCode.do("POST", "/api/login", `{"username":"admin","password":"long enough pw"}`); code != 200 {
		t.Fatalf("login without 2FA enrolled = %d %s", code, body)
	}
	if code, _, _ := noCode.do("GET", "/api/cameras", ""); code != 200 {
		t.Fatalf("cameras after password login = %d", code)
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

	// Once enabled, the code is required, and the session made without it
	// no longer opens the public door.
	if code, _, _ := noCode.do("GET", "/api/cameras", ""); code != 404 {
		t.Fatalf("password-only session after enabling 2FA = %d", code)
	}
	pubAsHome := &client{t: t, base: pub.URL, hc: home.hc}
	if code, _, _ := pubAsHome.do("GET", "/api/cameras", ""); code != 404 {
		t.Fatalf("home session without code honoured outside: %d", code)
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
	srv.pubLim.attempts = auth.NewLimiter(1000, time.Minute) // isolate the per-account limit
	srv.pubLim.attemptNets = auth.NewLimiter(1000, time.Minute)
	c := newClient(t, pub.URL)
	for i := 0; i < 10; i++ {
		c.do("POST", "/api/login", `{"username":"admin","password":"wrong password"}`)
	}
	if code, _, _ := c.do("POST", "/api/login", `{"username":"Admin","password":"wrong password"}`); code != 429 {
		t.Fatalf("11th attempt = %d, want 429", code)
	}
}

// wrongCode is a 6-digit code that matches none of the steps VerifyTOTP
// accepts now.
func wrongCode(t *testing.T, secret string) string {
	t.Helper()
	cur := auth.TOTPStep(time.Now())
	for _, c := range []string{"000000", "111111", "222222", "333333"} {
		clash := false
		for st := cur - 2; st <= cur+2; st++ {
			if good, _ := auth.TOTPCode(secret, st); good == c {
				clash = true
			}
		}
		if !clash {
			return c
		}
	}
	t.Fatal("no wrong code found")
	return ""
}

// The audit's lead: with the password, about 300 codes an hour could be
// guessed from the internet, and twice that with the e-mail alias. Wrong
// codes in a row now lock the factor for the account, whatever the name or
// address; the lock is audited, alerts the account (no notice choice
// needed) and never locks the owner out at home.
func TestWrongCodesLockSecondFactor(t *testing.T) {
	priv, srv := newTestServer(t)
	ctx := context.Background()
	hash, err := auth.HashPassword("long enough pw")
	if err != nil {
		t.Fatal(err)
	}
	id, err := srv.Store.CreateFirstUser(ctx, "ana", hash)
	if err != nil {
		t.Fatal(err)
	}
	secret := auth.NewTOTPSecret()
	if err := srv.Store.UpdateUserEmail(ctx, id, "ana@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := srv.Store.SetUserTOTP(ctx, id, secret); err != nil {
		t.Fatal(err)
	}
	stream := srv.push.subscribe(id) // no login notices chosen
	defer srv.push.unsubscribe(stream)
	pub := newPublicServer(t, srv, ZonePublic)
	for _, l := range []*loginLimits{srv.pubLim, srv.homeLim} { // isolate the code lock
		l.attempts = auth.NewLimiter(1000, time.Minute)
		l.fails = auth.NewLimiter(1000, time.Hour)
	}
	srv.pubLim.attemptNets = auth.NewLimiter(1000, time.Minute)
	srv.pubLim.failNets = auth.NewLimiter(1000, time.Hour)
	srv.pubLim.acct = auth.NewLimiter(1000, time.Hour)

	bad := wrongCode(t, secret)
	out := newClient(t, pub.URL)
	login := func(c *client, name, code string) (int, string) {
		code2, _, body := c.do("POST", "/api/login", `{"username":"`+name+`","password":"long enough pw","code":"`+code+`"}`)
		return code2, body
	}
	for i := 0; i < auth.CodeLockAfter; i++ {
		name := []string{"ana", "ANA@example.com"}[i%2]
		if code, body := login(out, name, bad); code != 401 {
			t.Fatalf("wrong code %d = %d %s", i+1, code, body)
		}
	}
	// Locked: even the right code is not checked from outside, under any name.
	cur := auth.TOTPStep(time.Now())
	good, _ := auth.TOTPCode(secret, cur)
	for _, name := range []string{"ana", "ana@example.com"} {
		if code, body := login(out, name, good); code != 429 || !strings.Contains(body, "locked") {
			t.Fatalf("right code while locked (%s) = %d %s, want 429", name, code, body)
		}
	}
	select {
	case <-stream:
	case <-time.After(5 * time.Second):
		t.Fatal("the account was not alerted")
	}
	rows, err := srv.Store.ListAudit(ctx, 50)
	if err != nil {
		t.Fatal(err)
	}
	locked := false
	for _, r := range rows {
		locked = locked || r.Action == "login.code_locked"
	}
	if !locked {
		t.Fatal("the lock was not audited")
	}
	// At home the factor still works, and a good code there unlocks outside.
	home := newClient(t, priv.URL)
	if code, body := login(home, "ana", good); code != 200 {
		t.Fatalf("home login with the right code = %d %s", code, body)
	}
	next, _ := auth.TOTPCode(secret, cur+1)
	if code, body := login(out, "ana", next); code != 200 {
		t.Fatalf("outside login after the home unlock = %d %s", code, body)
	}
	// The step used outside is not good again.
	if code, _ := login(newClient(t, pub.URL), "ana", next); code != 401 {
		t.Fatalf("reused step = %d, want 401", code)
	}
}
