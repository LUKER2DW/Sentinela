package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LUKER2DW/sentinela/internal/store"
	"github.com/LUKER2DW/sentinela/internal/sysinfo"
)

// signedInAccount creates a user with a role (and camera list) and signs it in.
func signedInAccount(t *testing.T, ts *httptest.Server, admin *client, name, role, cams string) (*client, int64) {
	t.Helper()
	code, m, body := admin.do("POST", "/api/users", `{"username":"`+name+`","password":"long enough pw","role":"`+role+`","cameras":[`+cams+`]}`)
	if code != 201 {
		t.Fatalf("create %s = %d %s", name, code, body)
	}
	c := newClient(t, ts.URL)
	if code, _, body := c.do("POST", "/api/login", `{"username":"`+name+`","password":"long enough pw"}`); code != 200 {
		t.Fatalf("login %s = %d %s", name, code, body)
	}
	return c, int64(m["id"].(float64))
}

// srcBackend is a go2rtc stand-in that reports the query of every proxied
// WebSocket and holds it open.
func srcBackend(t *testing.T) (*url.URL, <-chan url.Values) {
	t.Helper()
	got := make(chan url.Values, 8)
	be := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- r.URL.Query()
		conn, brw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = brw.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
		_ = brw.Flush()
		buf := make([]byte, 64)
		for {
			if _, err := conn.Read(buf); err != nil {
				return
			}
		}
	}))
	t.Cleanup(be.Close)
	u, _ := url.Parse(be.URL)
	return u, got
}

// openStream redeems a ticket path as c and returns the upgraded connection
// (nil and the status line when the upgrade was refused).
func openStream(t *testing.T, ts *httptest.Server, c *client, path string) (net.Conn, string) {
	t.Helper()
	tsURL, _ := url.Parse(ts.URL)
	conn, err := net.Dial("tcp", tsURL.Host)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	var cookies []string
	for _, ck := range c.hc.Jar.Cookies(tsURL) {
		cookies = append(cookies, ck.Name+"="+ck.Value)
	}
	fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nUser-Agent: test-browser\r\nCookie: %s\r\n\r\n", path, tsURL.Host, strings.Join(cookies, "; "))
	status, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	if !strings.Contains(status, "101") {
		conn.Close()
		return nil, status
	}
	return conn, status
}

func ticket(t *testing.T, c *client, cam, stream string) (int, string) {
	t.Helper()
	code, m, body := c.do("POST", "/api/cameras/"+cam+"/stream-ticket?stream="+stream, "")
	if code != 200 {
		return code, body
	}
	return code, m["url"].(string)
}

// Viewers watch; only operators get the stream that reaches the speaker.
func TestTalkTicketIsOperatorOnly(t *testing.T) {
	ts, srv := newTestServer(t)
	twoCameras(srv)
	srv.Cfg.Cameras[1].Features.Talk = new(false) // cam2: no speaker
	be, got := srcBackend(t)
	srv.go2rtc = be
	admin := newClient(t, ts.URL)
	admin.do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`)
	viewer, _ := signedInAccount(t, ts, admin, "kid", "viewer", "")
	op, _ := signedInAccount(t, ts, admin, "mum", "operator", "")

	if code, body := ticket(t, viewer, "cam1", "talk"); code != http.StatusForbidden {
		t.Fatalf("viewer talk ticket = %d %s", code, body)
	}
	if code, body := ticket(t, op, "cam2", "talk"); code != http.StatusNotFound {
		t.Fatalf("talk ticket for a camera without speaker = %d %s", code, body)
	}
	cases := []struct {
		who          *client
		stream, want string
	}{
		{viewer, "main", "cam1"},
		{viewer, "sub", "cam1_sub"},
		{viewer, "bogus", "cam1"},
		{op, "main", "cam1"},
		{op, "talk", "cam1_talk"},
	}
	for _, tc := range cases {
		code, path := ticket(t, tc.who, "cam1", tc.stream)
		if code != 200 {
			t.Fatalf("%s ticket = %d %s", tc.stream, code, path)
		}
		if conn, status := openStream(t, ts, tc.who, path); conn == nil {
			t.Fatalf("%s upgrade = %s", tc.stream, status)
		}
		select {
		case q := <-got:
			if q.Get("src") != tc.want || len(q) != 1 {
				t.Errorf("%s ticket opened go2rtc %v, want src=%s only", tc.stream, q, tc.want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s: go2rtc not reached", tc.stream)
		}
	}
}

// A talk ticket of an operator demoted before redeeming it opens nothing.
func TestTalkTicketNeedsOperatorAtRedeem(t *testing.T) {
	ts, srv := newTestServer(t)
	be, got := srcBackend(t)
	srv.go2rtc = be
	admin := newClient(t, ts.URL)
	admin.do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`)
	op, opID := signedInAccount(t, ts, admin, "mum", "operator", "")
	code, path := ticket(t, op, "cam1", "talk")
	if code != 200 {
		t.Fatalf("ticket = %d %s", code, path)
	}
	if code, _, body := admin.do("PATCH", fmt.Sprintf("/api/users/%d", opID), `{"role":"viewer"}`); code != 200 {
		t.Fatalf("demote = %d %s", code, body)
	}
	if conn, status := openStream(t, ts, op, path); conn != nil || !strings.Contains(status, "403") {
		t.Fatalf("demoted talk ticket = %q", status)
	}
	select {
	case q := <-got:
		t.Fatalf("go2rtc reached with %v", q)
	default:
	}
}

