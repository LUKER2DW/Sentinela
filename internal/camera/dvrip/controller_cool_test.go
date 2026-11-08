package dvrip

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

var _ net.Error = timeoutErr{}

// A camera that does not accept logins is left alone: after one failed
// login, requests fail at once instead of each dialing (and waiting) again.
func TestUnresponsiveCameraIsNotHammered(t *testing.T) {
	var dials atomic.Int32
	c := NewController("192.0.2.1:34567", "admin", "secret", Invert{}, nil)
	c.dial = func(context.Context, string, string, string) (*Conn, error) {
		dials.Add(1)
		return nil, timeoutErr{}
	}
	defer c.Close()
	ctx := context.Background()
	if err := c.Preset(ctx, "goto", 1); err == nil || errors.Is(err, ErrUnresponsive) {
		t.Fatalf("first request should reach the camera and fail, got %v", err)
	}
	for i := 0; i < 5; i++ {
		if err := c.Preset(ctx, "goto", 1); !errors.Is(err, ErrUnresponsive) {
			t.Fatalf("request %d during the pause: %v", i, err)
		}
	}
	if n := dials.Load(); n != 1 {
		t.Fatalf("dialed %d times during the pause, want 1", n)
	}
}

// The pause ends, and one good login clears it.
func TestPauseEndsAndLoginClearsIt(t *testing.T) {
	f := newFake(t)
	var fail atomic.Bool
	fail.Store(true)
	c := NewController(f.addr(), "admin", "secret", Invert{}, nil)
	real := c.dial
	c.dial = func(ctx context.Context, a, u, p string) (*Conn, error) {
		if fail.Load() {
			return nil, timeoutErr{}
		}
		return real(ctx, a, u, p)
	}
	defer c.Close()
	ctx := context.Background()
	_ = c.Preset(ctx, "goto", 1)
	c.mu.Lock()
	c.coolUntil = time.Now().Add(-time.Second) // the pause is over
	c.mu.Unlock()
	fail.Store(false)
	if err := c.Preset(ctx, "goto", 1); err != nil {
		t.Fatalf("after the pause: %v", err)
	}
	c.mu.Lock()
	failures, cool := c.failures, c.coolUntil
	c.mu.Unlock()
	if failures != 0 || !cool.IsZero() {
		t.Fatalf("a good login must clear the pause, got failures=%d until=%v", failures, cool)
	}
}

// Pauses lengthen with repeated failures but never pass coolMax.
func TestPauseGrowsAndIsCapped(t *testing.T) {
	c := NewController("192.0.2.1:34567", "admin", "secret", Invert{}, nil)
	var last time.Duration
	for i := 1; i <= 10; i++ {
		c.cool()
		d := time.Until(c.coolUntil)
		if i == 1 && (d > coolMin || d < coolMin-time.Second) {
			t.Fatalf("first pause %v, want about %v", d, coolMin)
		}
		if d > coolMax {
			t.Fatalf("pause %v exceeds the cap", d)
		}
		last = d
	}
	if last < coolMax-time.Second {
		t.Fatalf("pause after 10 failures is %v, want about %v", last, coolMax)
	}
}

// Stopping a movement is always tried, even during the pause.
func TestStopIgnoresThePause(t *testing.T) {
	f := newFake(t)
	c := NewController(f.addr(), "admin", "secret", Invert{}, nil)
	defer c.Close()
	ctx := context.Background()
	if err := c.Start(ctx, "move", "left", 4); err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	c.drop() // the session died; and the camera is in its pause
	c.coolUntil = time.Now().Add(time.Minute)
	c.mu.Unlock()
	if err := c.Stop(ctx); err != nil {
		t.Fatalf("stop during the pause: %v", err)
	}
	if got := strings.Join(f.ptzLog(), ","); !strings.HasSuffix(got, ":-1:4") {
		t.Fatalf("ptz log = %s, want a stop", got)
	}
}

// A session that was just opened is not retried when its request fails.
func TestFreshSessionFailureIsNotRetried(t *testing.T) {
	f := newFake(t)
	c := NewController(f.addr(), "admin", "secret", Invert{}, nil)
	var dials atomic.Int32
	real := c.dial
	c.dial = func(ctx context.Context, a, u, p string) (*Conn, error) {
		dials.Add(1)
		return real(ctx, a, u, p)
	}
	defer c.Close()
	err := c.with(context.Background(), func(*Conn) error { return timeoutErr{} })
	if err == nil {
		t.Fatal("expected the request error")
	}
	if n := dials.Load(); n != 1 {
		t.Fatalf("logged in %d times for one failed request, want 1", n)
	}
}
