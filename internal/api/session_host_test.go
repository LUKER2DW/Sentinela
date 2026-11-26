package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LUKER2DW/sentinela/internal/auth"
)

func TestEffectiveHost(t *testing.T) {
	cases := []struct {
		name, remote, host string
		xfh                []string
		want               string
	}{
		{"plain", "192.168.1.9:5000", "nvr.lan:8080", nil, "nvr.lan:8080"},
		{"lower-cased", "192.168.1.9:5000", "NVR.Example.ORG", nil, "nvr.example.org"},
		{"port kept", "203.0.113.7:5000", "nvr.example.org:8443", nil, "nvr.example.org:8443"},
		{"forwarded from loopback", "127.0.0.1:41000", "127.0.0.1:8080", []string{"Nvr.Example.org"}, "nvr.example.org"},
		{"last forwarded value", "127.0.0.1:41000", "127.0.0.1:8080", []string{"evil.test, nvr.example.org"}, "nvr.example.org"},
		{"last header line", "[::1]:41000", "localhost", []string{"evil.test", "nvr.example.org"}, "nvr.example.org"},
		{"forwarded ignored from the network", "203.0.113.7:5000", "nvr.example.org", []string{"evil.test"}, "nvr.example.org"},
		{"empty forwarded value", "127.0.0.1:41000", "nvr.example.org", []string{" "}, "nvr.example.org"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr, r.Host = c.remote, c.host
			for _, v := range c.xfh {
				r.Header.Add("X-Forwarded-Host", v)
			}
			if got := effectiveHost(r); got != c.want {
				t.Errorf("effectiveHost = %q, want %q", got, c.want)
			}
		})
	}
}

// signedIn makes a session for a fresh admin created on zone and host and
// returns its token.
func signedIn(t *testing.T, srv *Server, zone, host string) string {
	t.Helper()
	ctx := context.Background()
	id, err := srv.Store.CreateFirstUser(ctx, "admin", "hash")
	if err != nil {
		t.Fatal(err)
	}
	tok := auth.NewToken()
	if err := srv.Store.CreateSession(ctx, auth.HashToken(tok), id, "csrf", time.Now().Add(time.Hour), "ua", false, true, host, zone); err != nil {
		t.Fatal(err)
	}
	return tok
}

func TestSessionBoundToHost(t *testing.T) {
	const home = "nvr.example.org"
	const lanHost = "192.168.1.5:8080"
	cases := []struct {
		name        string
		createdZone string // zone the session was created on ("" = before 0013)
		created     string // Host the session was created on
		public      string // "" = private listener, else the public zone
		remote      string
		host        string
		xfh         string
		want        int
	}{
		{"public, same host", ZonePublic, home, ZonePublic, "203.0.113.7:5000", home, "", 200},
		{"public, host case does not matter", ZonePublic, home, ZonePublic, "203.0.113.7:5000", "NVR.example.org", "", 200},
		{"public, other host", ZonePublic, home, ZonePublic, "203.0.113.7:5000", "192.0.2.10:20001", "", 401},
		{"public, same name other port", ZonePublic, home, ZonePublic, "203.0.113.7:5000", home + ":20001", "", 401},
		{"public, port was part of the host", ZonePublic, home + ":8443", ZonePublic, "203.0.113.7:5000", home + ":8443", "", 200},
		{"funnel zone is bound too", ZoneFunnel, home, ZoneFunnel, "203.0.113.7:5000", "other.example.org", "", 401},
		{"proxy, forwarded host matches", ZoneProxy, home, ZoneProxy, "127.0.0.1:41000", "127.0.0.1:8080", home, 200},
		{"proxy, forwarded host differs", ZoneProxy, home, ZoneProxy, "127.0.0.1:41000", home, "other.example.org", 401},
		{"public from the network, forwarded host ignored", ZonePublic, home, ZonePublic, "203.0.113.7:5000", "other.example.org", home, 401},
		// The audit's lead: the Host header is the replaying client's own
		// choice, so a session made elsewhere must be refused whatever it says.
		{"LAN session replayed at the public listener with its LAN host", ZoneLAN, lanHost, ZonePublic, "203.0.113.7:5000", lanHost, "", 401},
		{"LAN session replayed through the proxy with a forwarded LAN host", ZoneLAN, lanHost, ZoneProxy, "127.0.0.1:41000", "127.0.0.1:7444", lanHost, 401},
		{"tailnet session replayed at the public listener", ZoneTailnet, "nvr.tailnet.ts.net", ZonePublic, "203.0.113.7:5000", "nvr.tailnet.ts.net", "", 401},
		{"session of one public path at another", ZonePublic, home, ZoneProxy, "127.0.0.1:41000", "127.0.0.1:7444", home, 401},
		{"public, legacy session without zone", "", home, ZonePublic, "203.0.113.7:5000", home, "", 401},
		{"public, legacy session without host", "", "", ZonePublic, "203.0.113.7:5000", "192.0.2.10:20001", "", 401},
		{"LAN is not bound", ZonePublic, home, "", "192.168.1.9:5000", lanHost, "", 200},
		{"LAN accepts its own sessions", ZoneLAN, lanHost, "", "192.168.1.9:5000", lanHost, "", 200},
		{"tailnet is not bound", ZoneLAN, home, "", "127.0.0.1:41000", "nvr.tailnet.ts.net", "", 200},
		{"legacy sessions still work at home", "", "", "", "192.168.1.9:5000", lanHost, "", 200},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, srv := newTestServer(t)
			tok := signedIn(t, srv, c.createdZone, c.created)
			h := srv.Handler()
			if c.public != "" {
				h = srv.PublicHandler(c.public)
			}
			r := httptest.NewRequest(http.MethodGet, "/api/me", nil)
			r.RemoteAddr, r.Host = c.remote, c.host
			if c.xfh != "" {
				r.Header.Set("X-Forwarded-Host", c.xfh)
			}
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			got := w.Code
			if got != 200 { // without a session /api/me is 401, or 404 behind the closed door
				got = 401
			}
			if got != c.want {
				t.Errorf("GET /api/me = %d, want %d", w.Code, c.want)
			}
		})
	}
}

