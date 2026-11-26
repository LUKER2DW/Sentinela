package auth

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func newTestLimiter(max int, per time.Duration, keys int) (*Limiter, *time.Time) {
	l := NewLimiterCap(max, per, keys)
	now := time.Unix(1000, 0)
	l.now = func() time.Time { return now }
	return l, &now
}

func TestLimiterKeyCap(t *testing.T) {
	l, now := newTestLimiter(2, time.Minute, 100)
	for i := 0; i < 100; i++ {
		if !l.Allow(fmt.Sprintf("k%d", i)) {
			t.Fatalf("key %d refused below the cap", i)
		}
	}
	// Full of live keys: none is forgotten, the table never grows, and new
	// keys go uncounted. However many events they make, none of them (nor
	// any other new key) is ever reported over the limit.
	for i := 0; i < 50; i++ {
		if !l.Allow("new") || !l.Allow(fmt.Sprintf("new%d", i)) {
			t.Fatalf("an untracked key was refused (event %d)", i)
		}
	}
	if l.Over("new") || l.Over("another") {
		t.Fatal("an untracked key must never inherit a lock")
	}
	if len(l.hits) != 100 {
		t.Fatalf("keys = %d, want 100", len(l.hits))
	}
	for i := 0; i < 100; i++ {
		if _, ok := l.hits[fmt.Sprintf("k%d", i)]; !ok {
			t.Fatalf("live key k%d was forgotten to make room", i)
		}
	}
	// Once the window passes the expired keys are dropped and room appears.
	*now = now.Add(2 * time.Minute)
	for i, want := range []bool{true, true, false} { // max 2
		if got := l.Allow("later"); got != want {
			t.Fatalf("event %d of a key tracked after expiry freed room = %v, want %v", i+1, got, want)
		}
	}
	if !l.Over("later") {
		t.Fatal("a key tracked after expiry freed room must be counted again")
	}
	if len(l.hits) != 1 {
		t.Fatalf("keys after expiry = %d, want 1", len(l.hits))
	}
}

// The residual of the audit: a counter just under its limit must survive
// any number of fresh keys, so a flood of filler names cannot reset it.
func TestLimiterNeverForgetsAnUnderLimitCounter(t *testing.T) {
	cases := []struct {
		name   string
		before int // events on the victim before the flood
	}{
		{"one under the limit", 299},
		{"at the limit", 300},
		{"barely counted", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l, now := newTestLimiter(300, time.Hour, 2000)
			for i := 0; i < c.before; i++ {
				l.Allow("acctall:victim")
			}
			for i := 0; i < 2*2000; i++ {
				*now = now.Add(time.Millisecond)
				l.Allow(fmt.Sprintf("acctall:filler%d", i))
			}
			w, ok := l.hits["acctall:victim"]
			if !ok || w.n != c.before {
				t.Fatalf("victim counter = %+v (present %v), want n=%d", w, ok, c.before)
			}
			for i := c.before; i < 300; i++ {
				l.Allow("acctall:victim")
			}
			if !l.Over("acctall:victim") {
				t.Fatal("victim must be limited after 300 events in its window")
			}
			// The window still ends: 61 minutes later the key starts afresh.
			*now = now.Add(61 * time.Minute)
			if l.Over("acctall:victim") || !l.Allow("acctall:victim") {
				t.Fatal("a new window must start after the old one expires")
			}
		})
	}
}

// Sources that already used up their limit keep their counter.
func TestLimiterKeepsLimitedKeysWhenFull(t *testing.T) {
	l, _ := newTestLimiter(2, time.Minute, 100)
	for i := 0; i < 100; i++ {
		k := fmt.Sprintf("k%d", i)
		l.Allow(k)
		l.Allow(k) // at the limit
	}
	for i := 0; i < 20; i++ {
		l.Allow(fmt.Sprintf("new%d", i))
	}
	if len(l.hits) != 100 {
		t.Fatalf("keys = %d, want 100 (limited keys must stay)", len(l.hits))
	}
	if l.Allow("k0") || !l.Over("k0") {
		t.Fatal("a limited key must stay limited")
	}
}

func TestLimiterOverUnknownKey(t *testing.T) {
	cases := []struct {
		name   string
		filled int // keys recorded before the check, each at its limit
	}{
		{"empty", 0},
		{"room left", 1},
		{"full", 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l, _ := newTestLimiter(1, time.Minute, 2)
			for i := 0; i < c.filled; i++ {
				l.Allow(fmt.Sprintf("k%d", i))
			}
			if l.Over("unknown") {
				t.Fatal("Over must be false for a key the table does not track")
			}
			for i := 0; i < c.filled; i++ {
				if !l.Over(fmt.Sprintf("k%d", i)) {
					t.Fatalf("k%d keeps its own counter", i)
				}
			}
		})
	}
}

