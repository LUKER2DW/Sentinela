package api

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/LUKER2DW/sentinela/internal/store"
)

// newPublicTLS serves srv's port-forward zone over TLS and HTTP/2 the way
// main does (ConfigurePublicServer, PublicConnLimits), with the anonymous
// deadlines shortened to readTO and writeTO.
func newPublicTLS(t *testing.T, srv *Server, readTO, writeTO time.Duration) (*httptest.Server, *connPool) {
	t.Helper()
	ts := httptest.NewUnstartedServer(srv.PublicHandler(ZonePublic))
	ts.EnableHTTP2 = true
	ConfigurePublicServer(ts.Config)
	ts.Config.ReadTimeout, ts.Config.WriteTimeout = readTO, writeTO
	pool := newConnPool(ts.Listener, PublicConnLimits())
	pool.hook(ts.Config)
	ts.Listener = pool
	ts.StartTLS()
	t.Cleanup(ts.Close)
	return ts, pool
}

// publicSession signs admin in on the public listener and returns the cookie.
func publicSession(t *testing.T, ts *httptest.Server) string {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	hc := ts.Client()
	hc.Jar = jar
	res, err := hc.Post(ts.URL+"/api/login", "application/json", strings.NewReader(`{"username":"admin","password":"long enough pw"}`))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	for _, c := range res.Cookies() {
		if c.Name == sessionCookie {
			return c.Name + "=" + c.Value
		}
	}
	t.Fatalf("public login = %d, no session cookie", res.StatusCode)
	return ""
}

