package onvif

import (
	"context"
	"errors"
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
	// Quiet, when set and true, keeps a motion event from opening: the
	// camera is moving under PTZ and sees its own movement.
	Quiet func(cameraID string) bool
	// Health, when set, hears whether events are flowing: ok after a
	// successful pull, not ok (with the redacted reason) when a session
	// ends. Called only when the state changes.
	Health func(cameraID string, ok bool, reason string)

	open    map[string]openEvent
	healthy *bool
	topics  map[string]bool // "type|topic" -> last active state
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
	const minBackoff = 5 * time.Second
	backoff := minBackoff
	for ctx.Err() == nil {
		delivered, err := w.session(ctx)
		if ctx.Err() != nil {
			break
		}
		wait := backoff
		switch {
		case errors.Is(err, errRotate):
			// Planned: the camera refused Renew, the subscription was used
			// until just before it expired. Subscribe again right away.
			w.Log.Debug("onvif events: subscription rotated", "camera", w.CameraID)
			backoff, wait = minBackoff, 0
		case delivered:
			// The session worked: reconnect promptly instead of waiting a
			// backoff left by earlier failures.
			backoff, wait = minBackoff, time.Second
			fallthrough
		default:
			reason := redact.Secrets(errString(err), w.Client.Password)
			w.Log.Warn("onvif events: session ended", "camera", w.CameraID, "err", reason)
			if !delivered {
				w.health(false, reason)
				backoff = min(backoff*2, 2*time.Minute)
			}
		}
		if wait > 0 {
			select {
			case <-ctx.Done():
			case <-time.After(wait):
			}
		}
	}
	w.closeAll(time.Now())
}

// errRotate ends a session whose subscription could not be renewed, just
// before the camera drops it.
var errRotate = errors.New("onvif: subscription rotated")

// subscriptionTTL is the InitialTerminationTime asked in Subscribe.
const subscriptionTTL = 120 * time.Second

func (w *Watcher) health(ok bool, reason string) {
	if w.Health == nil || (w.healthy != nil && *w.healthy == ok) {
		return
	}
	w.healthy = &ok
	w.Health(w.CameraID, ok, reason)
}

func errString(err error) string {
	if err == nil {
		return "closed"
	}
	return err.Error()
}

// session runs one subscription; delivered reports at least one good pull.
func (w *Watcher) session(ctx context.Context) (delivered bool, err error) {
	sub, err := w.Client.Subscribe(ctx)
	if err != nil {
		return false, err
	}
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = sub.Unsubscribe(c)
	}()
	w.Log.Info("onvif events: subscribed", "camera", w.CameraID)
	subscribed := time.Now()
	lastRenew := subscribed
	// expires: when the camera drops the subscription if Renew keeps
	// failing (this camera answers ResourceUnknownFault to Renew).
	expires := subscribed.Add(subscriptionTTL)
	for ctx.Err() == nil {
		started := time.Now()
		msgs, err := sub.Pull(ctx, 10*time.Second)
		if err != nil {
			return delivered, err
		}
		delivered = true
		w.health(true, "")
		now := time.Now()
		for _, m := range msgs {
			w.Handle(ctx, m, now)
		}
		w.expire(ctx, now)
		if now.Sub(lastRenew) > 50*time.Second {
			if err := sub.Renew(ctx); err == nil {
				expires = now.Add(subscriptionTTL)
			} else if ctx.Err() == nil {
				w.Log.Debug("onvif events: renew refused", "camera", w.CameraID, "err", redact.Secrets(err.Error(), w.Client.Password))
			}
			lastRenew = now
		}
		if time.Until(expires) < 15*time.Second {
			return true, errRotate
		}
		// A full batch may have more waiting; anything else keeps the pace.
		if wait := w.MinPull - time.Since(started); wait > 0 && len(msgs) < pullLimit {
			select {
			case <-ctx.Done():
			case <-time.After(wait):
			}
		}
	}
	return delivered, nil
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
		if typ == "motion" && w.Quiet != nil && w.Quiet(w.CameraID) {
			return
		}
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