// End to end: a cookie from a LAN login, the Host it was made on and a
// matching forwarded host do not open the public listeners.
func TestLANLoginCookieIsRefusedOutside(t *testing.T) {
	_, srv := newTestServer(t)
	post := func(h http.Handler, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		r.RemoteAddr, r.Host = "192.168.1.9:5000", "nvr.lan:8080"
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := post(srv.Handler(), "/api/setup", `{"username":"owner","password":"long enough pw"}`); w.Code != 200 {
		t.Fatalf("setup = %d %s", w.Code, w.Body.String())
	}
	w := post(srv.Handler(), "/api/login", `{"username":"owner","password":"long enough pw"}`)
	var tok string
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookie {
			tok = c.Value
		}
	}
	if w.Code != 200 || tok == "" {
		t.Fatalf("LAN login = %d %s", w.Code, w.Body.String())
	}
	sess, _, err := srv.Store.SessionByHash(context.Background(), auth.HashToken(tok))
	if err != nil || sess.Zone != ZoneLAN {
		t.Fatalf("session zone = %v %v, want lan", sess, err)
	}
	for _, c := range []struct{ zone, remote, host, xfh string }{
		{ZonePublic, "203.0.113.7:5000", "nvr.lan:8080", ""},
		{ZoneFunnel, "127.0.0.1:41000", "nvr.lan:8080", ""},
		{ZoneProxy, "127.0.0.1:41000", "127.0.0.1:7444", "nvr.lan:8080"},
	} {
		r := httptest.NewRequest(http.MethodGet, "/api/me", nil)
		r.RemoteAddr, r.Host = c.remote, c.host
		if c.xfh != "" {
			r.Header.Set("X-Forwarded-Host", c.xfh)
		}
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
		w := httptest.NewRecorder()
		srv.PublicHandler(c.zone).ServeHTTP(w, r)
		if w.Code == 200 {
			t.Errorf("%s: LAN cookie accepted from the internet: %s", c.zone, w.Body.String())
		}
	}
	// It still works at home.
	r := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	r.RemoteAddr, r.Host = "192.168.1.9:5000", "nvr.lan:8080"
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("LAN cookie at home = %d", w.Code)
	}
}

