package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/LUKER2DW/sentinela/internal/auth"
)

func TestLimiterKey(t *testing.T) {
	cases := []struct{ in, want string }{
		{"192.168.1.20", "192.168.1.20"},
		{"203.0.113.7", "203.0.113.7"},
		{"::ffff:192.168.1.20", "192.168.1.20"},
		{"2001:db8:1:2:aaaa:bbbb:cccc:dddd", "2001:db8:1:2::/64"},
		{"2001:db8:1:2::1", "2001:db8:1:2::/64"},
		{"2001:db8:1:3::1", "2001:db8:1:3::/64"},
		{"fe80::1%eth0", "fe80::/64"},
		{"not an address", "not an address"},
	}
	for _, c := range cases {
		if got := limiterKey(c.in); got != c.want {
			t.Errorf("limiterKey(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// The exact address stays available for logs and audit.
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "[2001:db8:1:2::9]:5000"
	if got := clientIP(r); got != "2001:db8:1:2::9" {
		t.Errorf("clientIP = %q, want the exact address", got)
	}
}

func TestAuditField(t *testing.T) {
	long := strings.Repeat("a", 5000)
	cases := []struct {
		name, in string
	}{
		{"short", "ana"},
		{"exact", strings.Repeat("b", maxAuditField)},
		{"long ascii", long},
		{"padded", strings.Repeat(" ", 3000) + "x"},
		{"multibyte straddling the cut", strings.Repeat("a", maxAuditField-1) + "é" + long},
		{"all multibyte", strings.Repeat("日", 500)},
		{"invalid utf-8", strings.Repeat("\xff", 500)},
	}
	for _, c := range cases {
		got := auditField(c.in)
		if len(got) > maxAuditField {
			t.Errorf("%s: %d bytes, want <= %d", c.name, len(got), maxAuditField)
		}
		if !utf8.ValidString(got) {
			t.Errorf("%s: result is not valid UTF-8: %q", c.name, got)
		}
	}
	if auditField("ana") != "ana" || auditField(strings.Repeat("b", maxAuditField)) != strings.Repeat("b", maxAuditField) {
		t.Error("values within the cap must be unchanged")
	}
}

func TestBusyAnswer(t *testing.T) {
	cases := []struct {
		err       error
		retry     string
		notRunErr bool
	}{
		{auth.ErrBusy, "5", true},
		{context.Canceled, "", true},
		{context.DeadlineExceeded, "", true},
		{errors.New("password too weak"), "", false},
	}
	for _, c := range cases {
		if notRun(c.err) != c.notRunErr {
			t.Errorf("notRun(%v) = %v", c.err, !c.notRunErr)
		}
		if !c.notRunErr {
			continue
		}
		w := httptest.NewRecorder()
		busy(w, c.err)
		if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") != c.retry {
			t.Errorf("busy(%v): %d retry=%q", c.err, w.Code, w.Header().Get("Retry-After"))
		}
		if strings.Contains(w.Body.String(), "argon") {
			t.Errorf("busy(%v) leaks internals: %s", c.err, w.Body.String())
		}
	}
}

// The failed-login audit row stores a bounded name, whichever path wrote it.
func TestFailedAuditNamesAreCapped(t *testing.T) {
	ts, srv := newTestServer(t)
	c := newClient(t, ts.URL)
	if code, _, body := c.do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`); code != 200 {
		t.Fatalf("setup = %d %s", code, body)
	}
	// Unknown user: the untrimmed name is whitespace padded to ~30 KiB.
	padded := strings.Repeat(" ", 30000) + "ghost"
	if code, _, _ := c.do("POST", "/api/login", `{"username":"`+padded+`","password":"wrong password"}`); code != 401 {
		t.Fatalf("unknown user login = %d", code)
	}
	// Failed invite: login far longer than any username.
	if code, _, _ := c.do("POST", "/api/invite/accept",
		`{"login":"`+strings.Repeat("x", 20000)+`","code":"AAAAA-AAAAA","password":"long enough pw"}`); code != 401 {
		t.Fatalf("bad invite = %d", code)
	}
	rows, err := srv.Store.ListAudit(context.Background(), 50)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, r := range rows {
		if r.Action == "login" || r.Action == "invite.accept" {
			seen[r.Action] = true
			if len(r.Username) > maxAuditField {
				t.Errorf("%s audit username is %d bytes, want <= %d", r.Action, len(r.Username), maxAuditField)
			}
		}
	}
	if !seen["login"] || !seen["invite.accept"] {
		t.Fatalf("expected audit rows for both failures, got %v", seen)
	}
}

// Setup on an install that already has a user must not spend an argon2id hash.
func TestSetupDoesNotHashWhenAlreadyDone(t *testing.T) {
	ts, _ := newTestServer(t)
	first := newClient(t, ts.URL)
	if code, _, body := first.do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`); code != 200 {
		t.Fatalf("setup = %d %s", code, body)
	}
	before := auth.ArgonRuns()
	other := newClient(t, ts.URL)
	for i := 0; i < 3; i++ {
		if code, _, _ := other.do("POST", "/api/setup", `{"username":"evil","password":"long enough pw"}`); code != http.StatusConflict {
			t.Fatalf("second setup = %d, want 409", code)
		}
	}
	if n := auth.ArgonRuns() - before; n != 0 {
		t.Fatalf("setup after completion ran %d argon2id computations, want 0", n)
	}
}

// Addresses of one IPv6 /64 share the login limits; another /64 does not.
func TestLoginLimitsAggregateIPv6(t *testing.T) {
	_, srv := newTestServer(t)
	// A name over 254 bytes is refused before any hash or database work but
	// still goes through the per-address limits.
	body := `{"username":"` + strings.Repeat("u", 300) + `","password":"x"}`
	post := func(remote string) int {
		r := httptest.NewRequest("POST", "/api/login", strings.NewReader(body))
		r.RemoteAddr = remote
		w := httptest.NewRecorder()
		srv.login(w, r)
		return w.Code
	}
	for i := 0; i < 5; i++ {
		addr := "[2001:db8:1:2::" + string(rune('1'+i)) + "]:4000"
		if code := post(addr); code != http.StatusUnauthorized {
			t.Fatalf("attempt %d from %s = %d, want 401", i+1, addr, code)
		}
	}
	if code := post("[2001:db8:1:2:ffff::7]:4000"); code != http.StatusTooManyRequests {
		t.Fatalf("6th address of the same /64 = %d, want 429", code)
	}
	if code := post("[2001:db8:1:9::1]:4000"); code != http.StatusUnauthorized {
		t.Fatalf("another /64 = %d, want 401", code)
	}
	// IPv4 stays per exact address.
	if code := post("192.0.2.10:4000"); code != http.StatusUnauthorized {
		t.Fatalf("IPv4 = %d, want 401", code)
	}
	if code := post("192.0.2.11:4000"); code != http.StatusUnauthorized {
		t.Fatalf("other IPv4 = %d, want 401", code)
	}
}
