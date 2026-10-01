package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/LUKER2DW/sentinela/internal/push"
	"github.com/LUKER2DW/sentinela/internal/review"
	"github.com/LUKER2DW/sentinela/internal/store"
)

// Notification modes (Casa / Fora / Noite). Which event types notify:
//   - home:  only camera tampering (someone covered or moved it);
//   - night: alerts (person, line crossed, area entered, tampering);
//   - away:  everything, motion included.
const (
	ModeHome  = "home"
	ModeNight = "night"
	ModeAway  = "away"
)

const modeKey = "notify_mode"

// pushGap: one notification per camera per burst of events.
const pushGap = time.Minute

// ModeSettings is the stored notification mode.
type ModeSettings struct {
	Mode string `json:"mode"`
	// NightAuto switches Casa to Noite between NightFrom and NightTo (local time).
	NightAuto bool   `json:"night_auto"`
	NightFrom string `json:"night_from"`
	NightTo   string `json:"night_to"`
}

var defaultMode = ModeSettings{Mode: ModeAway, NightAuto: true, NightFrom: "23:00", NightTo: "06:00"}

func validHM(s string) bool {
	_, err := time.Parse("15:04", s)
	return err == nil
}

// Effective is the mode in force at local time now.
func (m ModeSettings) Effective(now time.Time) string {
	if m.Mode != ModeHome || !m.NightAuto {
		return m.Mode
	}
	from, err1 := time.Parse("15:04", m.NightFrom)
	to, err2 := time.Parse("15:04", m.NightTo)
	if err1 != nil || err2 != nil {
		return m.Mode
	}
	mins := now.Hour()*60 + now.Minute()
	f, t := from.Hour()*60+from.Minute(), to.Hour()*60+to.Minute()
	in := f <= mins && mins < t
	if f > t { // crosses midnight
		in = mins >= f || mins < t
	}
	if in {
		return ModeNight
	}
	return ModeHome
}

// Notifies reports whether an event type notifies in a mode.
func Notifies(mode, typ string) bool {
	switch mode {
	case ModeAway:
		return true
	case ModeNight:
		return review.Severity(typ) == review.Alert
	case ModeHome:
		return typ == "tamper"
	}
	return false
}

// EventTypes are the event types a user can turn notifications on or off for.
var EventTypes = []string{"human", "vehicle", "animal", "line", "intrusion", "tamper", "motion"}

// NotifyPrefs are one user's notification choices (Minha conta).
type NotifyPrefs struct {
	// FollowMode: also apply the house mode (Casa / Noite / Fora).
	FollowMode bool `json:"follow_mode"`
	// Types that notify this user (subset of EventTypes).
	Types []string `json:"types"`
}

func defaultPrefs() NotifyPrefs {
	return NotifyPrefs{FollowMode: true, Types: slices.Clone(EventTypes)}
}

// prefsOf returns the user's choices, or the defaults.
func prefsOf(u *store.User) NotifyPrefs {
	p := defaultPrefs()
	if u.Notify != "" {
		var got NotifyPrefs
		if json.Unmarshal([]byte(u.Notify), &got) == nil {
			p = got
		}
	}
	return p
}

// Wants reports whether the user asked to be told about typ in mode.
func (p NotifyPrefs) Wants(mode, typ string) bool {
	return slices.Contains(p.Types, typ) && (!p.FollowMode || Notifies(mode, typ))
}

type notifier struct {
	sender *push.Sender
	fcm    *push.FCM
	mu     sync.Mutex
	last   map[string]time.Time
	// streams are the Android app's open notification streams, by user.
	streams map[chan []byte]int64
	// An FCM wake-up contains only a random id. The event details remain here
	// for the signed-in phone to fetch, then expire after 15 minutes.
	pending map[string]pendingNotice
}

type pendingNotice struct {
	userID  int64
	payload []byte
	expires time.Time
}

const pendingNoticeTTL = 15 * time.Minute

