package api

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/LUKER2DW/sentinela/internal/auth"
)

func TestInviteFirstAccessAndEmailLogin(t *testing.T) {
	priv, _ := newTestServer(t)
	admin := newClient(t, priv.URL)
	admin.do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`)

	// Authorize by e-mail only: the username comes from it.
	code, m, body := admin.do("POST", "/api/users", `{"email":"Bia.Souza@example.com","role":"viewer"}`)
	invite, _ := m["invite_code"].(string)
	if code != 201 || m["username"] != "bia.souza" || m["pending"] != true || len(invite) != 11 {
		t.Fatalf("invite = %d %s", code, body)
	}
	if code, _, _ := admin.do("POST", "/api/users", `{"email":"bia.souza@EXAMPLE.com","role":"viewer"}`); code != 409 {
		t.Fatalf("duplicate e-mail = %d", code)
	}

	bia := newClient(t, priv.URL)
	// No password yet: login fails like a wrong password.
	if code, _, _ := bia.do("POST", "/api/login", `{"username":"bia.souza","password":""}`); code != 401 {
		t.Fatalf("pending login = %d", code)
	}
	if code, _, _ := bia.do("POST", "/api/invite/accept", `{"login":"bia.souza","code":"AAAAA-AAAAA","password":"casa2026!x"}`); code != 401 {
		t.Fatalf("wrong code = %d", code)
	}
	if code, _, _ := bia.do("POST", "/api/invite/accept", `{"login":"bia.souza","code":"`+invite+`","password":"short"}`); code != 400 {
		t.Fatalf("weak password = %d", code)
	}
	// Typed in lower case with spaces: still the same code.
	typed := strings.ToLower(strings.ReplaceAll(invite, "-", " "))
	code, m, body = bia.do("POST", "/api/invite/accept", `{"login":"BIA.SOUZA@example.com","code":"`+typed+`","password":"casa2026!x"}`)
	if code != 200 || m["user"] == nil {
		t.Fatalf("accept = %d %s", code, body)
	}
	if code, _, _ := bia.do("GET", "/api/me", ""); code != 200 {
		t.Fatalf("session after accept = %d", code)
	}
	// Used once.
	if code, _, _ := newClient(t, priv.URL).do("POST", "/api/invite/accept", `{"login":"bia.souza","code":"`+invite+`","password":"casa2026!y"}`); code != 401 {
		t.Fatalf("second accept = %d", code)
	}
	// Sign in with the e-mail.
	if code, _, _ := newClient(t, priv.URL).do("POST", "/api/login", `{"username":"bia.souza@example.com","password":"casa2026!x"}`); code != 200 {
		t.Fatalf("e-mail login = %d", code)
	}

	// A new invite (lost password) drops the password and the sessions.
	id := int64(m["user"].(map[string]any)["id"].(float64))
	code, m, _ = admin.do("POST", "/api/users/"+strconv.FormatInt(id, 10)+"/invite", "")
	if code != 200 || m["pending"] != true || m["invite_code"] == "" {
		t.Fatalf("reinvite = %d %v", code, m)
	}
	if code, _, _ := bia.do("GET", "/api/me", ""); code != 401 {
		t.Fatalf("session after reinvite = %d", code)
	}
}

func TestInviteLink(t *testing.T) {
	priv, srv := newTestServer(t)
	srv.Cfg.Remote.Public.URL = "https://203.0.113.7:18443/"
	admin := newClient(t, priv.URL)
	admin.do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`)
	_, m, _ := admin.do("POST", "/api/users", `{"email":"ana@example.com","role":"viewer"}`)
	want := "https://203.0.113.7:18443/#/convite?c=" + m["invite_code"].(string) + "&u=ana%40example.com"
	if m["invite_url"] != want {
		t.Fatalf("invite_url = %v, want %s", m["invite_url"], want)
	}
}

// Two-step login is optional (ADR-0019 amendment): an invite redeemed from
// outside signs in with the new password alone.
func TestInviteFromOutsideWithoutTwoStep(t *testing.T) {
	priv, srv := newTestServer(t)
	admin := newClient(t, priv.URL)
	admin.do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`)
	_, m, _ := admin.do("POST", "/api/users", `{"username":"joao","role":"viewer"}`)
	invite := m["invite_code"].(string)

	pub := newPublicServer(t, srv, ZonePublic)
	out := newClient(t, pub.URL)
	req := `{"login":"joao","code":"` + invite + `","password":"casa2026!x"}`
	if code, m, body := out.do("POST", "/api/invite/accept", req); code != 200 || m["totp_required"] != nil {
		t.Fatalf("accept = %d %s", code, body)
	}
	if code, m, _ := out.do("GET", "/api/me", ""); code != 200 || m["user"].(map[string]any)["totp"] != false {
		t.Fatalf("me after accept = %d %v", code, m)
	}
	if code, _, _ := out.do("POST", "/api/invite/accept", req); code != 401 {
		t.Fatalf("invite reused = %d", code)
	}
}

func TestFailedLoginsBlockAddress(t *testing.T) {
	priv, srv := newTestServer(t)
	srv.loginRL = auth.NewLimiter(1000, time.Minute) // isolate the hourly limit
	c := newClient(t, priv.URL)
	c.do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`)
	other := newClient(t, priv.URL)
	for i := 0; i < 20; i++ {
		other.do("POST", "/api/login", `{"username":"nobody`+strconv.FormatInt(int64(i), 10)+`","password":"x"}`)
	}
	if code, _, _ := other.do("POST", "/api/login", `{"username":"admin","password":"long enough pw"}`); code != 429 {
		t.Fatalf("login after 20 failures = %d, want 429", code)
	}
}

func TestRememberMe(t *testing.T) {
	priv, _ := newTestServer(t)
	newClient(t, priv.URL).do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`)
	for _, tc := range []struct {
		body       string
		persistent bool
	}{
		{`{"username":"admin","password":"long enough pw"}`, true},
		{`{"username":"admin","password":"long enough pw","remember":false}`, false},
	} {
		c := newClient(t, priv.URL)
		resp, err := c.hc.Post(priv.URL+"/api/login", "application/json", strings.NewReader(tc.body))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		ck := resp.Cookies()
		if len(ck) != 1 || ck[0].Expires.IsZero() == tc.persistent {
			t.Fatalf("%s: cookie %+v", tc.body, ck)
		}
	}
}

// The Android app is always remembered, for a year from last use, even
// when the page asked not to remember.
func TestAppSessionLastsAYear(t *testing.T) {
	priv, _ := newTestServer(t)
	newClient(t, priv.URL).do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`)
	req, _ := http.NewRequest("POST", priv.URL+"/api/login",
		strings.NewReader(`{"username":"admin","password":"long enough pw","remember":false}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Linux; Android 14; wv) Chrome/129 Mobile Safari/537.36 SentinelaApp/1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	ck := resp.Cookies()
	year := time.Now().Add(appSessionTTL)
	if resp.StatusCode != 200 || len(ck) != 1 || ck[0].Expires.Before(year.Add(-time.Hour)) || ck[0].Expires.After(year.Add(time.Hour)) {
		t.Fatalf("app login = %d, cookie %+v", resp.StatusCode, ck)
	}
}

func TestSessionTTL(t *testing.T) {
	if sessionTTL(false, false) != shortSessionTTL || sessionTTL(true, false) != SessionTTL || sessionTTL(false, true) != appSessionTTL {
		t.Fatal("sessionTTL tiers")
	}
}
