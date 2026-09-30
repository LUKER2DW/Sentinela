package api

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// Profile photos (round 6). The browser crops and shrinks the picture to a
// small square before sending it, so the server only checks and stores
// bytes: no image decoding here. JPEG, PNG or WebP only (never SVG, which
// could carry script).
const maxAvatar = 512 << 10

var avatarTypes = map[string]bool{"image/jpeg": true, "image/png": true, "image/webp": true}

func (s *Server) avatarPath(id int64) string {
	return filepath.Join(s.Cfg.DataDir, "avatars", strconv.FormatInt(id, 10)+".img")
}

// putAvatar: POST /api/me/avatar, body = the image.
func (s *Server) putAvatar(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxAvatar))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeErr(w, http.StatusRequestEntityTooLarge, "picture too large")
			return
		}
		writeErr(w, http.StatusBadRequest, "could not read the picture")
		return
	}
	if !avatarTypes[http.DetectContentType(b)] {
		writeErr(w, http.StatusUnsupportedMediaType, "use a JPEG, PNG or WebP picture")
		return
	}
	p := s.avatarPath(u.ID)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		writeErr(w, http.StatusInternalServerError, "storage error")
		return
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o640); err != nil || os.Rename(tmp, p) != nil {
		_ = os.Remove(tmp)
		writeErr(w, http.StatusInternalServerError, "storage error")
		return
	}
	s.audit(r, "user.avatar", u.Username, true, "")
	writeJSON(w, http.StatusOK, map[string]int64{"version": time.Now().Unix()})
}

// deleteAvatar: DELETE /api/me/avatar goes back to the initial letter.
func (s *Server) deleteAvatar(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	if err := os.Remove(s.avatarPath(u.ID)); err != nil && !errors.Is(err, os.ErrNotExist) {
		writeErr(w, http.StatusInternalServerError, "storage error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// getAvatar: GET /api/avatars/{username}; 404 when the person has none.
func (s *Server) getAvatar(w http.ResponseWriter, r *http.Request) {
	u, err := s.Store.UserByName(r.Context(), r.PathValue("username"))
	if err != nil || u == nil {
		writeErr(w, http.StatusNotFound, "no picture")
		return
	}
	b, err := os.ReadFile(s.avatarPath(u.ID))
	if err != nil {
		writeErr(w, http.StatusNotFound, "no picture")
		return
	}
	ct := http.DetectContentType(b)
	if !avatarTypes[ct] {
		writeErr(w, http.StatusNotFound, "no picture")
		return
	}
	st, _ := os.Stat(s.avatarPath(u.ID))
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, "", st.ModTime(), bytes.NewReader(b))
}
