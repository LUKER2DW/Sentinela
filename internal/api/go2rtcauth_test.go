package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
)

// authGo2RTC answers like go2rtc with api.username/password and local_auth:
// every path needs the credential. It remembers the Authorization headers.
func authGo2RTC(t *testing.T, password string) (*url.URL, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var seen []string
	be := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Header.Get("Authorization"))
		mu.Unlock()
		if u, p, ok := r.BasicAuth(); !ok || u != "sentinela" || p != password {
			w.Header().Set("WWW-Authenticate", `Basic realm="go2rtc"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/streams":
			_, _ = io.WriteString(w, `{"cam1":{"producers":[{"url":"rtsp://x"}],"consumers":[]}}`)
		case "/api/frame.jpeg":
			_, _ = w.Write([]byte("\xff\xd8jpeg\xff\xd9"))
		case "/api/ws":
			_, _ = io.WriteString(w, "ws-ok")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(be.Close)
	u, _ := url.Parse(be.URL)
	return u, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), seen...) }
}

func TestGo2RTCCallsCarryTheAPIPassword(t *testing.T) {
	const pw = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	for _, tc := range []struct {
		name, serverPW string
		ok             bool
	}{
		{"right password", pw, true},
		{"wrong password", "not-the-password-but-long-enough-000000000000", false},
		{"no password", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts, srv := newTestServer(t)
			be, seen := authGo2RTC(t, pw)
			srv.go2rtc = be
			srv.Go2RTCPassword = tc.serverPW
			ctx := context.Background()

			if st, err := srv.go2rtcStatus(ctx); (err == nil) != tc.ok || tc.ok && len(st) != 1 {
				t.Errorf("go2rtcStatus = %v, %v", st, err)
			}
			if b, err := srv.grabFrame(ctx, "cam1"); (err == nil) != tc.ok || tc.ok && len(b) == 0 {
				t.Errorf("grabFrame = %d bytes, %v", len(b), err)
			}

			// The live proxy: a browser's own Authorization never reaches go2rtc.
			admin := newClient(t, ts.URL)
			admin.do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`)
			_, m, body := admin.do("POST", "/api/cameras/cam1/stream-ticket", "")
			path, _ := m["url"].(string)
			if path == "" {
				t.Fatalf("ticket = %s", body)
			}
			req, _ := http.NewRequest("GET", ts.URL+path, nil)
			req.SetBasicAuth("sentinela", pw) // a client that knows the password still cannot pass it
			resp, err := admin.hc.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			got, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if (resp.StatusCode == 200 && string(got) == "ws-ok") != tc.ok {
				t.Errorf("stream proxy = %d %q", resp.StatusCode, got)
			}
			want := ""
			if tc.serverPW != "" {
				r := httptest.NewRequest("GET", "/", nil)
				r.SetBasicAuth("sentinela", tc.serverPW)
				want = r.Header.Get("Authorization")
			}
			for _, a := range seen() {
				if a != want {
					t.Errorf("go2rtc got Authorization %q, want %q", a, want)
				}
			}
		})
	}
}