// fakeGo2RTC stands in for go2rtc's API: /api/ws adds a consumer named by
// the request's User-Agent (kept after the WebSocket closes when keep is
// set, like a WebRTC PeerConnection; dropped with it otherwise, like MSE),
// /api/streams lists them and /api/restart drops them all.
type fakeGo2RTC struct {
	mu        sync.Mutex
	keep      bool
	stuck     bool // a restart does not take (consumers stay)
	consumers map[string]bool
	restarts  int
	exits     int
	opened    chan string
}

func newFakeGo2RTC(t *testing.T, keep bool) (*fakeGo2RTC, *url.URL) {
	t.Helper()
	f := &fakeGo2RTC{keep: keep, consumers: map[string]bool{}, opened: make(chan string, 8)}
	be := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/streams":
			f.mu.Lock()
			var cons []map[string]any
			for ua := range f.consumers {
				cons = append(cons, map[string]any{"id": 1, "format_name": "webrtc", "user_agent": ua})
			}
			f.mu.Unlock()
			// Untagged consumers (the detector, go2rtc's own restream) are ignored.
			cons = append(cons, map[string]any{"user_agent": "Lavf/61"})
			_ = json.NewEncoder(w).Encode(map[string]any{"cam1": map[string]any{"consumers": cons}, "cam1_sub": map[string]any{}})
		case "/api/restart", "/api/exit":
			f.mu.Lock()
			if r.URL.Path == "/api/restart" {
				f.restarts++
			} else {
				f.exits++
			}
			if !f.stuck || r.URL.Path != "/api/restart" {
				f.consumers = map[string]bool{}
			}
			f.mu.Unlock()
		case "/api/ws":
			ua := r.UserAgent()
			f.mu.Lock()
			f.consumers[ua] = true
			f.mu.Unlock()
			f.opened <- ua
			conn, brw, err := w.(http.Hijacker).Hijack()
			if err != nil {
				return
			}
			defer conn.Close()
			_, _ = brw.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
			_ = brw.Flush()
			buf := make([]byte, 64)
			for {
				if _, err := conn.Read(buf); err != nil {
					break
				}
			}
			if !f.keep {
				f.mu.Lock()
				delete(f.consumers, ua)
				f.mu.Unlock()
			}
		}
	}))
	t.Cleanup(be.Close)
	u, _ := url.Parse(be.URL)
	return f, u
}

func (f *fakeGo2RTC) counts() (consumers, restarts, exits int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.consumers), f.restarts, f.exits
}

func (f *fakeGo2RTC) add(tag string) {
	f.mu.Lock()
	f.consumers[liveTagPrefix+tag] = true
	f.mu.Unlock()
}

