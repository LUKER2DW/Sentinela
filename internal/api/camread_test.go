package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LUKER2DW/sentinela/internal/camera/onvif"
)

func TestCamReads(t *testing.T) {
	p := readPolicy{maxAge: 200 * time.Millisecond, errAge: 50 * time.Millisecond, timeout: time.Second}
	var c camReads[int]
	var calls atomic.Int32
	slow := func(ctx context.Context) (int, error) {
		n := calls.Add(1)
		time.Sleep(50 * time.Millisecond)
		return int(n), nil
	}

	// Concurrent callers share one read.
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if v, err := c.get(context.Background(), "cam1", p, slow); v != 1 || err != nil {
				t.Errorf("got %d %v", v, err)
			}
		}()
	}
	wg.Wait()
	if n := calls.Load(); n != 1 {
		t.Fatalf("reads = %d, want 1", n)
	}
	// Fresh enough: reused. Another camera: its own read.
	if v, _ := c.get(context.Background(), "cam1", p, slow); v != 1 {
		t.Fatalf("cached = %d", v)
	}
	if v, _ := c.get(context.Background(), "cam2", p, slow); v != 2 {
		t.Fatalf("cam2 = %d", v)
	}
	// A shorter maxAge (an operator's ?fresh) reads again.
	time.Sleep(60 * time.Millisecond)
	short := p
	short.maxAge = 50 * time.Millisecond
	if v, _ := c.get(context.Background(), "cam1", short, slow); v != 3 {
		t.Fatalf("fresh = %d", v)
	}

	// A caller that goes away stops waiting; the read goes on and its
	// answer serves the next caller without another read.
	long := readPolicy{maxAge: time.Minute, errAge: time.Minute, timeout: time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	c.invalidate("cam1")
	if _, err := c.get(ctx, "cam1", long, slow); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("gone caller: %v", err)
	}
	waitFlight(t, &c, "cam1", false)
	if v, _ := c.get(context.Background(), "cam1", long, slow); v != 4 || calls.Load() != 4 {
		t.Fatalf("after a gone caller: %d (reads %d)", v, calls.Load())
	}

	// A failure is reused for errAge only.
	var fails atomic.Int32
	bad := func(context.Context) (int, error) { fails.Add(1); return 0, errors.New("down") }
	for range 5 {
		if _, err := c.get(context.Background(), "cam3", p, bad); err == nil {
			t.Fatal("want the error")
		}
	}
	if fails.Load() != 1 {
		t.Fatalf("failing reads = %d, want 1", fails.Load())
	}
	time.Sleep(60 * time.Millisecond)
	_, _ = c.get(context.Background(), "cam3", p, bad)
	if fails.Load() != 2 {
		t.Fatalf("failing reads after errAge = %d, want 2", fails.Load())
	}

	// Invalidated while running: the waiters get it, nobody keeps it.
	release := make(chan struct{})
	gated := func(context.Context) (int, error) { <-release; return int(calls.Add(1)), nil }
	done := make(chan int)
	go func() { v, _ := c.get(context.Background(), "cam4", long, gated); done <- v }()
	waitFlight(t, &c, "cam4", true)
	c.invalidate("cam4")
	close(release)
	first := <-done
	if v, _ := c.get(context.Background(), "cam4", long, slow); v == first {
		t.Fatalf("an invalidated answer was kept (%d)", v)
	}
}

// waitFlight waits until a read of key is running (running) or none is.
func waitFlight(t *testing.T, c *camReads[int], key string, running bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		c.mu.Lock()
		e := c.m[key]
		got := e != nil && e.flight != nil
		c.mu.Unlock()
		if got == running {
			return
		}
	}
	t.Fatalf("%s: read running never became %v", key, running)
}

// onvifStub answers GetSystemDateAndTime and GetPresets after a delay and
// counts requests per operation, and the most in flight at once.
type onvifStub struct {
	delay              time.Duration
	mu                 sync.Mutex
	ops                map[string]int
	inFlight, maxGoing int
}

