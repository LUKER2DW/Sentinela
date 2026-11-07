package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/LUKER2DW/sentinela/internal/store"
)

type seenRequest struct {
	method, path, auth, cookie, csrf string
}

func fakeAgent(t *testing.T) (*httptest.Server, func() []seenRequest) {
	var mu sync.Mutex
	var seen []seenRequest
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, seenRequest{r.Method, r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("Cookie"), r.Header.Get("X-CSRF-Token")})
		mu.Unlock()
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(ts.Close)
	return ts, func() []seenRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]seenRequest(nil), seen...)
	}
}

func TestHostingProxy(t *testing.T) {
	agent, seen := fakeAgent(t)
	ts, srv := newTestServerWith(t, "apps: {enabled: true, agent: "+strings.TrimPrefix(agent.URL, "http://")+"}\n")
	admin := newClient(t, ts.URL)
	if code, _, body := admin.do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`); code != 200 {
		t.Fatalf("setup = %d %s", code, body)
	}
	if code, m, _ := admin.do("GET", "/api/me", ""); code != 200 || m["features"].(map[string]any)["hosting"] != true {
		t.Fatalf("features: %v", m["features"])
	}
	if code, _, body := admin.do("POST", "/api/users", `{"username":"oper","password":"operator password","role":"operator"}`); code != 201 {
		t.Fatalf("create operator = %d %s", code, body)
	}
	if code, _, body := admin.do("GET", "/api/hosting/apps", ""); code != 200 || !strings.Contains(body, "ok") {
		t.Fatalf("list = %d %s", code, body)
	}
	if code, _, _ := admin.do("POST", "/api/hosting/apps", `{"name":"bot"}`); code != 200 {
		t.Fatalf("create = %d", code)
	}
	// ServeMux cleans "..": this ends at /api/users, never at the agent.
	admin.do("GET", "/api/hosting/../users", "")
	got := seen()
	if len(got) != 2 || got[0].path != "/v1/apps" || got[1].method != "POST" {
		t.Fatalf("agent saw %+v", got)
	}
	for _, r := range got {
		if r.auth != "Bearer "+srv.hosting.token || r.cookie != "" || r.csrf != "" {
			t.Fatalf("headers leaked or token missing: %+v", r)
		}
	}
	// Operators never reach the agent.
	oper := newClient(t, ts.URL)
	oper.do("POST", "/api/login", `{"username":"oper","password":"operator password"}`)
	if code, _, _ := oper.do("GET", "/api/hosting/apps", ""); code != 403 {
		t.Fatalf("operator = %d", code)
	}
	// Changes are audited, without bodies.
	_, _, audit := admin.do("GET", "/api/audit", "")
	if !strings.Contains(audit, `"apps.create"`) || strings.Contains(audit, "bot") && strings.Contains(audit, `"name"`) {
		t.Fatalf("audit: %s", audit)
	}
}

func TestHostingZonePolicy(t *testing.T) {
	agent, seen := fakeAgent(t)
	_, srv := newTestServerWith(t, "apps: {enabled: true, agent: "+strings.TrimPrefix(agent.URL, "http://")+"}\n")
	admin := &store.User{ID: 1, Username: "admin", Role: "admin"}
	call := func(method, path string) int {
		r := httptest.NewRequest(method, path, strings.NewReader("{}"))
		ctx := context.WithValue(r.Context(), zoneKey, ZonePublic)
		ctx = context.WithValue(ctx, userKey, admin)
		rec := httptest.NewRecorder()
		srv.hostingProxy(rec, r.WithContext(ctx))
		return rec.Code
	}
	for _, p := range []string{"/api/hosting/apps/bot/restart", "/api/hosting/apps/bot/deploy", "/api/hosting/apps/bot/stop"} {
		if code := call("POST", p); code != 200 {
			t.Errorf("POST %s from outside = %d, want 200", p, code)
		}
	}
	for _, c := range [][2]string{{"POST", "/api/hosting/apps"}, {"PUT", "/api/hosting/apps/bot"}, {"DELETE", "/api/hosting/apps/bot"},
		{"PUT", "/api/hosting/github-token"}, {"POST", "/api/hosting/inspect"}, {"POST", "/api/hosting/uploads"},
		{"PUT", "/api/hosting/apps/bot/secrets"}, {"PUT", "/api/hosting/apps/bot/files/text"}, {"PUT", "/api/hosting/apps/bot/files/upload"},
		{"DELETE", "/api/hosting/apps/bot/files"}} {
		if code := call(c[0], c[1]); code != 403 {
			t.Errorf("%s %s from outside = %d, want 403", c[0], c[1], code)
		}
	}
	if n := len(seen()); n != 3 {
		t.Fatalf("agent got %d requests, want 3", n)
	}
}

func TestHostingLargeSecretsBody(t *testing.T) {
	const size = 3 << 20 // JSON escaping can expand 64 x 8 KiB to 3 MiB.
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, err := io.Copy(io.Discard, r.Body)
		if err != nil || n != size {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(agent.Close)
	_, srv := newTestServerWith(t, "apps: {enabled: true, agent: "+strings.TrimPrefix(agent.URL, "http://")+"}\n")
	r := httptest.NewRequest(http.MethodPut, "/api/hosting/apps/bot/secrets", strings.NewReader(strings.Repeat("x", size)))
	ctx := context.WithValue(r.Context(), zoneKey, ZoneLAN)
	ctx = context.WithValue(ctx, userKey, &store.User{ID: 1, Username: "admin", Role: "admin"})
	w := httptest.NewRecorder()
	srv.hostingProxy(w, r.WithContext(ctx))
	if w.Code != http.StatusOK {
		t.Fatalf("large secrets body: %d %s", w.Code, w.Body.String())
	}
}

func TestHostingOff(t *testing.T) {
	ts, _ := newTestServer(t)
	admin := newClient(t, ts.URL)
	admin.do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`)
	if code, _, _ := admin.do("GET", "/api/hosting/apps", ""); code != 404 {
		t.Fatalf("hosting off = %d", code)
	}
	if _, m, _ := admin.do("GET", "/api/me", ""); m["features"].(map[string]any)["hosting"] != false {
		t.Fatal("feature advertised while off")
	}
}