// liveTimings sets the guard's timings for one test.
func liveTimings(t *testing.T, settle, cutGap, retryGap time.Duration) {
	t.Helper()
	oldSettle, oldLinger, oldCut, oldRetry, oldClose := liveSettle, liveLinger, liveCutGap, liveRetryGap, liveCloseSweep
	liveSettle, liveLinger, liveCutGap, liveRetryGap, liveCloseSweep = settle, time.Hour, cutGap, retryGap, 50*time.Millisecond
	t.Cleanup(func() {
		liveSettle, liveLinger, liveCutGap, liveRetryGap, liveCloseSweep = oldSettle, oldLinger, oldCut, oldRetry, oldClose
	})
}

// fastLiveGuard shortens the guard's timings and runs it for the test.
func fastLiveGuard(t *testing.T, srv *Server) {
	t.Helper()
	liveTimings(t, 150*time.Millisecond, time.Hour, time.Hour)
	runLiveGuard(t, srv)
}

// runLiveGuard runs the guard for the test.
func runLiveGuard(t *testing.T, srv *Server) {
	t.Helper()
	shortRecheck(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { srv.GuardLiveMedia(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		<-done
	})
}

// liveViewer signs a viewer in, opens one stream, and returns the open
// connection with the tag go2rtc saw.
func liveViewer(t *testing.T, ts *httptest.Server, f *fakeGo2RTC) (*client, *client, int64, net.Conn, string) {
	t.Helper()
	admin := newClient(t, ts.URL)
	admin.do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`)
	kid, kidID := signedInAccount(t, ts, admin, "kid", "viewer", "")
	code, path := ticket(t, kid, "cam1", "main")
	if code != 200 {
		t.Fatalf("ticket = %d %s", code, path)
	}
	conn, status := openStream(t, ts, kid, path)
	if conn == nil {
		t.Fatalf("upgrade = %s", status)
	}
	var ua string
	select {
	case ua = <-f.opened:
	case <-time.After(2 * time.Second):
		t.Fatal("go2rtc not reached")
	}
	if !strings.HasPrefix(ua, liveTagPrefix) || strings.Contains(ua, "test-browser") {
		t.Fatalf("go2rtc saw User-Agent %q, want the stream's tag only", ua)
	}
	return admin, kid, kidID, conn, ua
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal(what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A client that closes the signalling WebSocket but keeps its WebRTC media
// loses it when the account is disabled: go2rtc is restarted.
func TestRevokedWebRTCMediaIsCut(t *testing.T) {
	ts, srv := newTestServer(t)
	f, u := newFakeGo2RTC(t, true)
	srv.go2rtc = u
	fastLiveGuard(t, srv)
	admin, _, kidID, conn, _ := liveViewer(t, ts, f)
	conn.Close() // the WebSocket goes, the PeerConnection stays
	time.Sleep(300 * time.Millisecond)
	if n, restarts, _ := f.counts(); n != 1 || restarts != 0 {
		t.Fatalf("before revocation: %d consumers, %d restarts", n, restarts)
	}
	if code, _, body := admin.do("PATCH", fmt.Sprintf("/api/users/%d", kidID), `{"disabled":true}`); code != 200 {
		t.Fatalf("disable = %d %s", code, body)
	}
	waitFor(t, "media of a disabled account kept flowing", func() bool {
		n, restarts, _ := f.counts()
		return n == 0 && restarts == 1
	})
}

// Narrowing the cameras cuts the media of the camera taken away.
func TestNarrowedCameraMediaIsCut(t *testing.T) {
	ts, srv := newTestServer(t)
	twoCameras(srv)
	f, u := newFakeGo2RTC(t, true)
	srv.go2rtc = u
	fastLiveGuard(t, srv)
	admin, _, kidID, _, _ := liveViewer(t, ts, f)
	if code, _, body := admin.do("PATCH", fmt.Sprintf("/api/users/%d", kidID), `{"cameras":["cam2"]}`); code != 200 {
		t.Fatalf("narrow = %d %s", code, body)
	}
	waitFor(t, "media of a camera taken away kept flowing", func() bool {
		_, restarts, _ := f.counts()
		return restarts == 1
	})
}

// Media of an account that is still allowed survives its WebSocket, and a
// revoked stream whose consumer goes away by itself costs no restart.
func TestLiveGuardLeavesAllowedAndClosingStreams(t *testing.T) {
	t.Run("allowed", func(t *testing.T) {
		ts, srv := newTestServer(t)
		f, u := newFakeGo2RTC(t, true)
		srv.go2rtc = u
		fastLiveGuard(t, srv)
		_, _, _, conn, _ := liveViewer(t, ts, f)
		conn.Close()
		srv.LiveMediaChanged()
		time.Sleep(600 * time.Millisecond)
		if n, restarts, exits := f.counts(); n != 1 || restarts+exits != 0 {
			t.Fatalf("allowed stream: %d consumers, %d restarts, %d exits", n, restarts, exits)
		}
	})
	t.Run("closing", func(t *testing.T) {
		ts, srv := newTestServer(t)
		f, u := newFakeGo2RTC(t, false)
		srv.go2rtc = u
		fastLiveGuard(t, srv)
		admin, _, kidID, _, _ := liveViewer(t, ts, f)
		if code, _, body := admin.do("PATCH", fmt.Sprintf("/api/users/%d", kidID), `{"disabled":true}`); code != 200 {
			t.Fatalf("disable = %d %s", code, body)
		}
		waitFor(t, "WebSocket of a disabled account stayed open", func() bool { n, _, _ := f.counts(); return n == 0 })
		time.Sleep(600 * time.Millisecond)
		if _, restarts, exits := f.counts(); restarts+exits != 0 {
			t.Fatalf("closed by itself, yet go2rtc restarted %d / exited %d", restarts, exits)
		}
	})
}

// A tagged consumer this process does not know (left from before an API
// restart) is cut as well.
func TestUnknownLiveConsumerIsCut(t *testing.T) {
	_, srv := newTestServer(t)
	f, u := newFakeGo2RTC(t, true)
	srv.go2rtc = u
	f.consumers[liveTagPrefix+"0123456789abcdef01234567"] = true
	fastLiveGuard(t, srv)
	waitFor(t, "an unknown consumer was left running", func() bool {
		_, restarts, _ := f.counts()
		return restarts == 1
	})
}

// fakePresets records preset calls instead of moving a camera.
type fakePresets struct {
	mu    sync.Mutex
	calls []string
}

func (f *fakePresets) Start(context.Context, string, string, int) error { return nil }
func (f *fakePresets) Stop(context.Context) error                       { return nil }
func (f *fakePresets) Nudge(context.Context, string, string, int, time.Duration) error {
	return nil
}
func (f *fakePresets) Preset(_ context.Context, action string, id int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fmt.Sprintf("%s:%d", action, id))
	return nil
}

// Operators go to saved positions; saving and deleting them is for admins
// (CAMERAS.md), on the server and not only in the dashboard.
func TestPresetSetClearAdminOnly(t *testing.T) {
	ts, srv := newTestServer(t)
	drv := &fakePresets{}
	srv.ptzDrv["cam1"] = drv
	admin := newClient(t, ts.URL)
	admin.do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`)
	op, _ := signedInAccount(t, ts, admin, "mum", "operator", "")
	cases := []struct {
		name   string
		who    *client
		action string
		code   int
		call   string
	}{
		{"operator goto", op, "goto", 200, "goto:3"},
		{"operator set", op, "set", 403, ""},
		{"operator clear", op, "clear", 403, ""},
		{"operator other", op, "reboot", 400, ""},
		{"admin set", admin, "set", 200, "set:3"},
		{"admin clear", admin, "clear", 200, "clear:3"},
	}
	for _, tc := range cases {
		drv.mu.Lock()
		drv.calls = nil
		drv.mu.Unlock()
		code, _, body := tc.who.do("POST", "/api/cameras/cam1/presets/3", `{"action":"`+tc.action+`"}`)
		drv.mu.Lock()
		calls := strings.Join(drv.calls, ",")
		drv.mu.Unlock()
		if code != tc.code || calls != tc.call {
			t.Errorf("%s: %d %s, camera got %q; want %d and %q", tc.name, code, body, calls, tc.code, tc.call)
		}
	}
}