func (n *notifier) putPending(userID int64, payload []byte) (string, error) {
	var nonce [18]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	id := base64.RawURLEncoding.EncodeToString(nonce[:])
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.pending == nil {
		n.pending = make(map[string]pendingNotice)
	}
	now := time.Now()
	for k, v := range n.pending {
		if now.After(v.expires) {
			delete(n.pending, k)
		}
	}
	// Bound memory if many messages arrive before their TTL ends.
	if len(n.pending) >= 1024 {
		oldest := ""
		for k, v := range n.pending {
			if oldest == "" || v.expires.Before(n.pending[oldest].expires) {
				oldest = k
			}
		}
		delete(n.pending, oldest)
	}
	n.pending[id] = pendingNotice{userID: userID, payload: payload, expires: now.Add(pendingNoticeTTL)}
	return id, nil
}

func (n *notifier) getPending(userID int64, id string) ([]byte, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	v, ok := n.pending[id]
	return v.payload, ok && v.userID == userID && time.Now().Before(v.expires)
}

// subscribe opens a notification stream for userID.
func (n *notifier) subscribe(userID int64) chan []byte {
	c := make(chan []byte, 8)
	n.mu.Lock()
	if n.streams == nil {
		n.streams = map[chan []byte]int64{}
	}
	n.streams[c] = userID
	n.mu.Unlock()
	return c
}

func (n *notifier) unsubscribe(c chan []byte) {
	n.mu.Lock()
	delete(n.streams, c)
	n.mu.Unlock()
}

// stream sends payload to userID's open streams; it reports how many.
func (n *notifier) stream(userID int64, payload []byte) int {
	n.mu.Lock()
	defer n.mu.Unlock()
	sent := 0
	for c, uid := range n.streams {
		if uid != userID {
			continue
		}
		select {
		case c <- payload:
			sent++
		default: // slow client: drop
		}
	}
	return sent
}

// recent reports whether key notified within pushGap, and marks it now.
func (n *notifier) recent(key string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.last == nil {
		n.last = map[string]time.Time{}
	}
	if time.Since(n.last[key]) < pushGap {
		return true
	}
	n.last[key] = time.Now()
	return false
}

func (s *Server) modeSettings(ctx context.Context) ModeSettings {
	m := defaultMode
	if raw := s.Store.Setting(ctx, modeKey, ""); raw != "" {
		_ = json.Unmarshal([]byte(raw), &m)
	}
	return m
}

func (s *Server) localNow() time.Time {
	if loc, err := time.LoadLocation(s.Cfg.Timezone); err == nil {
		return time.Now().In(loc)
	}
	return time.Now()
}

// notify tells every user who wants it about a new event: Web Push to
// their browsers and the stream of the Android app. Each user gets one
// notification per camera per burst (pushGap).
func (s *Server) notify(ctx context.Context, cameraID, typ string, eventID int64, at time.Time) {
	// With the object detector running, plain motion (trees, shadows, cars
	// on the street) stays on the timeline; people and objects in the
	// zones notify instead (ADR-0016).
	if typ == "motion" && s.detectorHealthy() && s.detection(ctx, cameraID).Enabled {
		return
	}
	mode := s.modeSettings(ctx).Effective(s.localNow())
	name := cameraID
	if c := s.Cfg.Camera(cameraID); c != nil {
		name = c.Name
	}
	loc := s.localNow().Location()
	msg := map[string]any{
		"title": fmt.Sprintf("%s · %s", eventTitle(typ), name),
		"body":  fmt.Sprintf("%s. Toque para ver a gravação.", at.In(loc).Format("15:04:05")),
		"tag":   "sentinela-" + cameraID,
		"url":   eventURL(cameraID, eventID, at),
	}
	// The picture is fetched by the phone from the server itself; it never
	// travels through the push service (ADR-0013).
	if s.hasSnapshot(store.Event{ID: eventID, CameraID: cameraID}) {
		msg["image"] = fmt.Sprintf("/api/events/%d/snapshot", eventID)
	}
	users, err := s.Store.ListUsers(ctx)
	if err != nil {
		s.Log.Warn("notify: list users", "err", err)
		return
	}
	for i := range users {
		u := &users[i]
		if u.Disabled || u.Pending || !u.CanSee(cameraID) || !prefsOf(u).Wants(mode, typ) {
			continue
		}
		if s.push.recent(fmt.Sprintf("%d/%s", u.ID, cameraID)) {
			continue
		}
		s.sendAll(ctx, u.ID, msg)
	}
}

