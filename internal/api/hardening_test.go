package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/LUKER2DW/sentinela/internal/store"
)

// twoCameras adds a second camera to the test config.
func twoCameras(srv *Server) {
	srv.Cfg.Cameras = append(srv.Cfg.Cameras, srv.Cfg.Cameras[0])
	srv.Cfg.Cameras[1].ID, srv.Cfg.Cameras[1].Name = "cam2", "Fundo"
}

// shortRecheck makes open streams re-read their session quickly.
func shortRecheck(t *testing.T) {
	t.Helper()
	old := streamRecheckEvery
	streamRecheckEvery = 50 * time.Millisecond
	t.Cleanup(func() { streamRecheckEvery = old })
}

// restrictedViewer creates a viewer limited to cams and signs it in.
func restrictedViewer(t *testing.T, ts *httptest.Server, admin *client, cams string) (*client, int64) {
	t.Helper()
	code, m, body := admin.do("POST", "/api/users", `{"username":"kid","password":"viewer password","role":"viewer","cameras":[`+cams+`]}`)
	if code != 201 {
		t.Fatalf("create = %d %s", code, body)
	}
	kid := newClient(t, ts.URL)
	if code, _, body := kid.do("POST", "/api/login", `{"username":"kid","password":"viewer password"}`); code != 200 {
		t.Fatalf("login = %d %s", code, body)
	}
	return kid, int64(m["id"].(float64))
}

func TestSystemOnlyShowsVisibleCameras(t *testing.T) {
	ts, srv := newTestServer(t)
	twoCameras(srv)
	ctx := context.Background()
	now := time.Now().UTC()
	for _, id := range []string{"cam1", "cam2"} {
		if err := srv.Store.UpsertSegment(ctx, store.Segment{CameraID: id, Start: now.Add(-2 * time.Hour), End: now.Add(-time.Hour), Path: id + "/a.mp4", Bytes: 100}); err != nil {
			t.Fatal(err)
		}
		if err := srv.Store.PutRecorderStatus(ctx, store.RecorderStatus{CameraID: id, State: "recording", Since: now, Heartbeat: now, CurrentFile: id + "/secret-name.mp4"}); err != nil {
			t.Fatal(err)
		}
		srv.NoteEventWatch(id, false, "onvif "+id)
	}
	srv.det.mu.Lock()
	srv.det.status, srv.det.statusAt = json.RawMessage(`{"model":"m","inference_ms":3,"cameras":{"cam1":{"analysing":true},"cam2":{"analysing":false}}}`), time.Now()
	srv.det.mu.Unlock()

	admin := newClient(t, ts.URL)
	admin.do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`)
	kid, _ := restrictedViewer(t, ts, admin, `"cam2"`)

	_, _, all := admin.do("GET", "/api/system", "")
	for _, want := range []string{`"cam1"`, `"cam2"`} {
		if strings.Count(all, want) < 4 {
			t.Errorf("admin /api/system lacks %s: %s", want, all)
		}
	}
	code, m, body := kid.do("GET", "/api/system", "")
	if code != 200 {
		t.Fatalf("kid system = %d %s", code, body)
	}
	if strings.Contains(body, "cam1") {
		t.Errorf("restricted viewer sees a hidden camera: %s", body)
	}
	if !strings.Contains(body, "cam2") {
		t.Errorf("restricted viewer lost its own camera: %s", body)
	}
	// Host and volume data stays.
	for _, k := range []string{"host", "disk", "volumes", "version"} {
		if _, ok := m[k]; !ok {
			t.Errorf("system lost %q for a restricted viewer", k)
		}
	}
	det := m["detector"].(map[string]any)["status"].(map[string]any)
	if det["model"] != "m" || len(det["cameras"].(map[string]any)) != 1 {
		t.Errorf("detector status = %v", det)
	}
}

func TestCanSeeStream(t *testing.T) {
	ts, srv := newTestServer(t)
	twoCameras(srv)
	admin := newClient(t, ts.URL)
	admin.do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`)
	for _, tt := range []struct {
		cams   []string
		role   string
		stream string
		want   bool
	}{
		{nil, "viewer", "cam1_sub", true},
		{nil, "viewer", "other", true},
		{[]string{"cam2"}, "viewer", "cam2", true},
		{[]string{"cam2"}, "viewer", "cam2_sub", true},
		{[]string{"cam2"}, "viewer", "cam1", false},
		{[]string{"cam2"}, "viewer", "cam1_sub", false},
		{[]string{"cam2"}, "viewer", "other", false},
		{[]string{"cam2"}, "admin", "cam1", true},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r = r.WithContext(context.WithValue(r.Context(), userKey, &store.User{Role: tt.role, Cameras: tt.cams}))
		if got := srv.canSeeStream(r, tt.stream); got != tt.want {
			t.Errorf("%v %s sees %q = %v, want %v", tt.cams, tt.role, tt.stream, got, tt.want)
		}
	}
}

