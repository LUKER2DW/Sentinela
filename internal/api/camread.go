package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"
)

// camReads coalesces one kind of read per camera: the control state, the
// clock, a zone-editor picture. Viewers can ask as often as they like; the
// camera (or go2rtc) is asked at most once at a time, everyone asking
// meanwhile shares that answer, and the answer is reused while it is fresh
// enough. A failure is reused too (for errAge), so a failing camera is not
// asked again at once either.
//
// The read runs on its own context, bounded by the policy's timeout and
// detached from the requests: one that goes away neither cancels the read
// for the others nor makes the next request start another. Callers stop
// waiting when their own context ends, and no lock is held while they
// write their responses. Keys are camera ids from the configuration, so the
// map stays as small as the camera list. The zero value is ready to use.
//
// A read that panics (a parser meeting a camera reply it did not expect)
// fails with errReadPanicked, logged with its stack when log is set: it
// runs on its own goroutine, where a panic would end the whole server.
type camReads[T any] struct {
	mu  sync.Mutex
	m   map[string]*camRead[T]
	log *slog.Logger
}

// errReadPanicked is the error of a camera read that panicked.
var errReadPanicked = errors.New("camera read failed: internal error")

// readPolicy says how long answers are kept and how long a read may take.
type readPolicy struct {
	maxAge  time.Duration // an answer younger than this is reused
	errAge  time.Duration // a failure younger than this is reused
	timeout time.Duration // the read's own deadline
}

type camRead[T any] struct {
	at     time.Time // when the kept answer arrived (zero: none)
	val    T
	err    error
	flight *camFlight[T] // the read in progress, if any
}

type camFlight[T any] struct {
	done    chan struct{}
	val     T
	err     error
	discard bool // invalidated while running: shared with its waiters, not kept
}

// get returns the camera's answer: the kept one when fresh enough for p
// (even while another read runs), else that of the read in progress unless
// a change made it stale (invalidate), else that of a new read.
func (c *camReads[T]) get(ctx context.Context, key string, p readPolicy, read func(context.Context) (T, error)) (T, error) {
	c.mu.Lock()
	if c.m == nil {
		c.m = map[string]*camRead[T]{}
	}
	e := c.m[key]
	if e == nil {
		e = &camRead[T]{}
		c.m[key] = e
	}
	keep := p.maxAge
	if e.err != nil {
		keep = min(keep, p.errAge)
	}
	if !e.at.IsZero() && time.Since(e.at) < keep {
		v, err := e.val, e.err
		c.mu.Unlock()
		return v, err
	}
	f := e.flight
	if f == nil || f.discard {
		f = &camFlight[T]{done: make(chan struct{})}
		e.flight = f
		go c.run(key, e, f, p.timeout, read)
	}
	c.mu.Unlock()
	select {
	case <-f.done:
		return f.val, f.err
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}
}

func (c *camReads[T]) run(key string, e *camRead[T], f *camFlight[T], timeout time.Duration, read func(context.Context) (T, error)) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	val, err := c.call(ctx, key, read)
	c.mu.Lock()
	f.val, f.err = val, err
	if e.flight == f { // a newer read may have replaced a discarded one
		e.flight = nil
	}
	if !f.discard {
		e.at, e.val, e.err = time.Now(), val, err
	}
	c.mu.Unlock()
	close(f.done)
}

// call runs read, turning a panic into errReadPanicked so that run still
// records the answer and releases the waiters.
func (c *camReads[T]) call(ctx context.Context, key string, read func(context.Context) (T, error)) (val T, err error) {
	defer func() {
		if v := recover(); v != nil {
			if c.log != nil {
				c.log.Error("camera read panicked", "camera", key, "value", fmt.Sprint(v), "stack", string(debug.Stack()))
			}
			var zero T
			val, err = zero, errReadPanicked
		}
	}()
	return read(ctx)
}

// invalidate drops the camera's kept answer (after a change was made on
// it); a read already running still answers its waiters but is not kept,
// and a request arriving after the change starts a new read.
func (c *camReads[T]) invalidate(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.m[key]; e != nil {
		var zero T
		e.at, e.val, e.err = time.Time{}, zero, nil
		if e.flight != nil {
			e.flight.discard = true
		}
	}
}
