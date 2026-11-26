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
// the API: live push, snapshots, notifications). eventID is 0 for an event
// that was not stored because the camera opened too many of its type
// (OpenBurst): only its live state counts (the motion that wakes the
// detector). Such an event is stored at its next active message once its
// type is allowed one again, and the Sink then hears it start with its id.
type Sink func(cameraID, typ string, eventID int64, active bool, at time.Time)

// Watcher keeps a PullPoint subscription alive and writes events to the store.
//
// The camera's replies are plain HTTP and never authenticated, so what one
// can cost is bounded: one pull per MinPull, at most pullLimit messages per
// pull, topics of at most maxTopicLen bytes, a few active topics per event
// type, and per event type at most OpenBurst stored events at once (then
// one per OpenEvery).
type Watcher struct {
	CameraID string
	Client   *Client
	Store    *store.Store
	Log      *slog.Logger
	Sink     Sink
	// MaxEvent closes an event that never received an "inactive" message.
	MaxEvent time.Duration
	// MinPull is the shortest time between two pulls (default 1 s), full
	// batches included. This camera ignores the PullMessages timeout and
	// answers at once when it has nothing to say: pulling again right away
	// made ~45 requests a second, which starved its encoder (frames dropped
	// on every stream).
	MinPull time.Duration
	// CloseGrace is how long an event stays open after its type went
	// inactive (default 5 s when Run starts it): a re-trigger meanwhile
	// continues the same event instead of storing a new one, and the end
	// stored is when it went inactive.
	CloseGrace time.Duration
	// OpenBurst and OpenEvery bound the events one camera can store, per
	// event type (a busy motion scene does not use up tamper's): at most
	// OpenBurst at once, refilled by one every OpenEvery (defaults 20 and
	// 15 s). Beyond that, transitions still reach the Sink with event id 0,
	// and nothing is stored, pushed or notified.
	OpenBurst int
	OpenEvery time.Duration
	// Quiet, when set and true, keeps a motion event from opening: the
	// camera is moving under PTZ and sees its own movement.
	Quiet func(cameraID string) bool
	// Health, when set, hears whether events are flowing: ok after a
	// successful pull, not ok (with the redacted reason) when a session
	// ends. Called only when the state changes.
	Health func(cameraID string, ok bool, reason string)

	open    map[string]openEvent
	healthy *bool
	topics  map[string]map[string]struct{} // type -> topics active now
	allow   map[string]*allowance          // type -> its stored-event allowance
}

// allowance is one event type's token bucket (OpenBurst, OpenEvery).
// Types come from Classify, so there are at most four.
type allowance struct {
	tokens  float64   // stored events still allowed now
	refill  time.Time // when tokens was last refilled
	limited bool      // an event was not stored; logged once until full again
}

// maxActiveTopics is how many active topics are kept per event type. The
// type is active while any is, so a topic past the cap changes nothing;
// cameras send one or two per type.
const maxActiveTopics = 16

type openEvent struct {
	id    int64 // 0: not stored (OpenBurst)
	start time.Time
	ended time.Time // the type went inactive then; closed after CloseGrace
}

