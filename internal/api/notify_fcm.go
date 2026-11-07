package api

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// The app checks availability before choosing Firebase over its existing
// direct connection. The endpoint reveals no project credentials.
func (s *Server) fcmStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"available": s.fcmReady(r.Context())})
}

func (s *Server) fcmReady(ctx context.Context) bool {
	if s.push.fcm == nil {
		return false
	}
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return s.push.fcm.Ready(checkCtx) == nil
}

func validFCMToken(token string) bool {
	if len(token) < 20 || len(token) > 4096 {
		return false
	}
	return strings.IndexFunc(token, func(ch rune) bool {
		return ch < '!' || ch > '~'
	}) < 0
}

func (s *Server) registerFCM(w http.ResponseWriter, r *http.Request) {
	if !s.fcmReady(r.Context()) {
		writeErr(w, http.StatusServiceUnavailable, "Firebase notifications unavailable")
		return
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := readJSON(r, &body); err != nil || !validFCMToken(body.Token) {
		writeErr(w, http.StatusBadRequest, "invalid device token")
		return
	}
	ua := r.UserAgent()
	if len(ua) > 200 {
		ua = ua[:200]
	}
	checkCtx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	err := s.push.fcm.Validate(checkCtx, body.Token)
	cancel()
	if err != nil {
		s.Log.Warn("fcm: device validation failed", "err", err)
		writeErr(w, http.StatusServiceUnavailable, "Firebase could not validate this device")
		return
	}
	if err := s.Store.SaveFCMToken(r.Context(), userFrom(r).ID, sessionFrom(r).IDHash, body.Token, ua); err != nil {
		s.Log.Error("fcm: register device", "err", err)
		writeErr(w, http.StatusInternalServerError, "could not register device")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) unregisterFCM(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	if err := readJSON(r, &body); err != nil || !validFCMToken(body.Token) {
		writeErr(w, http.StatusBadRequest, "invalid device token")
		return
	}
	if err := s.Store.DeleteFCMToken(r.Context(), userFrom(r).ID, body.Token); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not unregister device")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) pendingNotification(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if len(id) != 24 {
		writeErr(w, http.StatusNotFound, "notification not found")
		return
	}
	payload, ok := s.push.getPending(userFrom(r).ID, id)
	if !ok {
		writeErr(w, http.StatusNotFound, "notification not found")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(payload)
}
