package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// blockedWriter is a client that stops reading: its first Write blocks.
type blockedWriter struct {
	h       http.Header
	writing chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockedWriter) Header() http.Header { return b.h }
func (b *blockedWriter) WriteHeader(int)     {}
func (b *blockedWriter) Write(p []byte) (int, error) {
	b.once.Do(func() { close(b.writing) })
	<-b.release
	return len(p), nil
}

// frame.jpeg: requests that go away do not start go2rtc decode after
// decode, and a client that stops reading holds nothing others wait on.
func TestFrameCoalescedAndUnlocked(t *testing.T) {
	var calls atomic.Int32
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		time.Sleep(150 * time.Millisecond) // a keyframe wait; finishes even if we left
		_, _ = w.Write([]byte("\xff\xd8frame\xff\xd9"))
	}))
	defer fake.Close()
	ts, srv := newTestServer(t)
	srv.go2rtc, _ = url.Parse(fake.URL)
	admin := newClient(t, ts.URL)
	admin.do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`)

	for range 20 {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/api/cameras/cam1/frame.jpeg", nil)
		if resp, err := admin.hc.Do(req); err == nil {
			resp.Body.Close()
		}
		cancel()
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("go2rtc decodes for 20 abandoned requests = %d, want 1", n)
	}

	// A stalled reader...
	u, _ := url.Parse(ts.URL)
	req := httptest.NewRequest("GET", "/api/cameras/cam1/frame.jpeg", nil)
	for _, c := range admin.hc.Jar.Cookies(u) {
		req.AddCookie(c)
	}
	bw := &blockedWriter{h: http.Header{}, writing: make(chan struct{}), release: make(chan struct{})}
	defer close(bw.release)
	go srv.Handler().ServeHTTP(bw, req)
	select {
	case <-bw.writing:
	case <-time.After(3 * time.Second):
		t.Fatal("the stalled request never wrote")
	}
	// ...does not keep the next one waiting.
	done := make(chan int, 1)
	go func() { code, _, _ := admin.do("GET", "/api/cameras/cam1/frame.jpeg", ""); done <- code }()
	select {
	case code := <-done:
		if code != 200 {
			t.Fatalf("frame = %d", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a stalled client blocked the next frame request")
	}
}
