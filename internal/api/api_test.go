package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	return newTestServerWith(t, "")
}

// newTestServerWith adds YAML (e.g. volumes) to the test config.
func newTestServerWith(t *testing.T, extra string) (*httptest.Server, *Server) {
	t.Helper()
	cfg, err := config.Parse([]byte(extra+`
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

func TestForwardedHeadersTrustedOnlyFromLoopback(t *testing.T) {
	cases := []struct {
		name, peer, xff, proto string
		ip                     string
		https                  bool
	}{
		{"lan client ignores forged headers", "192.168.1.52:5000", "100.64.0.9", "https", "192.168.1.52", false},
		{"tailscale serve on loopback", "127.0.0.1:41000", "100.101.1.2", "https", "100.101.1.2", true},
		{"client-supplied hops before the proxy hop", "127.0.0.1:41000", "1.2.3.4, 100.101.1.2", "https", "100.101.1.2", true},
		{"loopback without headers", "[::1]:41000", "", "", "::1", false},
		{"garbage forwarded ip", "127.0.0.1:41000", "not-an-ip", "http", "127.0.0.1", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = c.peer
			if c.xff != "" {
				r.Header.Set("X-Forwarded-For", c.xff)
			}
			if c.proto != "" {
				r.Header.Set("X-Forwarded-Proto", c.proto)
			}
			if got := clientIP(r); got != c.ip {
				t.Errorf("clientIP = %q, want %q", got, c.ip)
			}
			if got := isHTTPS(r); got != c.https {
				t.Errorf("isHTTPS = %v, want %v", got, c.https)
			}
		})
	}
}

func TestSessionCookieSecureOnlyOverHTTPS(t *testing.T) {
	_, srv := newTestServer(t)
	h := srv.Handler()
	post := func(path, peer, proto string) *http.Cookie {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"username":"owner","password":"correct horse"}`))
		r.RemoteAddr = peer
		r.Header.Set("Content-Type", "application/json")
		if proto != "" {
			r.Header.Set("X-Forwarded-Proto", proto)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("%s from %s: %d %s", path, peer, w.Code, w.Body.String())
		}
		for _, c := range w.Result().Cookies() {
			if c.Name == sessionCookie {
				return c
			}
		}
		t.Fatalf("%s: no session cookie", path)
		return nil
	}
	if c := post("/api/setup", "192.168.1.52:5000", "https"); c.Secure {
		t.Error("LAN HTTP login got a Secure cookie (forged X-Forwarded-Proto trusted)")
	}
	if c := post("/api/login", "127.0.0.1:41000", "https"); !c.Secure {
		t.Error("login through tailscale serve did not get a Secure cookie")
	}
}