// eventURL opens the recording at the event (a few seconds before it).
func eventURL(cameraID string, eventID int64, at time.Time) string {
	q := url.Values{"cam": {cameraID}, "t": {at.UTC().Add(-5 * time.Second).Format(time.RFC3339)}, "ev": {strconv.FormatInt(eventID, 10)}}
	return "/#/recordings?" + q.Encode()
}

func eventTitle(typ string) string {
	switch typ {
	case "motion":
		return "Movimento"
	case "human":
		return "Pessoa"
	case "line":
		return "Linha cruzada"
	case "intrusion":
		return "Entrada na área"
	case "tamper":
		return "Câmera mexida"
	case "vehicle":
		return "Veículo"
	case "animal":
		return "Animal"
	}
	return "Evento"
}

// sendAll sends msg to one user's browsers and apps. FCM gets only an opaque
// id; the app fetches msg from this server with its own session.
func (s *Server) sendAll(ctx context.Context, userID int64, msg map[string]any) int {
	payload, _ := json.Marshal(msg)
	sent := s.push.stream(userID, payload)
	if s.push.sender != nil {
		subs, err := s.Store.PushSubs(ctx, userID)
		if err != nil {
			s.Log.Warn("push: list subscriptions", "err", err)
		} else {
			for _, p := range subs {
				err := s.push.sender.Send(ctx, push.Subscription{Endpoint: p.Endpoint, P256dh: p.P256dh, Auth: p.Auth}, payload, 10*time.Minute)
				switch {
				case err == nil:
					sent++
					s.Store.MarkPushOK(ctx, p.ID)
				case errors.Is(err, push.ErrGone):
					_ = s.Store.DeletePushSub(ctx, 0, p.Endpoint)
				default:
					s.Log.Warn("push failed", "err", err)
				}
			}
		}
	}
	if s.push.fcm == nil {
		return sent
	}
	tokens, err := s.Store.FCMTokens(ctx, userID)
	if err != nil {
		s.Log.Warn("fcm: list tokens", "err", err)
		return sent
	}
	if len(tokens) == 0 {
		return sent
	}
	id, err := s.push.putPending(userID, payload)
	if err != nil {
		s.Log.Warn("fcm: create wake-up", "err", err)
		return sent
	}
	fcmSent := 0
	for _, token := range tokens {
		callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := s.push.fcm.Send(callCtx, token, map[string]string{"id": id})
		cancel()
		switch {
		case err == nil:
			fcmSent++
			s.Store.MarkFCMOK(ctx, token)
		case errors.Is(err, push.ErrUnregistered):
			_ = s.Store.DeleteFCMToken(ctx, 0, token)
		default:
			s.Log.Warn("fcm: send failed", "err", err)
		}
	}
	if fcmSent == 0 {
		s.push.mu.Lock()
		delete(s.push.pending, id)
		s.push.mu.Unlock()
	}
	sent += fcmSent
	return sent
}

// ---------- HTTP ----------

func (s *Server) pushInfo(w http.ResponseWriter, r *http.Request) {
	if s.push.sender == nil {
		writeErr(w, http.StatusServiceUnavailable, "notifications not available")
		return
	}
	subs, _ := s.Store.PushSubs(r.Context(), userFrom(r).ID)
	writeJSON(w, http.StatusOK, map[string]any{"public_key": s.push.sender.Keys.Public, "devices": len(subs)})
}