// sseLines reads an SSE body into a channel; it closes when the body ends.
func sseLines(body *bufio.Reader) <-chan string {
	ch := make(chan string, 64)
	go func() {
		defer close(ch)
		for {
			line, err := body.ReadString('\n')
			if err != nil {
				return
			}
			ch <- strings.TrimSpace(line)
		}
	}()
	return ch
}

func waitLine(t *testing.T, ch <-chan string, prefix string, d time.Duration) bool {
	t.Helper()
	deadline := time.After(d)
	for {
		select {
		case l, ok := <-ch:
			if !ok {
				return false
			}
			if strings.HasPrefix(l, prefix) {
				return true
			}
		case <-deadline:
			return false
		}
	}
}

func TestEventStreamFollowsSessionAndCameras(t *testing.T) {
	shortRecheck(t)
	ts, srv := newTestServer(t)
	twoCameras(srv)
	admin := newClient(t, ts.URL)
	admin.do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`)
	kid, kidID := restrictedViewer(t, ts, admin, `"cam1","cam2"`)

	resp, err := kid.hc.Get(ts.URL + "/api/events/stream")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("stream: %v %v", err, resp)
	}
	defer resp.Body.Close()
	lines := sseLines(bufio.NewReader(resp.Body))
	if !waitLine(t, lines, "retry:", 2*time.Second) {
		t.Fatal("no stream greeting")
	}
	srv.hub.Publish(LiveEvent{CameraID: "cam1", Type: "motion", Active: true, At: time.Now()})
	if !waitLine(t, lines, "data:", 2*time.Second) {
		t.Fatal("event of a visible camera not delivered")
	}

	// Narrowing the account's cameras applies to the open stream.
	if code, _, body := admin.do("PATCH", fmt.Sprintf("/api/users/%d", kidID), `{"cameras":["cam2"]}`); code != 200 {
		t.Fatalf("patch = %d %s", code, body)
	}
	time.Sleep(300 * time.Millisecond)
	srv.hub.Publish(LiveEvent{CameraID: "cam1", Type: "motion", Active: true, At: time.Now()})
	if waitLine(t, lines, "data:", 400*time.Millisecond) {
		t.Fatal("event of a camera taken away was still delivered")
	}
	srv.hub.Publish(LiveEvent{CameraID: "cam2", Type: "motion", Active: true, At: time.Now()})
	if !waitLine(t, lines, "data:", 2*time.Second) {
		t.Fatal("event of the remaining camera not delivered")
	}

	// A disabled account's stream ends.
	if code, _, body := admin.do("PATCH", fmt.Sprintf("/api/users/%d", kidID), `{"disabled":true}`); code != 200 {
		t.Fatalf("disable = %d %s", code, body)
	}
	if waitLine(t, lines, "never", 3*time.Second) {
		t.Fatal("unexpected line")
	}
	select {
	case _, ok := <-lines:
		if ok {
			t.Fatal("stream of a disabled account kept sending")
		}
	default:
		t.Fatal("stream of a disabled account is still open")
	}
}

// upgradeBackend is a go2rtc stand-in: it accepts a WebSocket-style upgrade and
// then holds the connection until the proxy closes it.
func upgradeBackend(t *testing.T) (*url.URL, <-chan struct{}) {
	t.Helper()
	closed := make(chan struct{})
	be := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
				close(closed)
				return
			}
		}
	}))
	t.Cleanup(be.Close)
	u, _ := url.Parse(be.URL)
	return u, closed
}

func TestStreamWSClosesWhenSessionEnds(t *testing.T) {
	shortRecheck(t)
	ts, srv := newTestServer(t)
	twoCameras(srv)
	be, closed := upgradeBackend(t)
	srv.go2rtc = be
	admin := newClient(t, ts.URL)
	admin.do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`)
	kid, kidID := restrictedViewer(t, ts, admin, `"cam1"`)

	_, m, body := kid.do("POST", "/api/cameras/cam1/stream-ticket", "")
	path, _ := m["url"].(string)
	if path == "" {
		t.Fatalf("ticket = %s", body)
	}
	tsURL, _ := url.Parse(ts.URL)
	conn, err := net.Dial("tcp", tsURL.Host)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var cookies []string
	for _, c := range kid.hc.Jar.Cookies(tsURL) {
		cookies = append(cookies, c.Name+"="+c.Value)
	}
	fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nCookie: %s\r\n\r\n", path, tsURL.Host, strings.Join(cookies, "; "))
	rd := bufio.NewReader(conn)
	status, err := rd.ReadString('\n')
	if err != nil || !strings.Contains(status, "101") {
		t.Fatalf("upgrade = %q %v", status, err)
	}
	select {
	case <-closed:
		t.Fatal("stream closed before the account changed")
	case <-time.After(300 * time.Millisecond):
	}
	if code, _, body := admin.do("PATCH", fmt.Sprintf("/api/users/%d", kidID), `{"disabled":true}`); code != 200 {
		t.Fatalf("disable = %d %s", code, body)
	}
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("live video stayed open after the account was disabled")
	}
}

