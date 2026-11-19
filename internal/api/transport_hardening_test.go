package api

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestPublicAddr(t *testing.T) {
	cases := map[string]bool{
		"203.0.113.9":        true,
		"8.8.8.8":            true,
		"2001:db8::1":        true,
		"::ffff:8.8.8.8":     true,
		"127.0.0.1":          false,
		"::1":                false,
		"10.1.2.3":           false,
		"192.168.1.20":       false,
		"172.16.0.5":         false,
		"169.254.1.1":        false,
		"100.64.1.2":         false, // tailnet (CGNAT)
		"100.127.255.254":    false,
		"fd7a:115c:a1e0::12": false, // tailnet ULA
		"fe80::1":            false,
		"0.0.0.0":            false,
	}
	for in, want := range cases {
		a, err := netip.ParseAddr(in)
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if got := publicAddr(a); got != want {
			t.Errorf("publicAddr(%s) = %v, want %v", in, got, want)
		}
	}
}

func TestWithZoneReclassifiesInternetTrafficOnThePrivateListener(t *testing.T) {
	cases := []struct {
		name   string
		remote string
		hdr    map[string]string
		want   string
	}{
		{"lan peer", "192.168.1.20:5555", nil, ZoneLAN},
		{"lan peer cannot fake a header", "192.168.1.20:5555", map[string]string{"X-Forwarded-For": "203.0.113.9"}, ZoneLAN},
		{"local host, no header", "127.0.0.1:5555", nil, ZoneTailnet},
		{"tailscale serve, tailnet peer", "127.0.0.1:5555", map[string]string{"X-Forwarded-For": "100.101.102.103"}, ZoneTailnet},
		{"tailscale serve, tailnet ULA peer", "[::1]:5555", map[string]string{"X-Forwarded-For": "fd7a:115c:a1e0::1"}, ZoneTailnet},
		{"tunnel on the private port, public client", "127.0.0.1:5555", map[string]string{"X-Forwarded-For": "203.0.113.9"}, ZoneProxy},
		{"proxy chain ends at a public address", "127.0.0.1:5555", map[string]string{"X-Forwarded-For": "10.0.0.1, 203.0.113.9"}, ZoneProxy},
		{"cloudflared header only", "127.0.0.1:5555", map[string]string{"CF-Connecting-IP": "2001:db8::7"}, ZoneProxy},
		{"real-ip header only", "127.0.0.1:5555", map[string]string{"X-Real-IP": "198.51.100.4"}, ZoneProxy},
		{"proxy for the lan, private client", "127.0.0.1:5555", map[string]string{"X-Forwarded-For": "192.168.1.20"}, ZoneTailnet},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got string
			h := withZone("", http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = zoneFrom(r) }))
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = c.remote
			for k, v := range c.hdr {
				r.Header.Set(k, v)
			}
			h.ServeHTTP(httptest.NewRecorder(), r)
			if got != c.want {
				t.Fatalf("zone = %q, want %q", got, c.want)
			}
		})
	}
	// A fixed (public) listener keeps its zone whatever the headers say.
	var got string
	h := withZone(ZonePublic, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = zoneFrom(r) }))
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "127.0.0.1:1"
	r.Header.Set("X-Forwarded-For", "10.0.0.1")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if got != ZonePublic {
		t.Fatalf("zone = %q, want %q", got, ZonePublic)
	}
}

func TestIsHTTPSInPublicZones(t *testing.T) {
	mk := func(zone string, hdr string) *http.Request {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = "127.0.0.1:1"
		if hdr != "" {
			r.Header.Set("X-Forwarded-Proto", hdr)
		}
		var out *http.Request
		withZone(zone, http.HandlerFunc(func(_ http.ResponseWriter, q *http.Request) { out = q })).ServeHTTP(httptest.NewRecorder(), r)
		return out
	}
	if !isHTTPS(mk(ZoneProxy, "")) || !isHTTPS(mk(ZoneFunnel, "")) || !isHTTPS(mk(ZonePublic, "")) {
		t.Fatal("a public zone is HTTPS even when the proxy sends no X-Forwarded-Proto")
	}
	if isHTTPS(mk("", "")) {
		t.Fatal("the plain-HTTP private listener must not be HTTPS without the header")
	}
	if !isHTTPS(mk("", "https")) {
		t.Fatal("tailscale serve on loopback says https")
	}
}

