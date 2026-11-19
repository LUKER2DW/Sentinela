package auth

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestLimiterKeyCap(t *testing.T) {
	l := NewLimiterCap(2, time.Minute, 100)
	now := time.Unix(1000, 0)
	l.now = func() time.Time { return now }
	for i := 0; i < 100; i++ {
		if !l.Allow(fmt.Sprintf("k%d", i)) {
			t.Fatalf("key %d refused below the cap", i)
		}
	}
	if len(l.hits) != 100 {
		t.Fatalf("keys = %d, want 100", len(l.hits))
	}
	// Full of keys that are not limited (one event each, max 2): a flood of
	// fresh sources must not lock new ones out, so one is forgotten per new
	// key and the map never grows.
	for i := 0; i < 50; i++ {
		if !l.Allow(fmt.Sprintf("new%d", i)) {
			t.Fatalf("new key %d refused although room can be made", i)
		}
	}
	if len(l.hits) != 100 {
		t.Fatalf("keys = %d after eviction, want 100", len(l.hits))
	}
	// Once the window passes the expired keys are swept and room appears.
	now = now.Add(2 * time.Minute)
	if !l.Allow("later") {
		t.Fatal("expired keys were not freed")
	}
	if len(l.hits) != 1 {
		t.Fatalf("keys after sweep = %d, want 1", len(l.hits))
	}
}

// Sources that already used up their limit keep their counter: the limiter
// refuses a new key rather than forget a brute-forcer.
func TestLimiterKeepsLimitedKeysWhenFull(t *testing.T) {
	l := NewLimiterCap(2, time.Minute, 100)
	now := time.Unix(1000, 0)
	l.now = func() time.Time { return now }
	for i := 0; i < 100; i++ {
		k := fmt.Sprintf("k%d", i)
		l.Allow(k)
		l.Allow(k) // at the limit
	}
	for i := 0; i < 20; i++ {
		if l.Allow(fmt.Sprintf("new%d", i)) {
			t.Fatalf("new key %d accepted while every key is limited", i)
		}
	}
	if len(l.hits) != 100 {
		t.Fatalf("keys = %d, want 100 (limited keys must stay)", len(l.hits))
	}
	if l.Allow("k0") {
		t.Fatal("a limited key must stay limited")
	}
}

func TestLimiterOverUnknownKeyWhenFull(t *testing.T) {
	l := NewLimiterCap(1, time.Minute, 1)
	if !l.Allow("a") || l.Allow("b") {
		t.Fatal("cap of 1")
	}
	if l.Over("b") {
		t.Fatal("Over must not report a key that was never recorded")
	}
}

func TestLimiterSweepIsAmortised(t *testing.T) {
	cases := []struct {
		name      string
		maxKeys   int
		calls     int
		maxSweeps int
	}{
		{"below cap, same instant", 5000, 20000, 1},
		{"at cap, same instant", 1000, 20000, 2}, // at most one forced scan per second
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := NewLimiterCap(5, time.Hour, c.maxKeys)
			now := time.Unix(1000, 0)
			l.now = func() time.Time { return now }
			for i := 0; i < c.calls; i++ {
				l.Allow(fmt.Sprintf("k%d", i%2000))
			}
			if l.sweeps > c.maxSweeps {
				t.Fatalf("%d full scans for %d calls in one instant, want <= %d", l.sweeps, c.calls, c.maxSweeps)
			}
		})
	}
}

func TestLimiterSweepRunsOncePerInterval(t *testing.T) {
	l := NewLimiterCap(5, time.Minute, 1000)
	now := time.Unix(1000, 0)
	l.now = func() time.Time { return now }
	l.Allow("a")
	first := l.sweeps
	for i := 0; i < 100; i++ {
		now = now.Add(100 * time.Millisecond) // 10 s in total, under window/2
		l.Allow("a")
	}
	if l.sweeps != first {
		t.Fatalf("swept %d extra times inside one interval", l.sweeps-first)
	}
	now = now.Add(time.Minute)
	l.Allow("a")
	if l.sweeps != first+1 {
		t.Fatalf("sweeps = %d, want %d after the interval", l.sweeps, first+1)
	}
}