func (o *onvifStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	o.mu.Lock()
	o.inFlight++
	o.maxGoing = max(o.maxGoing, o.inFlight)
	for _, op := range []string{"GetSystemDateAndTime", "GetCapabilities", "GetProfiles", "GetPresets"} {
		if strings.Contains(string(body), "<"+op) {
			o.ops[op]++
		}
	}
	o.mu.Unlock()
	time.Sleep(o.delay)
	o.mu.Lock()
	o.inFlight--
	o.mu.Unlock()
	reply := func(s string) {
		_, _ = io.WriteString(w, `<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Body>`+s+`</s:Body></s:Envelope>`)
	}
	switch {
	case strings.Contains(string(body), "<GetSystemDateAndTime"):
		reply(`<GetSystemDateAndTimeResponse><SystemDateAndTime><UTCDateTime><Date><Year>2026</Year><Month>11</Month><Day>26</Day></Date><Time><Hour>1</Hour><Minute>2</Minute><Second>3</Second></Time></UTCDateTime></SystemDateAndTime></GetSystemDateAndTimeResponse>`)
	case strings.Contains(string(body), "<GetProfiles"):
		reply(`<GetProfilesResponse><Profiles token="p"><PTZConfiguration token="c"/></Profiles></GetProfilesResponse>`)
	case strings.Contains(string(body), "<GetPresets"):
		reply(`<GetPresetsResponse><Preset token="t1"><Name>Preset 1</Name></Preset></GetPresetsResponse>`)
	default:
		reply(`<GetCapabilitiesResponse/>`)
	}
}

func (o *onvifStub) count(op string) (int, int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.ops[op], o.maxGoing
}

func startONVIFStub(t *testing.T, delay time.Duration) (*onvifStub, string, int) {
	t.Helper()
	o := &onvifStub{delay: delay, ops: map[string]int{}}
	srv := httptest.NewServer(o)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	h, p, _ := net.SplitHostPort(u.Host)
	port, _ := strconv.Atoi(p)
	return o, h, port
}

func parallelGets(t *testing.T, c *client, path string, n int) []int {
	t.Helper()
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, _ := http.NewRequest("GET", c.base+path, nil)
			resp, err := c.hc.Do(req)
			if err != nil {
				t.Error(err)
				return
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			codes[i] = resp.StatusCode
		}()
	}
	wg.Wait()
	return codes
}

func signedInViewer(t *testing.T, base string, admin *client) *client {
	t.Helper()
	admin.do("POST", "/api/users", `{"username":"view","password":"viewer password","role":"viewer"}`)
	v := newClient(t, base)
	if code, _, body := v.do("POST", "/api/login", `{"username":"view","password":"viewer password"}`); code != 200 {
		t.Fatalf("viewer login %d %s", code, body)
	}
	return v
}

// Any number of viewers asking for a camera's clock make one ONVIF request
// per clockRead.maxAge.
func TestCameraClockCoalesced(t *testing.T) {
	stub, host, port := startONVIFStub(t, 50*time.Millisecond)
	ts, srv := newTestServer(t)
	srv.Cfg.Cameras[0].Host, srv.Cfg.Cameras[0].ONVIFPort = host, port
	admin := newClient(t, ts.URL)
	admin.do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`)
	viewer := signedInViewer(t, ts.URL, admin)
	for i, code := range parallelGets(t, viewer, "/api/cameras/cam1/clock", 20) {
		if code != 200 {
			t.Fatalf("request %d = %d", i, code)
		}
	}
	parallelGets(t, viewer, "/api/cameras/cam1/clock", 5)
	if n, peak := stub.count("GetSystemDateAndTime"); n != 1 || peak != 1 {
		t.Fatalf("camera requests = %d (peak %d at once), want 1", n, peak)
	}
	_, m, _ := viewer.do("GET", "/api/cameras/cam1/clock", "")
	if _, ok := m["offset_ms"]; !ok {
		t.Fatalf("clock answer %v", m)
	}
}

// ?fresh=1 from a viewer does not skip the cache, and concurrent fresh
// reads from an operator share one camera request.
func TestCameraControlFreshCoalesced(t *testing.T) {
	stub, host, port := startONVIFStub(t, 30*time.Millisecond)
	ts, srv := newTestServer(t)
	delete(srv.ctrl, "cam1")
	srv.onvifPTZ["cam1"] = onvif.NewPTZ(host, port, "", "u", "p", onvif.Invert{})
	admin := newClient(t, ts.URL)
	admin.do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`)
	viewer := signedInViewer(t, ts.URL, admin)
	for i, code := range parallelGets(t, viewer, "/api/cameras/cam1/control?fresh=1", 20) {
		if code != 200 {
			t.Fatalf("request %d = %d", i, code)
		}
	}
	parallelGets(t, viewer, "/api/cameras/cam1/control?fresh=1", 10)
	if n, _ := stub.count("GetPresets"); n != 1 {
		t.Fatalf("viewer fresh reads reached the camera %d times, want 1", n)
	}
	time.Sleep(controlFresh + 100*time.Millisecond)
	parallelGets(t, admin, "/api/cameras/cam1/control?fresh=1", 10)
	if n, _ := stub.count("GetPresets"); n != 2 {
		t.Fatalf("after an operator's fresh reads: %d camera reads, want 2", n)
	}
	_, _, body := viewer.do("GET", "/api/cameras/cam1/control", "")
	if !strings.Contains(body, `"presets":[{"id":1,"name":"Preset 1"}]`) {
		t.Fatalf("control %s", body)
	}
}