// Run subscribes and pulls until ctx ends, re-subscribing with backoff.
func (w *Watcher) Run(ctx context.Context) {
	if w.MaxEvent == 0 {
		w.MaxEvent = 10 * time.Minute
	}
	if w.MinPull == 0 {
		w.MinPull = time.Second
	}
	if w.CloseGrace == 0 {
		w.CloseGrace = 5 * time.Second
	}
	w.init()
	_ = w.Store.CloseDanglingEvents(ctx, w.CameraID, time.Minute)
	const minBackoff = 5 * time.Second
	backoff := minBackoff
	for ctx.Err() == nil {
		delivered, err := w.session(ctx)
		if ctx.Err() != nil {
			break
		}
		// An event waiting out its grace ends now: no re-trigger can come
		// before the next session.
		w.settle(ctx, time.Now().Add(w.CloseGrace))
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
	if u := w.Store.Setting(ctx, w.settingKey(), ""); u != "" {
		// Only a pull point on the camera's current address: after the
		// address changed (or the id went to another camera) the old host
		// must not get this camera's credentials, nor speak for it.
		if !w.Client.onBase(u) {
			w.Log.Info("onvif events: forgetting a pull point of a previous camera address", "camera", w.CameraID)
			_ = w.Store.SetSetting(ctx, w.settingKey(), "")
		} else if try(u) {
			return sub, true, nil
		}
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
		w.settle(ctx, now)
		w.expire(ctx, now)
		// Every pull keeps the pace, a full batch too: whatever the camera
		// still holds comes with the next pull.
		if wait := w.MinPull - time.Since(started); wait > 0 {
			select {
			case <-ctx.Done():
			case <-time.After(wait):
			}
		}
	}
	return delivered, nil
}

func (w *Watcher) init() {
	if w.open == nil {
		w.open = map[string]openEvent{}
	}
	if w.topics == nil {
		w.topics = map[string]map[string]struct{}{}
	}
	if w.allow == nil {
		w.allow = map[string]*allowance{}
	}
	if w.OpenBurst <= 0 {
		w.OpenBurst = 20
	}
	if w.OpenEvery <= 0 {
		w.OpenEvery = 15 * time.Second
	}
}

// Handle applies one notification. Several topics map to one type (this
// camera sends motion on two topics): the type is active while any is.
// A topic longer than maxTopicLen is ignored: none is that long, and the
// active ones are kept between pulls.
func (w *Watcher) Handle(ctx context.Context, m Notification, now time.Time) {
	if len(m.Topic) > maxTopicLen {
		return
	}
	typ, active, ok := Classify(m)
	if !ok {
		return
	}
	w.init()
	// An event whose grace has run out ends first: a pull can return up to
	// its 10 s timeout after the type went inactive, and a message then
	// starts a new event instead of continuing that one.
	w.settle(ctx, now)
	set := w.topics[typ]
	switch {
	case active && set == nil:
		w.topics[typ] = map[string]struct{}{m.Topic: {}}
	case active && len(set) < maxActiveTopics:
		set[m.Topic] = struct{}{}
	case !active:
		delete(set, m.Topic)
		if len(set) == 0 {
			delete(w.topics, typ)
		}
	}
	w.transition(ctx, typ, len(w.topics[typ]) > 0, now)
	w.settle(ctx, now)
}

func (w *Watcher) transition(ctx context.Context, typ string, active bool, now time.Time) {
	ev, isOpen := w.open[typ]
	switch {
	case active && isOpen:
		// A re-trigger within the grace continues the same event.
		ev.ended = time.Time{}
		if ev.id == 0 && !w.quiet(typ) && w.allowOpen(typ, now) {
			// Not stored when it began (no allowance left): stored now,
			// from its start, and announced with its id.
			if id, err := w.Store.OpenEvent(ctx, w.CameraID, typ, ev.start, ""); err != nil {
				w.warn("store event", "err", err)
			} else {
				ev.id = id
				w.sink(typ, id, true, ev.start)
			}
		}
		w.open[typ] = ev
	case active:
		if w.quiet(typ) {
			return
		}
		ev = openEvent{start: now}
		if w.allowOpen(typ, now) {
			id, err := w.Store.OpenEvent(ctx, w.CameraID, typ, now, "")
			if err != nil {
				w.warn("store event", "err", err)
				return
			}
			ev.id = id
		}
		w.open[typ] = ev
		w.sink(typ, ev.id, true, now)
	case isOpen && ev.ended.IsZero():
		ev.ended = now // closed by settle once the grace has passed
		w.open[typ] = ev
	}
}

// quiet: motion of a camera moving under PTZ is its own movement.
func (w *Watcher) quiet(typ string) bool {
	return typ == "motion" && w.Quiet != nil && w.Quiet(w.CameraID)
}

// allowOpen takes one stored event from the type's allowance (a token
// bucket of OpenBurst, refilled by one every OpenEvery). The first refusal
// is logged; the next only after the allowance was full again.
func (w *Watcher) allowOpen(typ string, now time.Time) bool {
	burst := float64(w.OpenBurst)
	a := w.allow[typ]
	if a == nil {
		a = &allowance{tokens: burst, refill: now}
		w.allow[typ] = a
	}
	if d := now.Sub(a.refill); d > 0 {
		a.tokens = min(burst, a.tokens+float64(d)/float64(w.OpenEvery))
		a.refill = now
	}
	if a.tokens >= burst {
		a.limited = false
	}
	if a.tokens < 1 {
		if !a.limited {
			a.limited = true
			w.warn("onvif events: the camera opens events too fast; not storing more for now", "camera", w.CameraID, "type", typ)
		}
		return false
	}
	a.tokens--
	return true
}

// settle ends the events whose type stayed inactive for CloseGrace, at
// the time they went inactive.
func (w *Watcher) settle(ctx context.Context, now time.Time) {
	for typ, ev := range w.open {
		if !ev.ended.IsZero() && now.Sub(ev.ended) >= w.CloseGrace {
			w.end(ctx, typ, ev, ev.ended)
		}
	}
}

func (w *Watcher) end(ctx context.Context, typ string, ev openEvent, at time.Time) {
	if ev.id != 0 {
		_ = w.Store.CloseEvent(ctx, ev.id, at)
	}
	delete(w.open, typ)
	w.sink(typ, ev.id, false, at)
}

func (w *Watcher) sink(typ string, id int64, active bool, at time.Time) {
	if w.Sink != nil {
		w.Sink(w.CameraID, typ, id, active, at)
	}
}

func (w *Watcher) warn(msg string, args ...any) {
	if w.Log != nil {
		w.Log.Warn(msg, args...)
	}
}

func (w *Watcher) expire(ctx context.Context, now time.Time) {
	for typ, ev := range w.open {
		if now.Sub(ev.start) > w.MaxEvent {
			delete(w.topics, typ)
			at := now
			if !ev.ended.IsZero() {
				at = ev.ended
			}
			w.end(ctx, typ, ev, at)
		}
	}
}

func (w *Watcher) closeAll(now time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for typ, ev := range w.open {
		at := now
		if !ev.ended.IsZero() {
			at = ev.ended
		}
		w.end(ctx, typ, ev, at)
	}
}
