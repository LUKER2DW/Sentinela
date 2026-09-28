package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LUKER2DW/sentinela/internal/config"
	"github.com/LUKER2DW/sentinela/internal/store"
)

type client struct {
	t    *testing.T
	base string
	hc   *http.Client
	csrf string
}

func newTestServer(t *testing.T) (*httptest.Server, *Server) {
	t.Helper()
	cfg, err := config.Parse([]byte(`
data_dir: `+filepath.ToSlash(t.TempDir())+`
go2rtc_api: http://127.0.0.1:1
cameras:
  - {id: cam1, name: Frente, host: 127.0.0.1, dvrip_port: 1, user_env: U, password_env: P}
`), func(k string) string { return map[string]string{"U": "admin", "P": "Sup3r@Secret"}[k] })
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(cfg, st, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() { ts.Close(); srv.Close(); st.Close() })
	return ts, srv
}

func newClient(t *testing.T, base string) *client {
	jar, _ := cookiejar.New(nil)
	return &client{t: t, base: base, hc: &http.Client{Jar: jar}}
}

func (c *client) do(method, path, body string) (int, map[string]any, string) {
	c.t.Helper()
	req, _ := http.NewRequest(method, c.base+path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.csrf != "" {
		req.Header.Set("X-CSRF-Token", c.csrf)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	if tok, ok := m["csrf"].(string); ok {
		c.csrf = tok
	}
	return resp.StatusCode, m, string(raw)
}

func TestSetupLoginRolesCSRF(t *testing.T) {
	ts, _ := newTestServer(t)
	admin := newClient(t, ts.URL)

	if code, m, _ := admin.do("GET", "/api/setup", ""); code != 200 || m["needs_setup"] != true {
		t.Fatalf("setup status %d %v", code, m)
	}
	if code, _, _ := admin.do("GET", "/api/cameras", ""); code != 401 {
		t.Fatalf("unauthenticated cameras = %d", code)
	}
	if code, _, body := admin.do("POST", "/api/setup", `{"username":"admin","password":"short"}`); code != 400 {
		t.Fatalf("weak password accepted: %d %s", code, body)
	}
	if code, _, body := admin.do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`); code != 200 {
		t.Fatalf("setup = %d %s", code, body)
	}
	// Setup is one-shot.
	other := newClient(t, ts.URL)
	if code, _, _ := other.do("POST", "/api/setup", `{"username":"evil","password":"long enough pw"}`); code != 409 {
		t.Fatalf("second setup = %d", code)
	}

	// CSRF: unsafe method without token is refused.
	saved := admin.csrf
	admin.csrf = ""
	if code, _, _ := admin.do("POST", "/api/users", `{"username":"view","password":"viewer password","role":"viewer"}`); code != 403 {
		t.Fatalf("missing CSRF accepted: %d", code)
	}
	admin.csrf = saved
	if code, _, body := admin.do("POST", "/api/users", `{"username":"view","password":"viewer password","role":"viewer"}`); code != 201 {
		t.Fatalf("create viewer = %d %s", code, body)
	}

	viewer := newClient(t, ts.URL)
	if code, _, _ := viewer.do("POST", "/api/login", `{"username":"view","password":"wrong password"}`); code != 401 {
		t.Fatalf("bad login = %d", code)
	}
	if code, _, body := viewer.do("POST", "/api/login", `{"username":"VIEW","password":"viewer password"}`); code != 200 {
		t.Fatalf("login = %d %s", code, body)
	}
	if code, _, _ := viewer.do("GET", "/api/cameras", ""); code != 200 {
		t.Fatalf("viewer cameras = %d", code)
	}
	if code, _, _ := viewer.do("POST", "/api/cameras/cam1/ptz", `{"action":"start","op":"move","dir":"left","speed":3}`); code != 403 {
		t.Fatalf("viewer PTZ = %d, want 403", code)
	}
	if code, _, _ := viewer.do("GET", "/api/users", ""); code != 403 {
		t.Fatalf("viewer users = %d", code)
	}

	// Cross-origin requests are refused even with a valid session.
	req, _ := http.NewRequest("GET", ts.URL+"/api/cameras", nil)
	req.Header.Set("Origin", "http://evil.example")
	resp, _ := viewer.hc.Do(req)
	if resp.StatusCode != 403 {
		t.Fatalf("cross-origin = %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Last admin cannot be demoted.
	if code, _, _ := admin.do("PATCH", "/api/users/1", `{"role":"viewer"}`); code != 409 {
		t.Fatalf("demote last admin = %d", code)
	}

	// Logout invalidates the session.
	viewer.do("POST", "/api/logout", "")
	if code, _, _ := viewer.do("GET", "/api/me", ""); code != 401 {
		t.Fatalf("after logout = %d", code)
	}
}

func TestLoginRateLimit(t *testing.T) {
	ts, _ := newTestServer(t)
	c := newClient(t, ts.URL)
	last := 0
	for i := 0; i < 7; i++ {
		last, _, _ = c.do("POST", "/api/login", `{"username":"x","password":"yyyyyyyyyyyy"}`)
	}
	if last != 429 {
		t.Fatalf("7th login attempt = %d, want 429", last)
	}
}

func TestStreamTicketAndNoSecrets(t *testing.T) {
	ts, _ := newTestServer(t)
	c := newClient(t, ts.URL)
	c.do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`)

	code, m, body := c.do("POST", "/api/cameras/cam1/stream-ticket?stream=sub", "")
	if code != 200 || !strings.HasPrefix(m["url"].(string), "/stream/ws?ticket=") {
		t.Fatalf("ticket = %d %s", code, body)
	}
	for _, path := range []string{"/api/cameras", "/api/system", "/api/cameras/cam1/control"} {
		_, _, b := c.do("GET", path, "")
		if strings.Contains(b, "Sup3r@Secret") || strings.Contains(b, "password=") {
			t.Fatalf("%s leaks credentials: %s", path, b)
		}
	}
	// A forged ticket is refused before any proxying.
	if code, _, _ := c.do("GET", "/stream/ws?ticket=forged", ""); code != 403 {
		t.Fatalf("forged ticket = %d", code)
	}
	// A valid ticket is single-use: the first use reaches the (dead) go2rtc.
	url := m["url"].(string)
	if code, _, _ := c.do("GET", url, ""); code != 502 {
		t.Fatalf("first use = %d, want 502 (go2rtc down)", code)
	}
	if code, _, _ := c.do("GET", url, ""); code != 403 {
		t.Fatalf("reuse = %d, want 403", code)
	}
}

func TestTimelineValidation(t *testing.T) {
	ts, _ := newTestServer(t)
	c := newClient(t, ts.URL)
	c.do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`)
	if code, _, _ := c.do("GET", "/api/cameras/cam1/timeline?from=x&to=y", ""); code != 400 {
		t.Fatalf("bad range = %d", code)
	}
	if code, m, _ := c.do("GET", "/api/cameras/cam1/timeline?from=2026-09-28T00:00:00Z&to=2026-09-29T00:00:00Z", ""); code != 200 || m["segments"] == nil {
		t.Fatalf("timeline = %d %v", code, m)
	}
	if code, _, _ := c.do("GET", "/api/cameras/nope/timeline?from=2026-09-28T00:00:00Z&to=2026-09-29T00:00:00Z", ""); code != 404 {
		t.Fatalf("unknown camera = %d", code)
	}
}