func TestLogPathIsBounded(t *testing.T) {
	long := "/x?" + strings.Repeat("a", 5000)
	r := httptest.NewRequest("GET", long, nil)
	got := logPath(r)
	if len(got) > maxLoggedPath+len("…") || !strings.HasSuffix(got, "…") {
		t.Fatalf("logged path %d bytes, want <= %d with a marker", len(got), maxLoggedPath)
	}
	if p := logPath(httptest.NewRequest("GET", "/api/cameras", nil)); p != "/api/cameras" {
		t.Fatalf("short path changed: %q", p)
	}
	// A cut in the middle of a multi-byte character leaves valid UTF-8.
	r = httptest.NewRequest("GET", "/"+strings.Repeat("é", 400), nil)
	if g := logPath(r); !strings.HasSuffix(g, "…") {
		t.Fatalf("not cut: %d bytes", len(g))
	}
}

func TestClosedDoorSetsAndClearsDeadlines(t *testing.T) {
	s := &Server{}
	var seen bool
	h := s.closedDoor(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen = true }))
	// The recorder does not support deadlines; the middleware must not fail.
	r := httptest.NewRequest("GET", "/index.html", nil)
	r = r.WithContext(context.WithValue(r.Context(), zoneKey, ZonePublic))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if !seen || rec.Code == http.StatusInternalServerError {
		t.Fatalf("SPA shell must pass the closed door (seen=%v code=%d)", seen, rec.Code)
	}
	// Anything else is a 404 with no session.
	r = httptest.NewRequest("GET", "/api/cameras", nil)
	r = r.WithContext(context.WithValue(r.Context(), zoneKey, ZonePublic))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404", rec.Code)
	}
}

func TestLimitConnsPerSourceAndTotal(t *testing.T) {
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("no loopback listener:", err)
	}
	ln := LimitConns(inner, 3, 2)
	defer ln.Close()
	accepted := make(chan net.Conn, 8)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			accepted <- c
		}
	}()
	dial := func() net.Conn {
		c, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	get := func() net.Conn {
		select {
		case c := <-accepted:
			return c
		case <-time.After(time.Second):
			return nil
		}
	}
	// Two connections from the one source (127.0.0.1) are accepted...
	c1, c2 := dial(), dial()
	defer c1.Close()
	defer c2.Close()
	s1, s2 := get(), get()
	if s1 == nil || s2 == nil {
		t.Fatal("first two connections must be accepted")
	}
	// ...the third from the same source is closed before it reaches Accept's caller.
	c3 := dial()
	defer c3.Close()
	if c := get(); c != nil {
		t.Fatal("third connection from one source was accepted")
	}
	_ = c3.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := c3.Read(make([]byte, 1)); err == nil {
		t.Fatal("the refused connection must be closed")
	}
	// Closing one frees the slot.
	_ = s1.Close()
	c4 := dial()
	defer c4.Close()
	if get() == nil {
		t.Fatal("a slot was not freed after close")
	}
}

func TestLimitConnsReleaseIsIdempotent(t *testing.T) {
	l := &limitListener{total: 2, perIP: 2, per: map[string]int{}}
	if !l.take("a") {
		t.Fatal("take")
	}
	c := &limitConn{l: l, key: "a", Conn: nopConn{}}
	_ = c.Close()
	_ = c.Close() // a double close must not release twice
	if l.n != 0 || len(l.per) != 0 {
		t.Fatalf("n=%d per=%v, want empty", l.n, l.per)
	}
}

type nopConn struct{ net.Conn }

func (nopConn) Close() error { return nil }

func TestLogSafeIsOneLine(t *testing.T) {
	for in, want := range map[string]string{
		"plain":                "plain",
		"a\nb":                 "a b",
		"a\r\nINFO forged":     "a  INFO forged",
		"/x?q=1\n{\"fake\":1}": "/x?q=1 {\"fake\":1}",
	} {
		if got := logSafe(in); got != want {
			t.Errorf("logSafe(%q) = %q, want %q", in, got, want)
		}
	}
	r := httptest.NewRequest("GET", "/x", nil)
	r.URL.RawQuery = "a=1\nINFO forged"
	if p := logPath(r); strings.ContainsAny(p, "\r\n") {
		t.Fatalf("logged path has a line break: %q", p)
	}
}