func (s *Server) pushSubscribe(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Endpoint string `json:"endpoint"`
		Keys     struct {
			P256dh string `json:"p256dh"`
			Auth   string `json:"auth"`
		} `json:"keys"`
		ExpirationTime any `json:"expirationTime"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.push.sender == nil {
		writeErr(w, http.StatusServiceUnavailable, "notifications not available")
		return
	}
	if !s.push.sender.Allowed(body.Endpoint) || len(body.Endpoint) > 1024 || body.Keys.P256dh == "" || body.Keys.Auth == "" ||
		len(body.Keys.P256dh) > 200 || len(body.Keys.Auth) > 100 {
		writeErr(w, http.StatusBadRequest, "invalid or unsupported push subscription")
		return
	}
	ua := r.UserAgent()
	if len(ua) > 200 {
		ua = ua[:200]
	}
	if err := s.Store.SavePushSub(r.Context(), userFrom(r).ID, body.Endpoint, body.Keys.P256dh, body.Keys.Auth, ua); err != nil {
		writeErr(w, http.StatusInternalServerError, "database error")
		return
	}
	s.audit(r, "push.subscribe", "", true, "")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) pushUnsubscribe(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Endpoint string `json:"endpoint"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.Store.DeletePushSub(r.Context(), userFrom(r).ID, body.Endpoint); err != nil {
		writeErr(w, http.StatusInternalServerError, "database error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) pushTest(w http.ResponseWriter, r *http.Request) {
	n := s.sendAll(r.Context(), userFrom(r).ID, map[string]any{
		"title": "Sentinela", "body": "Notificações funcionando neste aparelho.", "tag": "sentinela-test", "url": "/#/events",
	})
	writeJSON(w, http.StatusOK, map[string]int{"sent": n})
}

func (s *Server) getMode(w http.ResponseWriter, r *http.Request) {
	m := s.modeSettings(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"settings": m, "effective": m.Effective(s.localNow())})
}

func (s *Server) putMode(w http.ResponseWriter, r *http.Request) {
	var m ModeSettings
	if err := readJSON(r, &m); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if (m.Mode != ModeHome && m.Mode != ModeNight && m.Mode != ModeAway) || !validHM(m.NightFrom) || !validHM(m.NightTo) {
		writeErr(w, http.StatusBadRequest, "invalid mode or time")
		return
	}
	raw, _ := json.Marshal(m)
	err := s.Store.SetSetting(r.Context(), modeKey, string(raw))
	s.audit(r, "notify.mode", m.Mode, err == nil, "")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "database error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": m, "effective": m.Effective(s.localNow())})
}

// notifyStream is the Android app's notification channel: a long-lived
// Server-Sent Events stream of this user's notifications, kept open by a
// foreground service, when Firebase is unavailable (ADR-0020).
func (s *Server) notifyStream(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	// The listener's write timeout would cut the stream: lift it here.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	sess := sessionFrom(r)
	c := s.push.subscribe(userFrom(r).ID)
	defer s.push.unsubscribe(c)
	fmt.Fprint(w, "retry: 10000\n\n: ready\n\n")
	fl.Flush()
	ping := time.NewTicker(45 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			// Signed out, revoked or disabled meanwhile: close, so the app
			// learns it on its next connect.
			if _, _, err := s.Store.SessionByHash(r.Context(), sess.IDHash); err != nil {
				return
			}
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		case b := <-c:
			fmt.Fprintf(w, "event: notify\ndata: %s\n\n", b)
			fl.Flush()
		}
	}
}

// getNotifyPrefs returns the caller's notification choices.
func (s *Server) getNotifyPrefs(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"prefs": prefsOf(userFrom(r)), "types": EventTypes})
}

// putNotifyPrefs changes the caller's notification choices.
func (s *Server) putNotifyPrefs(w http.ResponseWriter, r *http.Request) {
	var p NotifyPrefs
	if err := readJSON(r, &p); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	clean := []string{}
	for _, t := range EventTypes {
		if slices.Contains(p.Types, t) {
			clean = append(clean, t)
		}
	}
	p.Types = clean
	raw, _ := json.Marshal(p)
	if err := s.Store.SetUserNotify(r.Context(), userFrom(r).ID, string(raw)); err != nil {
		writeErr(w, http.StatusInternalServerError, "database error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"prefs": p, "types": EventTypes})
}
