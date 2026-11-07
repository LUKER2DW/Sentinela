package onvif

import (
	"context"
	"log/slog"
	"strconv"
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
		reason := redact.Secrets(errString(err), w.Client.Password)
		w.Log.Warn("onvif events: session ended", "camera", w.CameraID, "err", reason)
		if delivered {
			// The session worked: reconnect promptly instead of waiting a
			// backoff left by earlier failures.
			backoff, wait = minBackoff, time.Second
		} else {
			w.health(false, reason)
			backoff = min(backoff*2, 2*time.Minute)
		}
		select {
		case <-ctx.Done():
		case <-time.After(wait):
		}
	}
	w.closeAll(time.Now())
}

// pullPointScan is how many numbered pull points pullPoint tries when the
// camera refuses a new subscription and none is remembered. This camera has
// 32 slots (/event_service/0..31) and never frees one (docs/CAMERA.md).
const pullPointScan = 32

func (w *Watcher) settingKey() string { return "onvif.pullpoint." + w.CameraID }

// pullPoint returns a working subscription. It reuses the remembered one
// first: this camera never frees a subscription (Unsubscribe and expiry are
// ignored), every PullMessages keeps it alive, and it refuses new ones once
// its 32 slots are used. adopted reports a reused subscription, whose first
// batch may hold stale messages.
func (w *Watcher) pullPoint(ctx context.Context) (sub *Subscription, adopted bool, err error) {
	try := func(u string) bool {
		s := &Subscription{c: w.Client, URL: u}
		if _, err := s.Pull(ctx, time.Second); err != nil {
			return false
		}
		sub = s
		return true
	}
	if u := w.Store.Setting(ctx, w.settingKey(), ""); u != "" && try(u) {
		return sub, true, nil
	}
	sub, err = w.Client.Subscribe(ctx)
	if err == nil {
		_ = w.Store.SetSetting(ctx, w.settingKey(), sub.URL)
		return sub, false, nil
	}
	if !strings.Contains(err.Error(), "ResourceUnknownFault") {
		return nil, false, err
	}
	// Every slot is taken (by an earlier run): adopt one of them.
	for n := pullPointScan - 1; n >= 0 && ctx.Err() == nil; n-- {
		if try(w.Client.Base + "/event_service/" + strconv.Itoa(n)) {
			_ = w.Store.SetSetting(ctx, w.settingKey(), sub.URL)
			w.Log.Info("onvif events: reusing a pull point the camera kept", "camera", w.CameraID, "slot", n)
			return sub, true, nil
		}
	}
	return nil, false, err
}

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
	// No Unsubscribe and no Renew: this camera ignores both (it answers
	// ResourceUnknownFault) and a pull keeps the subscription alive, so the
	// same one is kept for as long as pulls work, across restarts too.
	sub, adopted, err := w.pullPoint(ctx)
	if err != nil {
		return false, err
	}
	w.Log.Info("onvif events: subscribed", "camera", w.CameraID, "reused", adopted)
	for ctx.Err() == nil {
		started := time.Now()
		msgs, err := sub.Pull(ctx, 10*time.Second)
		if err != nil {
			return delivered, err
		}
		delivered = true
		w.health(true, "")
		now := time.Now()
		if adopted {
			// Drop what queued while nobody was pulling.
			msgs, adopted = nil, false
		}
		for _, m := range msgs {
			w.Handle(ctx, m, now)
		}
		w.expire(ctx, now)
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