// Protecting recordings stops at a share of the recordings disk per camera
// and in total, so protection can neither fill the disk nor push the other
// cameras' recordings out.
func TestProtectionIsCapped(t *testing.T) {
	ts, srv := newTestServer(t)
	twoCameras(srv)
	srv.diskUsage = func(string) (sysinfo.Disk, error) { return sysinfo.Disk{Total: 10000, Pct: 50}, nil }
	ctx := context.Background()
	start := time.Now().UTC().Add(-time.Hour)
	ids := map[string][]int64{}
	for _, cam := range []string{"cam1", "cam2"} {
		for i := 0; i < 4; i++ {
			s := start.Add(time.Duration(i) * time.Minute)
			p := fmt.Sprintf("%s/%d.mp4", cam, i)
			if err := srv.Store.UpsertSegment(ctx, store.Segment{CameraID: cam, Start: s, End: s.Add(time.Minute), Path: p, Bytes: 200}); err != nil {
				t.Fatal(err)
			}
			segs, _ := srv.Store.SegmentsBetween(ctx, cam, s, s.Add(time.Second))
			ids[cam] = append(ids[cam], segs[len(segs)-1].ID)
		}
	}
	admin := newClient(t, ts.URL)
	admin.do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`)
	op, _ := signedInAccount(t, ts, admin, "mum", "operator", "")
	protect := func(id int64, on bool) int {
		code, _, _ := op.do("PUT", fmt.Sprintf("/api/recordings/%d/protected", id), fmt.Sprintf(`{"protected":%v}`, on))
		return code
	}
	// Defaults: 5 % of the disk per camera (500 B), 10 % in total (1000 B).
	steps := []struct {
		cam  string
		i    int
		on   bool
		want int
	}{
		{"cam1", 0, true, 200}, {"cam1", 1, true, 200},
		{"cam1", 2, true, http.StatusConflict}, // camera cap
		{"cam1", 1, true, 200},                 // already protected: no change
		{"cam2", 0, true, 200}, {"cam2", 1, true, 200},
		{"cam2", 2, true, http.StatusConflict},
		{"cam1", 0, false, 200}, {"cam1", 2, true, 200}, // room again
	}
	for n, st := range steps {
		if got := protect(ids[st.cam][st.i], st.on); got != st.want {
			t.Fatalf("step %d (%s #%d %v) = %d, want %d", n, st.cam, st.i, st.on, got, st.want)
		}
	}
	// A larger camera share still stops at the total.
	srv.Cfg.Recording.MaxProtectedCameraPercent = 10
	if got := protect(ids["cam1"][3], true); got != 200 {
		t.Fatalf("up to the total = %d, want 200", got)
	}
	if got := protect(ids["cam2"][3], true); got != http.StatusConflict {
		t.Fatalf("over the total = %d, want 409", got)
	}
	cam, total, err := srv.Store.ProtectedBytes(ctx, "cam1")
	if err != nil || cam != 600 || total != 1000 {
		t.Fatalf("protected bytes = %d/%d %v, want 600/1000", cam, total, err)
	}
	// cam2 is removed: its protected clips no longer count against the
	// total (nobody can unprotect them; retention ages them out), and its
	// recordings cannot be protected any more.
	srv.Cfg.Cameras = srv.Cfg.Cameras[:1]
	if got := protect(ids["cam1"][2], false); got != 200 {
		t.Fatalf("unprotect = %d", got)
	}
	srv.Cfg.Recording.MaxProtectedPercent, srv.Cfg.Recording.MaxProtectedCameraPercent = 7, 7
	if got := protect(ids["cam1"][2], true); got != 200 {
		t.Fatalf("with a removed camera's 400 B protected, 600 B of 700 = %d, want 200", got)
	}
	if got := protect(ids["cam2"][2], true); got != http.StatusConflict {
		t.Fatalf("protecting a removed camera's recording = %d, want 409", got)
	}
	// No disk reading: refuse rather than protect blindly.
	srv.diskUsage = func(string) (sysinfo.Disk, error) { return sysinfo.Disk{}, errors.New("no disk") }
	if got := protect(ids["cam2"][3], true); got != http.StatusServiceUnavailable {
		t.Fatalf("without a disk reading = %d, want 503", got)
	}
}