// A read that panics (a parser meeting an unexpected camera reply) fails
// with errReadPanicked for everyone waiting, is logged with its stack, and
// is kept like any failure; the server goes on.
func TestCamReadPanicIsAnError(t *testing.T) {
	var logs strings.Builder
	c := camReads[int]{log: slog.New(slog.NewTextHandler(&logs, nil))}
	p := readPolicy{maxAge: time.Second, errAge: 50 * time.Millisecond, timeout: time.Second}
	var calls atomic.Int32
	release := make(chan struct{})
	reply := []byte{}
	bad := func(context.Context) (int, error) {
		calls.Add(1)
		<-release
		return int(reply[len(reply)/2]), nil // a parser reading past a short reply: panics
	}
	errs := make(chan error, 5)
	for range 5 {
		go func() {
			_, err := c.get(context.Background(), "cam1", p, bad)
			errs <- err
		}()
	}
	time.Sleep(20 * time.Millisecond)
	close(release)
	for range 5 {
		select {
		case err := <-errs:
			if !errors.Is(err, errReadPanicked) {
				t.Fatalf("waiter got %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("a waiter was left waiting after the read panicked")
		}
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("reads = %d, want 1", n)
	}
	if _, err := c.get(context.Background(), "cam1", p, bad); !errors.Is(err, errReadPanicked) || calls.Load() != 1 {
		t.Fatalf("the failure was not reused: %v, %d reads", err, calls.Load())
	}
	if !strings.Contains(logs.String(), "camera read panicked") || !strings.Contains(logs.String(), "camread") {
		t.Fatalf("log: %s", logs.String())
	}
	time.Sleep(60 * time.Millisecond)
	if v, err := c.get(context.Background(), "cam1", p, func(context.Context) (int, error) { return 7, nil }); v != 7 || err != nil {
		t.Fatalf("after the failure aged: %d %v", v, err)
	}
	// The zero value (no logger) recovers too.
	var quiet camReads[int]
	if _, err := quiet.get(context.Background(), "cam1", p, func(context.Context) (int, error) { panic("boom") }); !errors.Is(err, errReadPanicked) {
		t.Fatalf("zero value: %v", err)
	}
}

// A request arriving after a change does not join a read that started
// before it: it starts a new one and gets the state after the change; the
// old read still answers its own waiters and is not kept.
func TestCamReadAfterInvalidateIsFresh(t *testing.T) {
	var c camReads[int]
	p := readPolicy{maxAge: time.Minute, errAge: time.Minute, timeout: time.Second}
	release := make(chan struct{})
	var state atomic.Int32
	state.Store(1)
	read := func(context.Context) (int, error) {
		v := int(state.Load())
		if v == 1 {
			<-release // the read from before the change is slow
		}
		return v, nil
	}
	old := make(chan int, 1)
	go func() {
		v, _ := c.get(context.Background(), "cam1", p, read)
		old <- v
	}()
	time.Sleep(20 * time.Millisecond)
	state.Store(2) // the change
	c.invalidate("cam1")
	if v, err := c.get(context.Background(), "cam1", p, read); err != nil || v != 2 {
		t.Fatalf("after the change got %d, %v; want 2", v, err)
	}
	close(release)
	if v := <-old; v != 1 {
		t.Fatalf("the old waiter got %d", v)
	}
	if v, _ := c.get(context.Background(), "cam1", p, read); v != 2 {
		t.Fatalf("kept %d after the old read ended; want 2", v)
	}
}
