package onvif

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/LUKER2DW/sentinela/internal/auth/redact"
	"github.com/LUKER2DW/sentinela/internal/store"
)

// Sink receives event transitions with the stored event id (implemented by
// the API: live push, snapshots, notifications).
type Sink func(cameraID, typ string, eventID int64, active bool, at time.Time)

// Watcher keeps a PullPoint subscription alive and writes events to the store.
type Watcher struct {
	CameraID string
	Client   *Client
	Store    *store.Store
	Log      *slog.Logger
	Sink     Sink
	// MaxEvent closes an event that never received an "inactive" message.
	MaxEvent time.Duration
	// MinPull is the shortest time between two pulls (default 1 s). This
	// camera ignores the PullMessages timeout and answers at once when it has
	// nothing to say: pulling again right away made ~45 requests a second,
	// which starved its encoder (frames dropped on every stream).
	MinPull time.Duration

	open   map[string]openEvent
	topics map[string]bool // "type|topic" -> last active state
}

type openEvent struct {
	id    int64
	start time.Time
}

// Run subscribes and pulls until ctx ends, re-subscribing with backoff.
func (w *Watcher) Run(ctx context.Context) {
	if w.MaxEvent == 0 {
		w.MaxEvent = 10 * time.Minute
	}
	if w.MinPull == 0 {
		w.MinPull = time.Second
	}
	w.open = map[string]openEvent{}
	w.topics = map[string]bool{}
	_ = w.Store.CloseDanglingEvents(ctx, w.CameraID, time.Minute)
	backoff := 5 * time.Second
	for ctx.Err() == nil {
		err := w.session(ctx)
		if ctx.Err() != nil {
			break
		}
		w.Log.Warn("onvif events: session ended", "camera", w.CameraID, "err", redact.Secrets(errString(err), w.Client.Password))
		select {
		case <-ctx.Done():
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 2*time.Minute)
	}
	w.closeAll(time.Now())
}

func errString(err error) string {
	if err == nil {
		return "closed"
	}
	return err.Error()
}

func (w *Watcher) session(ctx context.Context) error {
	sub, err := w.Client.Subscribe(ctx)
	if err != nil {
		return err
	}
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = sub.Unsubscribe(c)
	}()
	w.Log.Info("onvif events: subscribed", "camera", w.CameraID)
	lastRenew := time.Now()
	for ctx.Err() == nil {
		started := time.Now()
		msgs, err := sub.Pull(ctx, 10*time.Second)
		if err != nil {
			return err
		}
		now := time.Now()
		for _, m := range msgs {
			w.Handle(ctx, m, now)
		}
		w.expire(ctx, now)
		if now.Sub(lastRenew) > 50*time.Second {
			if err := sub.Renew(ctx); err != nil {
				return err
			}
			lastRenew = now
		}
		// A full batch may have more waiting; anything else keeps the pace.
		if wait := w.MinPull - time.Since(started); wait > 0 && len(msgs) < pullLimit {
			select {
			case <-ctx.Done():
			case <-time.After(wait):
			}
		}
	}
	return nil
}

// Handle applies one notification. Several topics map to one type (this
// camera sends motion on two topics): the type is active while any is.
func (w *Watcher) Handle(ctx context.Context, m Notification, now time.Time) {
	typ, active, ok := Classify(m)
	if !ok {
		return
	}
	if w.topics == nil {
		w.topics, w.open = map[string]bool{}, map[string]openEvent{}
	}
	w.topics[typ+"|"+m.Topic] = active
	anyActive := false
	for k, v := range w.topics {
		if v && strings.HasPrefix(k, typ+"|") {
			anyActive = true
		}
	}
	w.transition(ctx, typ, anyActive, now)
}

func (w *Watcher) transition(ctx context.Context, typ string, active bool, now time.Time) {
	ev, isOpen := w.open[typ]
	switch {
	case active && !isOpen:
		id, err := w.Store.OpenEvent(ctx, w.CameraID, typ, now, "")
		if err != nil {
			w.Log.Warn("store event", "err", err)
			return
		}
		ev = openEvent{id: id, start: now}
		w.open[typ] = ev
	case !active && isOpen:
		_ = w.Store.CloseEvent(ctx, ev.id, now)
		delete(w.open, typ)
	default:
		return
	}
	if w.Sink != nil {
		w.Sink(w.CameraID, typ, ev.id, active, now)
	}
}

func (w *Watcher) expire(ctx context.Context, now time.Time) {
	for typ, ev := range w.open {
		if now.Sub(ev.start) > w.MaxEvent {
			for k := range w.topics {
				if strings.HasPrefix(k, typ+"|") {
					delete(w.topics, k)
				}
			}
			w.transition(ctx, typ, false, now)
		}
	}
}

func (w *Watcher) closeAll(now time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for typ := range w.open {
		w.transition(ctx, typ, false, now)
	}
}
