package api

import (
	"context"
	"encoding/hex"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/LUKER2DW/sentinela/internal/auth"
	"github.com/LUKER2DW/sentinela/internal/store"
)

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{3,32}$`)

type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) setupStatus(w http.ResponseWriter, r *http.Request) {
	n, err := s.Store.CountUsers(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "database error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"needs_setup": n == 0, "version": Version})
}

// setup creates the first admin. It only works while no user exists.
func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	if !s.loginRL.Allow("setup:" + clientIP(r)) {
		writeErr(w, http.StatusTooManyRequests, "too many attempts, wait a minute")
		return
	}
	var c credentials
	if err := readJSON(r, &c); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if !usernamePattern.MatchString(c.Username) {
		writeErr(w, http.StatusBadRequest, "username: 3-32 letters, digits, '.', '_' or '-'")
		return
	}
	hash, err := auth.HashPassword(c.Password)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	id, err := s.Store.CreateFirstUser(r.Context(), c.Username, hash)
	if err != nil {
		writeErr(w, http.StatusConflict, "setup already completed")
		return
	}
	s.Log.Info("first admin created", "user", c.Username)
	_ = s.Store.Audit(r.Context(), id, c.Username, "setup", c.Username, true, "")
	s.startSession(w, r, id)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if !s.loginRL.Allow("login:" + ip) {
		writeErr(w, http.StatusTooManyRequests, "too many attempts, wait a minute")
		return
	}
	var c credentials
	if err := readJSON(r, &c); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	u, err := s.Store.UserByName(r.Context(), strings.TrimSpace(c.Username))
	if err != nil {
		auth.VerifyDummy(c.Password)
		_ = s.Store.Audit(r.Context(), 0, c.Username, "login", ip, false, "unknown user")
		writeErr(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	if !auth.VerifyPassword(u.PWHash, c.Password) {
		_ = s.Store.Audit(r.Context(), u.ID, u.Username, "login", ip, false, "bad password")
		writeErr(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	_ = s.Store.Audit(r.Context(), u.ID, u.Username, "login", ip, true, "")
	s.startSession(w, r, u.ID)
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, userID int64) {
	tok, csrf := auth.NewToken(), auth.NewToken()
	exp := time.Now().Add(SessionTTL)
	ua := r.UserAgent()
	if len(ua) > 200 {
		ua = ua[:200]
	}
	if err := s.Store.CreateSession(r.Context(), auth.HashToken(tok), userID, csrf, exp, ua); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not create session")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: tok, Path: "/", Expires: exp, HttpOnly: true,
		Secure: s.Cfg.SecureCookies || isHTTPS(r), SameSite: http.SameSiteStrictMode})
	u, _ := s.Store.UserByID(r.Context(), userID)
	writeJSON(w, http.StatusOK, map[string]any{"user": u, "csrf": csrf})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if sess := sessionFrom(r); sess != nil {
		_ = s.Store.DeleteSession(r.Context(), sess.IDHash)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true,
		Secure: s.Cfg.SecureCookies || isHTTPS(r), SameSite: http.SameSiteStrictMode})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// mySessions lists the caller's devices (Account page, Stage B2).
func (s *Server) mySessions(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.UserSessions(r.Context(), userFrom(r).ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "database error")
		return
	}
	cur := hex.EncodeToString(sessionFrom(r).IDHash[:8])
	for i := range list {
		list[i].Current = list[i].ID == cur
	}
	writeJSON(w, http.StatusOK, list)
}

// revokeSession signs one of the caller's devices out.
func (s *Server) revokeSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if len(id) != 16 {
		writeErr(w, http.StatusBadRequest, "invalid session id")
		return
	}
	err := s.Store.DeleteUserSession(r.Context(), userFrom(r).ID, id)
	s.audit(r, "session.revoke", id, err == nil, "")
	if err != nil {
		writeErr(w, http.StatusNotFound, "session not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"user": userFrom(r), "csrf": sessionFrom(r).CSRF})
}

func (s *Server) changeOwnPassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	u := userFrom(r)
	if !s.loginRL.Allow("pw:"+strconv.FormatInt(u.ID, 10)) || !auth.VerifyPassword(u.PWHash, body.Current) {
		writeErr(w, http.StatusForbidden, "current password is wrong")
		return
	}
	hash, err := auth.HashPassword(body.New)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.Store.UpdateUserPassword(r.Context(), u.ID, hash); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not update password")
		return
	}
	s.audit(r, "user.password", u.Username, true, "self")
	s.startSession(w, r, u.ID) // old sessions were revoked; keep this device logged in
}

// ---------- users (admin) ----------

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.Store.ListUsers(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "database error")
		return
	}
	writeJSON(w, http.StatusOK, users)
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if !usernamePattern.MatchString(body.Username) || !auth.ValidRole(body.Role) {
		writeErr(w, http.StatusBadRequest, "invalid username or role")
		return
	}
	hash, err := auth.HashPassword(body.Password)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	id, err := s.Store.CreateUser(r.Context(), body.Username, hash, body.Role)
	if err != nil {
		writeErr(w, http.StatusConflict, "username already exists")
		return
	}
	s.audit(r, "user.create", body.Username, true, body.Role)
	u, _ := s.Store.UserByID(r.Context(), id)
	writeJSON(w, http.StatusCreated, u)
}

func (s *Server) targetUser(w http.ResponseWriter, r *http.Request) *store.User {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return nil
	}
	u, err := s.Store.UserByID(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "user not found")
		return nil
	}
	return u
}

// lastAdminGuard refuses changes that would leave no admin.
func (s *Server) lastAdminGuard(ctx context.Context, u *store.User) error {
	if u.Role != auth.RoleAdmin {
		return nil
	}
	n, err := s.Store.CountAdmins(ctx)
	if err != nil {
		return err
	}
	if n <= 1 {
		return errors.New("cannot remove the last admin")
	}
	return nil
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	u := s.targetUser(w, r)
	if u == nil {
		return
	}
	var body struct {
		Role     *string `json:"role"`
		Password *string `json:"password"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.Role != nil {
		if !auth.ValidRole(*body.Role) {
			writeErr(w, http.StatusBadRequest, "invalid role")
			return
		}
		if *body.Role != auth.RoleAdmin {
			if err := s.lastAdminGuard(r.Context(), u); err != nil {
				writeErr(w, http.StatusConflict, err.Error())
				return
			}
		}
		if err := s.Store.UpdateUserRole(r.Context(), u.ID, *body.Role); err != nil {
			writeErr(w, http.StatusInternalServerError, "database error")
			return
		}
		s.audit(r, "user.role", u.Username, true, *body.Role)
	}
	if body.Password != nil {
		hash, err := auth.HashPassword(*body.Password)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := s.Store.UpdateUserPassword(r.Context(), u.ID, hash); err != nil {
			writeErr(w, http.StatusInternalServerError, "database error")
			return
		}
		s.audit(r, "user.password", u.Username, true, "reset by admin")
	}
	u, _ = s.Store.UserByID(r.Context(), u.ID)
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	u := s.targetUser(w, r)
	if u == nil {
		return
	}
	if u.ID == userFrom(r).ID {
		writeErr(w, http.StatusConflict, "you cannot delete yourself")
		return
	}
	if err := s.lastAdminGuard(r.Context(), u); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	if err := s.Store.DeleteUser(r.Context(), u.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, "database error")
		return
	}
	s.audit(r, "user.delete", u.Username, true, "")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) listAudit(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Store.ListAudit(r.Context(), 200)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "database error")
		return
	}
	writeJSON(w, http.StatusOK, rows)
}