func TestStreamWSClosesWhenCameraTakenAway(t *testing.T) {
	shortRecheck(t)
	ts, srv := newTestServer(t)
	twoCameras(srv)
	be, closed := upgradeBackend(t)
	srv.go2rtc = be
	admin := newClient(t, ts.URL)
	admin.do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`)
	kid, kidID := restrictedViewer(t, ts, admin, `"cam1"`)
	_, m, _ := kid.do("POST", "/api/cameras/cam1/stream-ticket", "")
	path, _ := m["url"].(string)
	tsURL, _ := url.Parse(ts.URL)
	conn, err := net.Dial("tcp", tsURL.Host)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var cookies []string
	for _, c := range kid.hc.Jar.Cookies(tsURL) {
		cookies = append(cookies, c.Name+"="+c.Value)
	}
	fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nCookie: %s\r\n\r\n", path, tsURL.Host, strings.Join(cookies, "; "))
	if status, err := bufio.NewReader(conn).ReadString('\n'); err != nil || !strings.Contains(status, "101") {
		t.Fatalf("upgrade = %q %v", status, err)
	}
	if code, _, body := admin.do("PATCH", fmt.Sprintf("/api/users/%d", kidID), `{"cameras":["cam2"]}`); code != 200 {
		t.Fatalf("patch = %d %s", code, body)
	}
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("live video stayed open after the camera was taken away")
	}
}

func TestIdleWriterReleasesStalledClient(t *testing.T) {
	done := make(chan error, 1)
	canceled := make(chan struct{}, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		iw := newIdleWriter(w, 300*time.Millisecond, func() {
			select {
			case canceled <- struct{}{}:
			default:
			}
		})
		defer iw.Close()
		chunk := make([]byte, 256<<10)
		for {
			if _, err := iw.Write(chunk); err != nil {
				done <- err
				return
			}
		}
	}))
	defer ts.Close()
	resp, err := http.Get(ts.URL) // headers arrive; the body is never read
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("write did not fail")
		}
	case <-time.After(20 * time.Second):
		t.Fatal("a client that stopped reading held the response for ever")
	}
	select {
	case <-canceled:
	default:
		t.Fatal("command was not cancelled on the failed write")
	}
}

func TestTimelapsePrepareIsBounded(t *testing.T) {
	ts, srv := newTestServer(t)
	admin := newClient(t, ts.URL)
	admin.do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`)
	t0 := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	query := func(sec int) string {
		from := t0.Add(time.Duration(sec) * time.Second)
		return "/api/cameras/cam1/timelapse/prepare?from=" + from.Format(time.RFC3339) + "&to=" + from.Add(time.Hour).Format(time.RFC3339) + "&every=60&fps=10"
	}
	srv.tl.mu.Lock()
	srv.tl.jobs = map[string]*tlJob{}
	for i := 0; i < timelapseMaxActiveUser; i++ {
		srv.tl.jobs[fmt.Sprint("own", i)] = &tlJob{State: "queued", user: 1}
	}
	srv.tl.mu.Unlock()
	if code, _, body := admin.do("GET", query(1), ""); code != http.StatusTooManyRequests {
		t.Fatalf("per-user cap = %d %s", code, body)
	}

	// Global cap: other users' jobs fill the queue.
	srv.tl.mu.Lock()
	srv.tl.jobs = map[string]*tlJob{}
	for i := 0; i < timelapseMaxActive; i++ {
		srv.tl.jobs[fmt.Sprint("other", i)] = &tlJob{State: "running", user: int64(100 + i)}
	}
	srv.tl.mu.Unlock()
	if code, _, body := admin.do("GET", query(2), ""); code != http.StatusTooManyRequests {
		t.Fatalf("global cap = %d %s", code, body)
	}

	// A job already in the queue is reported, not refused.
	p := tlParams{cam: &srv.Cfg.Cameras[0], from: t0.Add(3 * time.Second), to: t0.Add(3*time.Second + time.Hour), every: time.Minute, fps: 10}
	srv.tl.mu.Lock()
	srv.tl.jobs[p.key()] = &tlJob{State: "running", user: 100}
	srv.tl.mu.Unlock()
	if code, m, body := admin.do("GET", query(3), ""); code != 200 || m["state"] != "running" {
		t.Fatalf("existing job = %d %s", code, body)
	}
}