// No call scans the table: expired keys are dropped from the front of the
// queue, so the entries examined stay proportional to the keys ever added.
func TestLimiterPruneIsAmortised(t *testing.T) {
	cases := []struct {
		name    string
		maxKeys int
		keys    int
		step    time.Duration
	}{
		{"below cap", 50000, 20000, time.Millisecond},
		{"at cap", 1000, 20000, time.Millisecond},
		{"keys expiring all the time", 1000, 20000, 10 * time.Millisecond},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l, now := newTestLimiter(5, time.Second, c.maxKeys)
			calls := 0
			for i := 0; i < c.keys; i++ {
				*now = now.Add(c.step)
				l.Allow(fmt.Sprintf("k%d", i))
				l.Over(fmt.Sprintf("k%d", i/2))
				calls += 2
			}
			if l.pruned > c.keys+calls {
				t.Fatalf("examined %d entries for %d keys and %d calls, want <= %d", l.pruned, c.keys, calls, c.keys+calls)
			}
			if len(l.hits) > c.maxKeys {
				t.Fatalf("keys = %d over the cap %d", len(l.hits), c.maxKeys)
			}
		})
	}
}

// After a flood the queue and map give their memory back.
func TestLimiterShrinksAfterFlood(t *testing.T) {
	l, now := newTestLimiter(5, time.Minute, 50000)
	for i := 0; i < 40000; i++ {
		l.Allow(fmt.Sprintf("k%d", i))
	}
	*now = now.Add(2 * time.Minute)
	l.Allow("after")
	if len(l.hits) != 1 || len(l.queue)-l.head != 1 || len(l.queue) > 1024 || l.peak != 1 {
		t.Fatalf("hits=%d queue=%d head=%d peak=%d after expiry", len(l.hits), len(l.queue), l.head, l.peak)
	}
}

// A clock that steps back leaves the queue out of order; counting stays
// right and expired keys are still dropped later.
func TestLimiterClockStepBack(t *testing.T) {
	l, now := newTestLimiter(1, time.Minute, 10)
	l.Allow("a")
	*now = now.Add(-30 * time.Second)
	l.Allow("b")                     // queued after a, with an earlier start
	*now = now.Add(80 * time.Second) // a is 50 s old (live, front), b 80 s (expired)
	if !l.Allow("b") || l.Allow("b") {
		t.Fatal("b: a new window then the limit")
	}
	*now = now.Add(5 * time.Minute)
	l.Allow("c")
	if len(l.hits) != 1 {
		t.Fatalf("keys = %d, want only c", len(l.hits))
	}
}

var home = WithHomeLane(context.Background())

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
		{"free slot, home", newArgonGate(1, 1, 1, 1, time.Second), home, false, nil, true},
		{"free slot, public", newArgonGate(1, 1, 1, 1, time.Second), context.Background(), false, nil, true},
		{"canceled before start", newArgonGate(1, 1, 1, 1, time.Second), canceled, false, context.Canceled, true},
		{"home queue full fails fast", newArgonGate(1, 0, 1, 1, time.Second), home, true, ErrBusy, true},
		{"home wait too long", newArgonGate(1, 1, 1, 1, 60*time.Millisecond), home, true, ErrBusy, false},
		{"public wait too long for a slot", newArgonGate(1, 1, 1, 1, 60*time.Millisecond), context.Background(), true, ErrBusy, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.prefill {
				full(c.gate)
			}
			start := time.Now()
			release, err := c.gate.acquire(c.ctx)
			if !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
			if err == nil {
				release()
			}
			if c.fast && time.Since(start) > 40*time.Millisecond {
				t.Fatalf("took %v, want an immediate answer", time.Since(start))
			}
			if !c.fast && time.Since(start) < 50*time.Millisecond {
				t.Fatalf("answered after %v, before maxWait", time.Since(start))
			}
			if c.gate.homeWaiting.Load() != 0 || c.gate.publicWaiting.Load() != 0 || len(c.gate.public) != 0 {
				t.Fatalf("leaked: home waiting %d, public waiting %d, public tokens %d",
					c.gate.homeWaiting.Load(), c.gate.publicWaiting.Load(), len(c.gate.public))
			}
		})
	}
}

