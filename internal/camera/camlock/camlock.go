// Package camlock provides the lock that serializes the requests to one
// camera's control session (AGENTS.md §6: a single owner per camera).
package camlock

import (
	"context"
	"sync"
)

// Mutex is a mutual exclusion lock whose waiters can give up: LockContext
// returns when the caller's context ends, so a request that waited past its
// deadline leaves the queue without touching the camera session. Waiters
// get the lock in the order they asked. The zero value is unlocked.
type Mutex struct {
	once sync.Once
	ch   chan struct{}
}

func (m *Mutex) slot() chan struct{} {
	m.once.Do(func() { m.ch = make(chan struct{}, 1) })
	return m.ch
}

// Lock waits for the lock.
func (m *Mutex) Lock() { m.slot() <- struct{}{} }

// LockContext waits for the lock until ctx ends. It returns ctx's error,
// without the lock, when ctx ended first (or had already ended).
func (m *Mutex) LockContext(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case m.slot() <- struct{}{}:
		if err := ctx.Err(); err != nil {
			m.Unlock()
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// TryLock takes the lock if it is free.
func (m *Mutex) TryLock() bool {
	select {
	case m.slot() <- struct{}{}:
		return true
	default:
		return false
	}
}

// Unlock releases the lock. As with sync.Mutex, unlocking a Mutex that is
// not locked is a programming error and panics: ignoring it would hide a
// release of a lock another request now holds.
func (m *Mutex) Unlock() {
	select {
	case <-m.slot():
	default:
		panic("camlock: unlock of unlocked Mutex")
	}
}
