package auth

import (
	"testing"
	"time"
)

func newTestGuard() (*CodeGuard, *time.Time) {
	g := NewCodeGuard()
	now := time.Unix(1_000_000, 0)
	g.now = func() time.Time { return now }
	return g, &now
}

// Wrong codes in a row lock the factor with a doubling wait, capped at an
// hour; the count is per user and per kind of zone.
func TestCodeGuardBackoff(t *testing.T) {
	g, _ := newTestGuard()
	want := []time.Duration{0, 0, 0, 0, time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute,
		16 * time.Minute, 32 * time.Minute, time.Hour, time.Hour, time.Hour}
	for i, w := range want {
		f := g.Failed(1, true)
		if f.Fails != i+1 || f.Lock != w {
			t.Fatalf("wrong code %d: %+v, want lock %v", i+1, f, w)
		}
		if got := g.Locked(1, true); got != w {
			t.Fatalf("after wrong code %d Locked = %v, want %v", i+1, got, w)
		}
	}
	if g.Locked(2, true) != 0 || g.Locked(1, false) != 0 {
		t.Fatal("another user, or the home zones, must not be locked")
	}
}

func TestCodeGuardLockEnds(t *testing.T) {
	g, now := newTestGuard()
	for i := 0; i < CodeLockAfter; i++ {
		g.Failed(1, true)
	}
	*now = now.Add(59 * time.Second)
	if g.Locked(1, true) != time.Second {
		t.Fatalf("Locked = %v, want 1s left", g.Locked(1, true))
	}
	*now = now.Add(time.Second)
	if g.Locked(1, true) != 0 {
		t.Fatal("the lock must end")
	}
	// The run goes on: the next wrong code locks for twice as long.
	if f := g.Failed(1, true); f.Lock != 2*time.Minute {
		t.Fatalf("next lock = %v, want 2m", f.Lock)
	}
}

// A run is forgotten a day after its last wrong code, so typos weeks apart
// never lock; a run still going on (codes under a day apart) keeps counting.
func TestCodeGuardForgetsQuietRuns(t *testing.T) {
	cases := []struct {
		name     string
		gap      time.Duration // between wrong codes
		wantLock bool          // after CodeLockAfter of them
	}{
		{"codes an hour apart", time.Hour, true},
		{"codes a day apart", 24 * time.Hour, true},
		{"codes over a day apart", 24*time.Hour + time.Minute, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g, now := newTestGuard()
			var f CodeFailure
			for i := 0; i < CodeLockAfter; i++ {
				*now = now.Add(c.gap)
				f = g.Failed(1, true)
			}
			if got := f.Lock > 0; got != c.wantLock {
				t.Fatalf("locked = %v (%+v), want %v", got, f, c.wantLock)
			}
			if !c.wantLock && f.Fails != 1 {
				t.Fatalf("fails = %d, want a fresh run of 1", f.Fails)
			}
		})
	}
	// After a lock, a day without wrong codes starts over: the next wrong
	// code is the first of a new run, which locks (and alerts) again only
	// after CodeLockAfter more.
	g, now := newTestGuard()
	for i := 0; i < CodeLockAfter+3; i++ {
		g.Failed(1, true)
	}
	*now = now.Add(25 * time.Hour)
	if g.Locked(1, true) != 0 {
		t.Fatal("the lock must have ended")
	}
	for i := 1; i < CodeLockAfter; i++ {
		if f := g.Failed(1, true); f.Fails != i || f.Lock != 0 {
			t.Fatalf("wrong code %d of the new run: %+v", i, f)
		}
	}
	if f := g.Failed(1, true); f.Lock != time.Minute || !f.Alert {
		t.Fatalf("new run's lock = %+v, want 1m and an alert", f)
	}
}

func TestCodeGuardPassed(t *testing.T) {
	cases := []struct {
		name                 string
		passPublic           bool
		wantPublic, wantHome bool // still locked after the good code
	}{
		{"good code outside clears the outside count only", true, false, true},
		{"good code at home clears both", false, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g, _ := newTestGuard()
			for i := 0; i < CodeLockAfter; i++ {
				g.Failed(1, true)
				g.Failed(1, false)
			}
			g.Passed(1, c.passPublic)
			if got := g.Locked(1, true) > 0; got != c.wantPublic {
				t.Errorf("outside locked = %v, want %v", got, c.wantPublic)
			}
			if got := g.Locked(1, false) > 0; got != c.wantHome {
				t.Errorf("home locked = %v, want %v", got, c.wantHome)
			}
		})
	}
}

// The account is told when a run first locks, then at most once a day.
func TestCodeGuardAlerts(t *testing.T) {
	g, now := newTestGuard()
	var alerts int
	for i := 0; i < 40; i++ {
		*now = now.Add(time.Hour)
		if g.Failed(1, true).Alert {
			alerts++
		}
	}
	// 36 locks spread over 40 hours: the first, then one a day later.
	if alerts != 2 {
		t.Fatalf("alerts = %d, want 2", alerts)
	}
}
