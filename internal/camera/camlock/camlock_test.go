package camlock

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestLockContext(t *testing.T) {
	var m Mutex
	m.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := m.LockContext(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiter got %v, want its deadline", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("waiter left after %v", d)
	}
	if m.TryLock() {
		t.Fatal("lock taken twice")
	}
	m.Unlock()
	if err := m.LockContext(context.Background()); err != nil {
		t.Fatalf("free lock: %v", err)
	}
	m.Unlock()
	done, cancel2 := context.WithCancel(context.Background())
	cancel2()
	if err := m.LockContext(done); !errors.Is(err, context.Canceled) {
		t.Fatalf("ended context got %v", err)
	}
	if !m.TryLock() {
		t.Fatal("an ended context kept the lock")
	}
	m.Unlock()
}

// Unlocking an unlocked Mutex panics, as sync.Mutex does.
func TestUnlockOfUnlocked(t *testing.T) {
	for name, m := range map[string]*Mutex{"zero": {}, "used": {}} {
		if name == "used" {
			if !m.TryLock() {
				t.Fatal("a new Mutex is locked")
			}
			m.Unlock()
		}
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: unlock of an unlocked Mutex did not panic", name)
				}
			}()
			m.Unlock()
		}()
		if !m.TryLock() {
			t.Errorf("%s: the failed unlock left the lock taken", name)
		}
	}
}

func TestMutualExclusion(t *testing.T) {
	var m Mutex
	var wg sync.WaitGroup
	n, inside := 0, 0
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := m.LockContext(context.Background()); err != nil {
				t.Error(err)
				return
			}
			inside++
			if inside != 1 {
				t.Error("two holders")
			}
			n++
			inside--
			m.Unlock()
		}()
	}
	wg.Wait()
	if n != 50 {
		t.Fatalf("n = %d", n)
	}
}