func TestArgonGateWaiterGetsSlotAndCancelFrees(t *testing.T) {
	for _, ctx := range []context.Context{home, context.Background()} {
		g := newArgonGate(1, 2, 1, 2, 5*time.Second)
		g.slots <- struct{}{}

		got := make(chan error, 1)
		go func() {
			release, err := g.acquire(ctx)
			if err == nil {
				release()
			}
			got <- err
		}()
		time.Sleep(30 * time.Millisecond)
		<-g.slots
		if err := <-got; err != nil {
			t.Fatalf("waiter: %v", err)
		}

		// A waiter whose request ends stops waiting and never takes the slot.
		g.slots <- struct{}{}
		cctx, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() {
			_, err := g.acquire(cctx)
			done <- err
		}()
		time.Sleep(30 * time.Millisecond)
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled waiter: %v", err)
		}
		<-g.slots
		if len(g.slots) != 0 || len(g.public) != 0 || g.homeWaiting.Load() != 0 || g.publicWaiting.Load() != 0 {
			t.Fatalf("slots=%d public=%d waiting=%d/%d, want all 0", len(g.slots), len(g.public),
				g.homeWaiting.Load(), g.publicWaiting.Load())
		}
	}
}

// The audit's residual: anonymous logins from the internet kept both slots
// and the shared queue busy, so home sign-ins got 503. Public work now runs on
// one slot with its own short queue; home work always finds the other slot.
func TestArgonGatePublicFloodLeavesHomeASlot(t *testing.T) {
	g := newArgonGate(2, 8, 1, 4, 300*time.Millisecond)
	outside := context.Background()

	running, err := g.acquire(outside) // the flood's running hash
	if err != nil {
		t.Fatal(err)
	}
	// The flood fills its own queue...
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if release, err := g.acquire(outside); err == nil {
				<-stop
				release()
			}
		}()
	}
	waitFor(t, func() bool { return g.publicWaiting.Load() == 4 })
	// ...and anything more from outside is refused at once.
	start := time.Now()
	if _, err := g.acquire(outside); !errors.Is(err, ErrBusy) || time.Since(start) > 40*time.Millisecond {
		t.Fatalf("public over its queue: %v after %v, want an immediate ErrBusy", err, time.Since(start))
	}
	// Home gets the free slot at once, even with the public queue full.
	start = time.Now()
	release, err := g.acquire(home)
	if err != nil || time.Since(start) > 40*time.Millisecond {
		t.Fatalf("home during the flood: %v after %v", err, time.Since(start))
	}
	// A second home check waits for that slot, never for the public one.
	got := make(chan error, 1)
	go func() {
		r, err := g.acquire(home)
		if err == nil {
			r()
		}
		got <- err
	}()
	time.Sleep(30 * time.Millisecond)
	release()
	if err := <-got; err != nil {
		t.Fatalf("second home check: %v", err)
	}
	running()
	close(stop)
	wg.Wait()
	if len(g.slots) != 0 || len(g.public) != 0 {
		t.Fatalf("slots=%d public=%d after the flood", len(g.slots), len(g.public))
	}
}

// waitFor polls cond for up to a second.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not reached")
}

// A saturated queue answers ErrBusy without running argon2id; an abandoned
// request does not run its hash either.
func TestPasswordChecksSkipHashWhenBusyOrAbandoned(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	busy := newArgonGate(1, 0, 1, 0, time.Second)
	busy.slots <- struct{}{}
	canceled, cancel := context.WithCancel(home)
	cancel()
	idle := newArgonGate(1, 1, 1, 1, time.Second)

	cases := []struct {
		name string
		gate *argonGate
		ctx  context.Context
		want error
	}{
		{"queue full", busy, home, ErrBusy},
		{"abandoned", idle, canceled, context.Canceled},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before := ArgonRuns()
			acquire := func() (func(), error) { return c.gate.acquire(c.ctx) }
			ok, err := verifyPassword(h, "correct horse battery", acquire)
			if ok || !errors.Is(err, c.want) {
				t.Fatalf("verify = %v, %v; want false, %v", ok, err, c.want)
			}
			_, err = hashPassword("another good password", acquire)
			if !errors.Is(err, c.want) {
				t.Fatalf("hash err = %v, want %v", err, c.want)
			}
			if ArgonRuns() != before {
				t.Fatal("argon2id ran for a request that was refused")
			}
		})
	}
	// Normal path unchanged: right and wrong passwords still tell apart.
	for _, ctx := range []context.Context{home, context.Background()} {
		if ok, err := VerifyPasswordContext(ctx, h, "correct horse battery"); !ok || err != nil {
			t.Fatalf("valid password: %v %v", ok, err)
		}
		if ok, err := VerifyPasswordContext(ctx, h, "wrong password!!"); ok || err != nil {
			t.Fatalf("wrong password: %v %v", ok, err)
		}
	}
	if ok, err := VerifyPasswordContext(canceled, "garbage", "x"); ok || err != nil {
		t.Fatalf("malformed hash must be a plain false: %v %v", ok, err)
	}
	if err := VerifyDummyContext(context.Background(), "whatever password"); err != nil {
		t.Fatalf("dummy verify: %v", err)
	}
}