// Lead 1: closedDoor used to clear the anonymous deadlines when it returned,
// so the response flush that net/http does after the handler waited for ever
// on a client that grants no window. Now an anonymous stream ends at the
// write deadline; a signed-in one is not cut.
func TestPublicH2AnonymousStreamsEndSignedInLive(t *testing.T) {
	priv, srv := newTestServer(t)
	newClient(t, priv.URL).do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`)
	// The sign-in below runs within these deadlines: argon2id under the race
	// detector takes longer than 300 ms.
	writeTO := 300 * time.Millisecond
	if raceEnabled {
		writeTO = 5 * time.Second
	}
	ts, pool := newPublicTLS(t, srv, writeTO, writeTO)
	cookie := publicSession(t, ts)

	anon := dialH2(t, ts, map[uint16]uint32{h2SettingInitialWindowSize: 0})
	const n = 8
	for i := uint32(0); i < n; i++ {
		anon.get(2*i+1, "/api/nope", true)
	}
	resets := map[uint32]bool{}
	if !anon.until(10*writeTO, func(f h2frame) bool {
		if f.typ == h2RSTStream {
			resets[f.stream] = true
		}
		return len(resets) == n
	}) {
		t.Fatalf("anonymous streams reset: %d of %d", len(resets), n)
	}

	me := dialH2(t, ts, map[uint16]uint32{h2SettingInitialWindowSize: 0})
	me.get(1, "/api/me", true, "cookie", cookie)
	if me.until(4*writeTO, func(f h2frame) bool { return f.stream == 1 && (f.typ == h2RSTStream || f.typ == h2Data) }) {
		t.Fatal("a signed-in stream was cut (or sent DATA with no window)")
	}
	me.windowUpdate(1, 1<<16)
	if !me.until(2*time.Second, func(f h2frame) bool { return f.stream == 1 && f.typ == h2Data }) {
		t.Fatal("no response once the window opened")
	}
	// The signed-in connections (the login client's and this one) are the
	// household's: out of the anonymous share.
	pool.mu.Lock()
	trusted := 0
	for _, v := range pool.src {
		trusted += v
	}
	trusted -= pool.anonN
	pool.mu.Unlock()
	if trusted < 1 {
		t.Fatal("no connection was marked signed in")
	}
}

// Lead 1, HTTP/1: a body declared and never sent used to stall net/http's
// drain after the handler with no deadline; now the request ends at the read
// deadline with an answer and a closed connection.
func TestPublicHTTP1UnsentBodyIsBounded(t *testing.T) {
	_, srv := newTestServer(t)
	ts := httptest.NewUnstartedServer(srv.PublicHandler(ZoneProxy))
	ConfigurePublicServer(ts.Config)
	ts.Config.ReadTimeout, ts.Config.WriteTimeout = 300*time.Millisecond, time.Second
	ts.Start()
	t.Cleanup(ts.Close)
	for _, req := range []string{
		"GET /api/x HTTP/1.1\r\nHost: t\r\nContent-Length: 10\r\n\r\n",
		"POST /api/login HTTP/1.1\r\nHost: t\r\nTransfer-Encoding: chunked\r\n\r\n5\r\n{\"use",
	} {
		c, err := net.Dial("tcp", ts.Listener.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		_, _ = io.WriteString(c, req)
		_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
		got, err := io.ReadAll(c)
		c.Close()
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			t.Fatalf("%q: connection still open after %v", req[:12], time.Since(start))
		}
		if !strings.HasPrefix(string(got), "HTTP/1.1 4") {
			t.Fatalf("%q: answer %q", req[:12], got)
		}
	}
}

// Lead 2: the port-forward listener advertises small HTTP/2 buffers and few
// streams, refuses streams past the limit, and drops anonymous request DATA
// instead of keeping it while a handler waits on a stalled response.
func TestPublicHTTP2Settings(t *testing.T) {
	_, srv := newTestServer(t)
	ts, _ := newPublicTLS(t, srv, anonReadTimeout, anonWriteTimeout)
	p := dialH2(t, ts, nil)
	var got map[uint16]uint32
	var connWindow uint32
	p.until(2*time.Second, func(f h2frame) bool {
		if f.typ == h2Settings && f.flags&h2FlagAck == 0 {
			got = settingsOf(f)
		}
		if f.typ == h2WindowUpdate && f.stream == 0 {
			connWindow = 65535 + binary.BigEndian.Uint32(f.payload)
		}
		return got != nil && connWindow != 0
	})
	want := map[uint16]uint32{
		h2SettingMaxConcurrentStreams: publicStreams,
		h2SettingInitialWindowSize:    publicWindow,
		h2SettingMaxFrameSize:         publicFrameMax,
	}
	for id, v := range want {
		if got[id] != v {
			t.Errorf("setting %#x = %d, want %d (all: %v)", id, got[id], v, got)
		}
	}
	if h := got[h2SettingMaxHeaderListSize]; h == 0 || h > publicHeaderMax+1024 {
		t.Errorf("header list size = %d, want about %d", h, publicHeaderMax)
	}
	if connWindow != publicWindow {
		t.Errorf("connection window = %d, want %d", connWindow, publicWindow)
	}
}

func TestPublicH2StreamLimitAndGoroutines(t *testing.T) {
	_, srv := newTestServer(t)
	const writeTO = 2 * time.Second
	ts, _ := newPublicTLS(t, srv, writeTO, writeTO)
	runtime.GC()
	base := runtime.NumGoroutine()
	const conns = 2
	var peers []*h2peer
	for range conns {
		p := dialH2(t, ts, map[uint16]uint32{h2SettingInitialWindowSize: 0})
		// Wait for the server's settings: its stream limit applies once acknowledged.
		p.until(2*time.Second, func(f h2frame) bool { return f.typ == h2Settings && f.flags&h2FlagAck != 0 })
		for i := uint32(0); i <= publicStreams; i++ {
			p.get(2*i+1, "/api/nope", true)
		}
		peers = append(peers, p)
	}
	over := uint32(2*publicStreams + 1)
	for i, p := range peers {
		if !p.until(writeTO/2, func(f h2frame) bool { return f.typ == h2RSTStream && f.stream == over }) {
			t.Fatalf("conn %d: stream %d past the limit was not refused", i, over)
		}
	}
	// Each stalled stream holds one handler goroutine; each connection a few.
	if g := runtime.NumGoroutine() - base; g > conns*(publicStreams+6) {
		t.Fatalf("%d goroutines for %d stalled connections", g, conns)
	}
	// At the write deadline they are all gone.
	deadline := time.Now().Add(5 * writeTO)
	for runtime.NumGoroutine()-base > conns*6 {
		if time.Now().After(deadline) {
			t.Fatalf("%d goroutines left after the write deadline", runtime.NumGoroutine()-base)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// The memory a stranger holds per stalled stream with the largest headers
// stays near the figure of the publicHTTP2 comment (about 24 KB + 16 KiB).
func TestPublicH2StalledStreamMemory(t *testing.T) {
	if testing.Short() || raceEnabled {
		t.Skip("memory measurement")
	}
	_, srv := newTestServer(t)
	ts, _ := newPublicTLS(t, srv, anonReadTimeout, anonWriteTimeout)
	pad := strings.Repeat("a", publicHeaderMax-1024)
	var heap, stack uint64
	measure := func() uint64 {
		runtime.GC()
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		heap, stack = m.HeapInuse-heap, m.StackInuse-stack
		return m.HeapInuse + m.StackInuse
	}
	before := measure()
	const conns = 4
	for range conns {
		p := dialH2(t, ts, map[uint16]uint32{h2SettingInitialWindowSize: 0})
		for i := uint32(0); i < publicStreams; i++ {
			p.get(2*i+1, "/api/nope", true, "x-pad", pad)
		}
		n := 0
		p.until(5*time.Second, func(f h2frame) bool {
			if f.typ == h2Headers {
				n++
			}
			return n == publicStreams
		})
		if n != publicStreams {
			t.Fatalf("%d of %d streams answered", n, publicStreams)
		}
	}
	per := (measure() - before) / (conns * publicStreams)
	t.Logf("about %d bytes per stalled stream (connections included; heap %d, stacks %d)", per,
		heap/(conns*publicStreams), stack/(conns*publicStreams))
	if per > 64<<10 {
		t.Fatalf("%d bytes per stalled stream, the budget assumes about 40 KB", per)
	}
}

func TestPublicH2AnonymousBodyIsDropped(t *testing.T) {
	_, srv := newTestServer(t)
	srv.Web = fstest.MapFS{
		"index.html":    {Data: []byte("<!doctype html>")},
		"assets/big.js": {Data: []byte(strings.Repeat("x", 64<<10))},
	}
	ts, _ := newPublicTLS(t, srv, anonReadTimeout, anonWriteTimeout)
	p := dialH2(t, ts, map[uint16]uint32{h2SettingInitialWindowSize: 0})
	// Skip the server's first grant of its connection window.
	if !p.until(2*time.Second, func(f h2frame) bool { return f.typ == h2WindowUpdate && f.stream == 0 }) {
		t.Fatal("no initial connection window")
	}
	// The handler stalls writing the asset (no window), and never reads the body.
	p.get(1, "/assets/big.js", false)
	const sent = 20 << 14 // 320 KiB, inside both windows
	for i := 0; i < sent>>14; i++ {
		p.frame(h2Data, 0, 1, make([]byte, 1<<14))
	}
	var refunded uint32
	if !p.until(3*time.Second, func(f h2frame) bool {
		if f.typ == h2WindowUpdate && f.stream == 0 {
			refunded += binary.BigEndian.Uint32(f.payload)
		}
		if f.typ == h2RSTStream && f.stream == 1 {
			t.Fatal("stream reset before the check")
		}
		return refunded >= publicWindow/2
	}) {
		t.Fatalf("only %d of %d bytes of the anonymous body were released", refunded, sent)
	}
}

// closedDoor keeps the anonymous deadlines to the end (never clears them),
// sets them itself only where the server does not, and lifts them for
// signed-in requests only.
func TestClosedDoorDeadlines(t *testing.T) {
	bounded := &http.Server{ReadTimeout: anonReadTimeout, WriteTimeout: anonWriteTimeout}
	cases := []struct {
		name        string
		zone        string
		user        bool
		server      *http.Server
		path        string
		wantSet     bool // non-zero deadlines set
		wantLift    bool // zero deadlines set
		wantHandler bool
	}{
		{"anonymous, private listener reclassified", ZoneProxy, false, nil, "/index.html", true, false, true},
		{"anonymous, closed path", ZoneProxy, false, nil, "/api/cameras", true, false, false},
		{"anonymous, public server bounds it", ZonePublic, false, bounded, "/index.html", false, false, true},
		{"signed in lifts them", ZonePublic, true, bounded, "/api/cameras", false, true, true},
		// A kept-alive connection may carry a write deadline an anonymous
		// public request left: home clears it (net/http does not here).
		{"home clears a stale write deadline", ZoneLAN, false, nil, "/api/cameras", false, true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := &Server{}
			called := false
			h := s.closedDoor(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
			ctx := context.WithValue(context.Background(), zoneKey, c.zone)
			if c.user {
				ctx = context.WithValue(ctx, userKey, &store.User{ID: 1, Role: "viewer"})
			}
			if c.server != nil {
				ctx = context.WithValue(ctx, http.ServerContextKey, c.server)
			}
			w := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
			h.ServeHTTP(w, httptest.NewRequest("GET", c.path, nil).WithContext(ctx))
			set, lift := false, false
			for _, d := range append(w.reads, w.writes...) {
				if d.IsZero() {
					lift = true
				} else {
					set = true
				}
			}
			if set != c.wantSet || lift != c.wantLift || called != c.wantHandler {
				t.Fatalf("set=%v lift=%v handler=%v (reads %v writes %v)", set, lift, called, w.reads, w.writes)
			}
		})
	}
}

type deadlineRecorder struct {
	*httptest.ResponseRecorder
	reads, writes []time.Time
}

func (d *deadlineRecorder) SetReadDeadline(t time.Time) error {
	d.reads = append(d.reads, t)
	return nil
}

func (d *deadlineRecorder) SetWriteDeadline(t time.Time) error {
	d.writes = append(d.writes, t)
	return nil
}

// Anonymous requests carry no body to a handler, except the sign-in forms,
// read in full first (at most anonBodyMax).
func TestClosedDoorAnonymousBodies(t *testing.T) {
	cases := []struct {
		name, method, path string
		size               int
		wantCode           int
		wantBody           int // bytes the handler reads; -1 = handler not called
	}{
		{"SPA GET with a body", "GET", "/index.html", 1000, 200, 0},
		{"closed path", "POST", "/api/cameras", 1000, 404, -1},
		{"sign-in form", "POST", "/api/login", 1000, 200, 1000},
		{"sign-in form at the cap", "POST", "/api/invite/accept", anonBodyMax, 200, anonBodyMax},
		{"sign-in form over the cap", "POST", "/api/login", anonBodyMax + 1, 413, -1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := &Server{}
			read := -1
			h := s.closedDoor(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				read = len(b)
			}))
			body := &closeRecorder{Reader: strings.NewReader(strings.Repeat("x", c.size))}
			r := httptest.NewRequest(c.method, c.path, nil)
			r.Body = body
			r = r.WithContext(context.WithValue(r.Context(), zoneKey, ZonePublic))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)
			if rec.Code != c.wantCode || read != c.wantBody || !body.closed {
				t.Fatalf("code=%d read=%d closed=%v, want %d %d true", rec.Code, read, body.closed, c.wantCode, c.wantBody)
			}
		})
	}
}

type closeRecorder struct {
	io.Reader
	closed bool
}

func (c *closeRecorder) Close() error {
	c.closed = true
	return nil
}

// ConfigurePublicServer's numbers keep the anonymous worst case of the
// publicHTTP2 comment inside its budget.
func TestPublicMemoryBudget(t *testing.T) {
	const (
		connCost   = 40 << 10 // idle HTTP/2 connection (measured)
		streamCost = 24 << 10 // stalled stream without its headers (measured)
		budget     = 80 << 20
	)
	lim := PublicConnLimits()
	worst := lim.Anon * (connCost + publicWindow + publicStreams*(streamCost+publicHeaderMax))
	if worst > budget {
		t.Fatalf("anonymous worst case %d MiB > %d MiB", worst>>20, budget>>20)
	}
	hs := &http.Server{}
	ConfigurePublicServer(hs)
	if hs.ReadTimeout != anonReadTimeout || hs.WriteTimeout != anonWriteTimeout || hs.MaxHeaderBytes != publicHeaderMax ||
		hs.HTTP2 == nil || hs.HTTP2.MaxConcurrentStreams != publicStreams || hs.HTTP2.MaxReceiveBufferPerConnection != publicWindow ||
		hs.HTTP2.MaxReceiveBufferPerStream != publicWindow {
		t.Fatalf("public server = %+v %+v", hs, hs.HTTP2)
	}
	if lim.Anon > lim.Total || lim.PerSource > lim.PerNet {
		t.Fatalf("limits %+v", lim)
	}
}