func TestNuvemFileAPI(t *testing.T) {
	share := t.TempDir()
	ts, _ := newTestServerWith(t, "volumes:\n  - {id: nuvem, name: Nuvem, path: "+filepath.ToSlash(share)+", kind: nas}\n")
	admin := newClient(t, ts.URL)
	if code, _, body := admin.do("POST", "/api/setup", `{"username":"dono","password":"long enough pw"}`); code != 200 {
		t.Fatalf("setup = %d %s", code, body)
	}
	admin.do("POST", "/api/users", `{"username":"view","password":"viewer password","role":"viewer"}`)

	code, m, body := admin.do("GET", "/api/nas/list?path=", "")
	if code != 200 || !strings.Contains(body, `"dono"`) || !strings.Contains(body, `"Compartilhado"`) || m["can_write"] != true {
		t.Fatalf("list root = %d %s", code, body)
	}
	// Resumable upload in two chunks.
	if code, _, body := admin.do("PUT", "/api/nas/upload?path=dono/a.txt&offset=0&total=11", "hello "); code != 200 {
		t.Fatalf("chunk 1 = %d %s", code, body)
	}
	if code, m, _ := admin.do("GET", "/api/nas/upload?path=dono/a.txt", ""); code != 200 || m["offset"] != float64(6) {
		t.Fatalf("resume point = %d %v", code, m)
	}
	if code, m, _ := admin.do("PUT", "/api/nas/upload?path=dono/a.txt&offset=0&total=11", "x"); code != 409 || m["offset"] != float64(6) {
		t.Fatalf("stale offset = %d %v", code, m)
	}
	if code, m, _ := admin.do("PUT", "/api/nas/upload?path=dono/a.txt&offset=6&total=11", "world"); code != 200 || m["done"] != true {
		t.Fatalf("chunk 2 = %d %v", code, m)
	}
	// Range download, served as an attachment with a sandbox CSP.
	req, _ := http.NewRequest("GET", ts.URL+"/api/nas/file?path=dono/a.txt", nil)
	req.Header.Set("Range", "bytes=6-")
	resp, err := admin.hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 206 || string(got) != "world" || !strings.HasPrefix(resp.Header.Get("Content-Disposition"), "attachment") ||
		!strings.Contains(resp.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("download = %d %q %v", resp.StatusCode, got, resp.Header)
	}
	// Viewer: reads the shared folder, cannot write, cannot see the admin's folder.
	viewer := newClient(t, ts.URL)
	viewer.do("POST", "/api/login", `{"username":"view","password":"viewer password"}`)
	if code, _, _ := viewer.do("PUT", "/api/nas/upload?path=Compartilhado/x&offset=0&total=1", "x"); code != 403 {
		t.Fatalf("viewer upload = %d", code)
	}
	if code, _, _ := viewer.do("GET", "/api/nas/file?path=dono/a.txt", ""); code != 403 {
		t.Fatalf("viewer reads admin file = %d", code)
	}
	if code, _, _ := viewer.do("GET", "/api/nas/file?path=../../etc/passwd", ""); code != 403 && code != 404 {
		t.Fatalf("traversal = %d", code)
	}
	// Delete goes to the recycle bin and is audited.
	if code, m, _ := admin.do("DELETE", "/api/nas/file?path=dono/a.txt", ""); code != 200 || m["trash"] != ".lixeira/dono/dono/a.txt" {
		t.Fatalf("delete = %d %v", code, m)
	}
	if _, err := os.Stat(filepath.Join(share, ".lixeira", "dono", "dono", "a.txt")); err != nil {
		t.Fatalf("not in the bin: %v", err)
	}
	if _, _, body := admin.do("GET", "/api/audit", ""); !strings.Contains(body, "nas.upload") || !strings.Contains(body, "nas.delete") {
		t.Fatalf("audit = %s", body)
	}
}

func TestEventSnapshot(t *testing.T) {
	jpeg := []byte("\xff\xd8\xff\xe0fake-jpeg\xff\xd9")
	calls := 0
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/frame.jpeg" || r.URL.Query().Get("src") != "cam1_sub" {
			http.NotFound(w, r)
			return
		}
		w.Write(jpeg)
	}))
	defer fake.Close()
	ts, srv := newTestServer(t)
	srv.go2rtc, _ = url.Parse(fake.URL)
	admin := newClient(t, ts.URL)
	admin.do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`)

	ctx := context.Background()
	now := time.Now().UTC()
	id1, _ := srv.Store.OpenEvent(ctx, "cam1", "motion", now, "")
	id2, _ := srv.Store.OpenEvent(ctx, "cam1", "motion", now.Add(time.Second), "")
	srv.takeSnapshot(ctx, "cam1", id1)
	srv.takeSnapshot(ctx, "cam1", id2) // within 10 s: rate-limited
	if calls != 1 {
		t.Fatalf("go2rtc called %d times, want 1 (rate limit)", calls)
	}
	code, _, body := admin.do("GET", fmt.Sprintf("/api/events/%d/snapshot", id1), "")
	if code != 200 || body != string(jpeg) {
		t.Fatalf("snapshot = %d %q", code, body)
	}
	if code, _, _ := admin.do("GET", fmt.Sprintf("/api/events/%d/snapshot", id2), ""); code != 404 {
		t.Fatalf("rate-limited event snapshot = %d, want 404", code)
	}
	if _, _, body := admin.do("GET", "/api/events", ""); !strings.Contains(body, `"snapshot":true`) {
		t.Fatalf("events list lacks snapshot flag: %s", body)
	}
}

func TestReviewItemsAndSeen(t *testing.T) {
	ts, srv := newTestServer(t)
	admin := newClient(t, ts.URL)
	admin.do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`)
	ctx := context.Background()
	t0 := time.Now().UTC().Add(-time.Hour)
	a, _ := srv.Store.OpenEvent(ctx, "cam1", "motion", t0, "")
	_ = srv.Store.CloseEvent(ctx, a, t0.Add(5*time.Second))
	b, _ := srv.Store.OpenEvent(ctx, "cam1", "intrusion", t0.Add(20*time.Second), "")
	_ = srv.Store.CloseEvent(ctx, b, t0.Add(25*time.Second))
	c, _ := srv.Store.OpenEvent(ctx, "cam1", "motion", t0.Add(10*time.Minute), "")
	_ = c

	_, m, body := admin.do("GET", "/api/review", "")
	items, _ := m["items"].([]any)
	if len(items) != 2 || m["unseen"] != float64(2) || !strings.Contains(body, `"severity":"alert"`) {
		t.Fatalf("review = %s", body)
	}
	if _, m, _ := admin.do("GET", "/api/review?severity=alert", ""); len(m["items"].([]any)) != 1 {
		t.Fatalf("alert filter = %v", m)
	}
	if code, _, _ := admin.do("POST", "/api/review/seen", fmt.Sprintf(`{"ids":[%d]}`, a)); code != 200 {
		t.Fatalf("mark seen = %d", code)
	}
	if _, m, _ := admin.do("GET", "/api/review?unseen=1", ""); len(m["items"].([]any)) != 1 || m["unseen"] != float64(1) {
		t.Fatalf("after seen = %v", m)
	}
	admin.do("POST", "/api/review/seen", `{"all":true}`)
	if _, m, _ := admin.do("GET", "/api/review", ""); m["unseen"] != float64(0) {
		t.Fatalf("after mark all = %v", m)
	}
}

