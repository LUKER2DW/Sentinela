package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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

type notifier struct {
	sender *push.Sender
	mu     sync.Mutex
	last   map[string]time.Time
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

// notify sends a Web Push for a new event when the mode asks for it.
func (s *Server) notify(ctx context.Context, cameraID, typ string, eventID int64, at time.Time) {
	if s.push.sender == nil || !Notifies(s.modeSettings(ctx).Effective(s.localNow()), typ) {
		return
	}
	// With the object detector running, plain motion (trees, shadows, cars
	// on the street) stays on the timeline; people and objects in the
	// zones notify instead (ADR-0016).
	if typ == "motion" && s.detectorHealthy() && s.detection(ctx, cameraID).Enabled {
		return
	}
	s.push.mu.Lock()
	if s.push.last == nil {
		s.push.last = map[string]time.Time{}
	}
	if time.Since(s.push.last[cameraID]) < pushGap {
		s.push.mu.Unlock()
		return
	}
	s.push.last[cameraID] = time.Now()
	s.push.mu.Unlock()

	name := cameraID
	if c := s.Cfg.Camera(cameraID); c != nil {
		name = c.Name
	}
	loc := s.localNow().Location()
	msg := map[string]any{
		"title": fmt.Sprintf("%s · %s", eventTitle(typ), name),
		"body":  fmt.Sprintf("%s. Toque para ver.", at.In(loc).Format("15:04:05")),
		"tag":   "sentinela-" + cameraID,
		"url":   "/#/events",
	}
	// The picture is fetched by the phone from the server, over the tailnet;
	// it never travels through the push service (ADR-0013).
	if s.hasSnapshot(store.Event{ID: eventID, CameraID: cameraID}) {
		msg["image"] = fmt.Sprintf("/api/events/%d/snapshot", eventID)
	}
	s.sendAll(ctx, 0, cameraID, msg)
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

// sendAll pushes msg to every subscription (of userID, or everyone when 0).
// With a cameraID, users who may not see that camera are skipped.
func (s *Server) sendAll(ctx context.Context, userID int64, cameraID string, msg map[string]any) int {
	subs, err := s.Store.PushSubs(ctx, userID)
	if err != nil {
		s.Log.Warn("push: list subscriptions", "err", err)
		return 0
	}
	allowed := map[int64]bool{}
	if cameraID != "" {
		users, err := s.Store.ListUsers(ctx)
		if err != nil {
			s.Log.Warn("push: list users", "err", err)
			return 0
		}
		for _, u := range users {
			allowed[u.ID] = u.CanSee(cameraID)
		}
	}
	payload, _ := json.Marshal(msg)
	sent := 0
	for _, p := range subs {
		if cameraID != "" && !allowed[p.UserID] {
			continue
		}
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
	return sent
}

// ---------- HTTP ----------

func (s *Server) pushInfo(w http.ResponseWriter, r *http.Request) {
	if s.push.sender == nil {
		writeErr(w, http.StatusServiceUnavailable, "notifications not available")
		return
	}
	subs, _ := s.Store.PushSubs(r.Context(), userFrom(r).ID)
	// ntfy_url: the relay the Android app registers with (empty = none).
	writeJSON(w, http.StatusOK, map[string]any{"public_key": s.push.sender.Keys.Public, "devices": len(subs), "ntfy_url": s.Cfg.Ntfy.PublicURL})
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
	if s.push.sender == nil {
		writeErr(w, http.StatusServiceUnavailable, "notifications not available")
		return
	}
	n := s.sendAll(r.Context(), userFrom(r).ID, "", map[string]any{
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