func TestArgonGateAcquire(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	full := func(g *argonGate) {
		for i := 0; i < cap(g.slots); i++ {
			g.slots <- struct{}{}
		}
	}
	cases := []struct {
		name    string
		gate    *argonGate
		ctx     context.Context
		prefill bool
		want    error
		fast    bool
	}{
		{"free slot", newArgonGate(1, 1, time.Second), context.Background(), false, nil, true},
		{"canceled before start", newArgonGate(1, 1, time.Second), canceled, false, context.Canceled, true},
		{"queue full fails fast", newArgonGate(1, 0, time.Second), context.Background(), true, ErrBusy, true},
		{"wait too long", newArgonGate(1, 1, 60*time.Millisecond), context.Background(), true, ErrBusy, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.prefill {
				full(c.gate)
			}
			start := time.Now()
			err := c.gate.acquire(c.ctx)
			if !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
			if c.fast && time.Since(start) > 40*time.Millisecond {
				t.Fatalf("took %v, want an immediate answer", time.Since(start))
			}
			if !c.fast && time.Since(start) < 50*time.Millisecond {
				t.Fatalf("answered after %v, before maxWait", time.Since(start))
			}
			if n := c.gate.waiting.Load(); n != 0 {
				t.Fatalf("waiting counter leaked: %d", n)
			}
		})
	}
}

func TestArgonGateWaiterGetsSlotAndCancelFrees(t *testing.T) {
	g := newArgonGate(1, 2, 5*time.Second)
	g.slots <- struct{}{}

	got := make(chan error, 1)
	go func() { got <- g.acquire(context.Background()) }()
	time.Sleep(30 * time.Millisecond)
	g.release()
	if err := <-got; err != nil {
		t.Fatalf("waiter: %v", err)
	}

	// A waiter whose request ends stops waiting and never takes the slot.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- g.acquire(ctx) }()
	time.Sleep(30 * time.Millisecond)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled waiter: %v", err)
	}
	g.release()
	if len(g.slots) != 0 || g.waiting.Load() != 0 {
		t.Fatalf("slots=%d waiting=%d, want both 0", len(g.slots), g.waiting.Load())
	}
}

// A saturated queue answers ErrBusy without running argon2id; an abandoned
// request does not run its hash either.
func TestPasswordChecksSkipHashWhenBusyOrAbandoned(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	busy := newArgonGate(1, 0, time.Second)
	busy.slots <- struct{}{}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	idle := newArgonGate(1, 1, time.Second)

	cases := []struct {
		name string
		gate *argonGate
		ctx  context.Context
		want error
	}{
		{"queue full", busy, context.Background(), ErrBusy},
		{"abandoned", idle, canceled, context.Canceled},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before := ArgonRuns()
			ok, err := verifyPassword(h, "correct horse battery", func() error { return c.gate.acquire(c.ctx) }, c.gate.release)
			if ok || !errors.Is(err, c.want) {
				t.Fatalf("verify = %v, %v; want false, %v", ok, err, c.want)
			}
			_, err = hashPassword("another good password", func() error { return c.gate.acquire(c.ctx) }, c.gate.release)
			if !errors.Is(err, c.want) {
				t.Fatalf("hash err = %v, want %v", err, c.want)
			}
			if ArgonRuns() != before {
				t.Fatal("argon2id ran for a request that was refused")
			}
		})
	}
	// Normal path unchanged: right and wrong passwords still tell apart.
	if ok, err := VerifyPasswordContext(context.Background(), h, "correct horse battery"); !ok || err != nil {
		t.Fatalf("valid password: %v %v", ok, err)
	}
	if ok, err := VerifyPasswordContext(context.Background(), h, "wrong password!!"); ok || err != nil {
		t.Fatalf("wrong password: %v %v", ok, err)
	}
	if ok, err := VerifyPasswordContext(canceled, "garbage", "x"); ok || err != nil {
		t.Fatalf("malformed hash must be a plain false: %v %v", ok, err)
	}
	if err := VerifyDummyContext(context.Background(), "whatever password"); err != nil {
		t.Fatalf("dummy verify: %v", err)
	}
}