func TestNotifyModes(t *testing.T) {
	loc := time.FixedZone("BRT", -3*3600)
	at := func(h, m int) time.Time { return time.Date(2026, 9, 29, h, m, 0, 0, loc) }
	home := ModeSettings{Mode: ModeHome, NightAuto: true, NightFrom: "23:00", NightTo: "06:00"}
	cases := []struct {
		m    ModeSettings
		now  time.Time
		want string
	}{
		{home, at(12, 0), ModeHome},
		{home, at(23, 30), ModeNight},
		{home, at(5, 59), ModeNight},
		{home, at(6, 0), ModeHome},
		{ModeSettings{Mode: ModeHome, NightFrom: "23:00", NightTo: "06:00"}, at(23, 30), ModeHome},
		{ModeSettings{Mode: ModeAway, NightAuto: true, NightFrom: "23:00", NightTo: "06:00"}, at(23, 30), ModeAway},
		{ModeSettings{Mode: ModeHome, NightAuto: true, NightFrom: "13:00", NightTo: "14:00"}, at(13, 30), ModeNight},
	}
	for _, c := range cases {
		if got := c.m.Effective(c.now); got != c.want {
			t.Errorf("%+v at %s = %s, want %s", c.m, c.now.Format("15:04"), got, c.want)
		}
	}
	for _, c := range []struct {
		mode, typ string
		want      bool
	}{
		{ModeAway, "motion", true}, {ModeNight, "motion", false}, {ModeNight, "line", true},
		{ModeHome, "line", false}, {ModeHome, "tamper", true}, {"bogus", "tamper", false},
	} {
		if Notifies(c.mode, c.typ) != c.want {
			t.Errorf("Notifies(%s, %s) != %v", c.mode, c.typ, c.want)
		}
	}
}

func TestPushSubscribeAndMode(t *testing.T) {
	ts, _ := newTestServer(t)
	admin := newClient(t, ts.URL)
	admin.do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`)
	if code, m, _ := admin.do("GET", "/api/push", ""); code != 200 || m["public_key"] == "" {
		t.Fatalf("push info = %d %v", code, m)
	}
	if code, _, _ := admin.do("POST", "/api/push/subscribe", `{"endpoint":"https://192.168.1.10/x","keys":{"p256dh":"a","auth":"b"}}`); code != 400 {
		t.Fatalf("LAN endpoint accepted: %d", code)
	}
	if code, _, body := admin.do("POST", "/api/push/subscribe", `{"endpoint":"https://fcm.googleapis.com/fcm/send/x","expirationTime":null,"keys":{"p256dh":"a","auth":"b"}}`); code != 200 {
		t.Fatalf("subscribe = %d %s", code, body)
	}
	if _, m, _ := admin.do("GET", "/api/push", ""); m["devices"] != float64(1) {
		t.Fatalf("devices = %v", m)
	}
	if _, m, _ := admin.do("GET", "/api/notify-mode", ""); m["settings"].(map[string]any)["mode"] != "away" {
		t.Fatalf("default mode = %v", m)
	}
	if code, _, _ := admin.do("PUT", "/api/notify-mode", `{"mode":"home","night_auto":true,"night_from":"25:00","night_to":"06:00"}`); code != 400 {
		t.Fatalf("bad time accepted: %d", code)
	}
	if code, m, _ := admin.do("PUT", "/api/notify-mode", `{"mode":"home","night_auto":false,"night_from":"23:00","night_to":"06:00"}`); code != 200 || m["effective"] != "home" {
		t.Fatalf("set mode = %d %v", code, m)
	}
}