func TestTimelapseJobsPrunedSoon(t *testing.T) {
	var jobs tlJobs
	jobs.jobs = map[string]*tlJob{}
	jobs.jobs["a"] = &tlJob{State: "queued"}
	jobs.jobs["b"] = &tlJob{State: "running"}
	jobs.jobs["c"] = &tlJob{State: "failed"}
	jobs.update("c", func(*tlJob) {}) // stamps the end time
	jobs.jobs["c"].ended = time.Now().Add(-time.Hour)
	jobs.jobs["d"] = &tlJob{State: "ready", ended: time.Now()}
	jobs.mu.Lock()
	jobs.pruneLocked(timelapseJobTTL)
	jobs.mu.Unlock()
	if _, ok := jobs.jobs["c"]; ok {
		t.Error("old finished job kept")
	}
	for _, k := range []string{"a", "b", "d"} {
		if _, ok := jobs.jobs[k]; !ok {
			t.Errorf("job %s dropped", k)
		}
	}
	jobs.update("a", func(j *tlJob) { j.State = "failed" })
	if jobs.jobs["a"].ended.IsZero() {
		t.Error("end time not recorded")
	}
}

func TestPushSubscriptionEndsWithItsSession(t *testing.T) {
	ts, srv := newTestServer(t)
	admin := newClient(t, ts.URL)
	admin.do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`)
	phone := newClient(t, ts.URL)
	phone.do("POST", "/api/login", `{"username":"admin","password":"long enough pw"}`)
	for _, c := range []*client{admin, phone} {
		ep := "https://fcm.googleapis.com/fcm/send/" + map[bool]string{true: "admin", false: "phone"}[c == admin]
		if code, _, body := c.do("POST", "/api/push/subscribe", `{"endpoint":"`+ep+`","keys":{"p256dh":"a","auth":"b"}}`); code != 200 {
			t.Fatalf("subscribe = %d %s", code, body)
		}
	}
	if subs, _ := srv.Store.PushSubs(context.Background(), 1); len(subs) != 2 {
		t.Fatalf("subs = %d", len(subs))
	}
	// Signing the phone out removes its subscription only.
	if code, _, _ := phone.do("POST", "/api/logout", ""); code != 200 {
		t.Fatalf("logout = %d", code)
	}
	subs, _ := srv.Store.PushSubs(context.Background(), 1)
	if len(subs) != 1 || !strings.HasSuffix(subs[0].Endpoint, "/admin") {
		t.Fatalf("after logout subs = %+v", subs)
	}
}