func TestSessionStoresHostItWasCreatedOn(t *testing.T) {
	_, srv := newTestServer(t)
	h := srv.Handler()
	r := httptest.NewRequest(http.MethodPost, "/api/setup", strings.NewReader(`{"username":"owner","password":"long enough pw"}`))
	r.RemoteAddr, r.Host = "192.168.1.9:5000", "Nvr.Lan:8080"
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("setup = %d %s", w.Code, w.Body.String())
	}
	var tok string
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookie {
			tok = c.Value
		}
	}
	sess, _, err := srv.Store.SessionByHash(context.Background(), auth.HashToken(tok))
	if err != nil {
		t.Fatal(err)
	}
	if sess.Host != "nvr.lan:8080" || sess.Zone != ZoneLAN {
		t.Errorf("session host/zone = %q/%q, want nvr.lan:8080/lan", sess.Host, sess.Zone)
	}
}

func TestSetupVersionHiddenFromTheInternet(t *testing.T) {
	_, srv := newTestServer(t)
	for _, c := range []struct {
		name    string
		h       http.Handler
		remote  string
		wantVer bool
	}{
		{"LAN", srv.Handler(), "192.168.1.9:5000", true},
		{"public", srv.PublicHandler(ZonePublic), "203.0.113.7:5000", false},
		{"proxy", srv.PublicHandler(ZoneProxy), "127.0.0.1:41000", false},
	} {
		r := httptest.NewRequest(http.MethodGet, "/api/setup", nil)
		r.RemoteAddr = c.remote
		w := httptest.NewRecorder()
		c.h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("%s: setup = %d", c.name, w.Code)
		}
		if has := strings.Contains(w.Body.String(), `"version"`); has != c.wantVer {
			t.Errorf("%s: version present = %v, want %v: %s", c.name, has, c.wantVer, w.Body.String())
		}
	}
}

func TestCameraProbesAreHomeOnly(t *testing.T) {
	_, srv := newTestServer(t)
	tok := signedIn(t, srv, ZonePublic, "nvr.example.org")
	for _, path := range []string{"/api/cameras/test", "/api/cameras/discover"} {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"host":"192.168.1.50"}`))
		r.RemoteAddr, r.Host = "203.0.113.7:5000", "nvr.example.org"
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-CSRF-Token", "csrf")
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
		w := httptest.NewRecorder()
		srv.PublicHandler(ZonePublic).ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Errorf("POST %s from the internet = %d, want 403: %s", path, w.Code, w.Body.String())
		}
	}
}

func TestSystemNetworkNamesAreForAdmins(t *testing.T) {
	dir := t.TempDir()
	health := filepath.Join(dir, "disks.json")
	if err := os.WriteFile(health, []byte(`{"updated":"2026-01-01T00:00:00Z","mounts":{"/srv/nas":{"device":"/dev/sdb","state":"ok"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ts, _ := newTestServerWith(t, "nas_host: SENTINELA\ndisk_health_file: "+filepath.ToSlash(health)+
		"\nvolumes:\n  - {id: nuvem, name: Nuvem, path: "+filepath.ToSlash(dir)+", share: Nuvem, kind: nas}\n")
	admin := newClient(t, ts.URL)
	admin.do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`)
	kid, _ := restrictedViewer(t, ts, admin, `"cam1"`)

	_, m, body := admin.do("GET", "/api/system", "")
	for _, want := range []string{`"nas_host":"SENTINELA"`, `"share":"Nuvem"`, `/srv/nas`} {
		if !strings.Contains(body, want) {
			t.Errorf("admin /api/system lacks %s: %s", want, body)
		}
	}
	if m["disk_health"] == nil {
		t.Error("admin lost disk_health")
	}
	_, m, body = kid.do("GET", "/api/system", "")
	for _, bad := range []string{"SENTINELA", `"share"`, "/srv/nas", "/dev/sdb"} {
		if strings.Contains(body, bad) {
			t.Errorf("non-admin /api/system leaks %s: %s", bad, body)
		}
	}
	if m["disk_health"] != nil || m["nas_host"] != "" {
		t.Errorf("non-admin disk_health/nas_host = %v / %v", m["disk_health"], m["nas_host"])
	}
	vols, _ := m["volumes"].([]any)
	if len(vols) != 1 || vols[0].(map[string]any)["total"] == nil {
		t.Errorf("non-admin lost the usage numbers: %v", m["volumes"])
	}
}
