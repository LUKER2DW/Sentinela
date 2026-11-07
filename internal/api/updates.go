package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"
)

// Automatic server updates (ADR-0023), switched from System → Atualizações.
// The API cannot touch the host: it only writes preferences and a "check
// now" request under data_dir/updates, which the host's sentinela-update
// units read (scripts/auto-update.sh runs as root there). Where releases
// come from is not settable here: only root sets it in
// /etc/sentinela/auto-update.env, so even a compromised dashboard cannot
// point the server at someone else's releases.

// UpdateSettings is data_dir/updates/settings.json.
type UpdateSettings struct {
	Enabled bool   `json:"enabled"`
	At      string `json:"at"` // HH:MM, server local time
	// Major also installs new major versions (otherwise only notified).
	Major bool `json:"major"`
	// ReplaceManual lets a release replace a build deployed from a computer.
	ReplaceManual bool `json:"replace_manual"`
}

var hhmm = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

func (s *Server) updatesDir() string { return filepath.Join(s.Cfg.DataDir, "updates") }

func (s *Server) updateSettings() UpdateSettings {
	st := UpdateSettings{At: "04:00"}
	if b, err := os.ReadFile(filepath.Join(s.updatesDir(), "settings.json")); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	if !hhmm.MatchString(st.At) {
		st.At = "04:00"
	}
	return st
}

// writeUpdateFile writes one file of data_dir/updates atomically (0644:
// the host's root script reads it).
func (s *Server) writeUpdateFile(name string, b []byte) error {
	if err := os.MkdirAll(s.updatesDir(), 0o755); err != nil {
		return err
	}
	tmp := filepath.Join(s.updatesDir(), "."+name+".tmp")
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(s.updatesDir(), name))
}

// getUpdates: GET /api/updates (admin).
func (s *Server) getUpdates(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"version":  Version,
		"settings": s.updateSettings(),
		"last":     hostJSON(s.Cfg.UpdateStatusFile),
		// The host units are installed by install-server.sh / deploy.sh;
		// without them (dev machines) the settings have no effect.
		"host": fileExists(s.Cfg.UpdateUnitsMarker),
	})
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

// putUpdates: PUT /api/updates (admin, home or tailnet).
func (s *Server) putUpdates(w http.ResponseWriter, r *http.Request) {
	var body UpdateSettings
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if !hhmm.MatchString(body.At) {
		writeErr(w, http.StatusBadRequest, "time must be HH:MM")
		return
	}
	b, _ := json.Marshal(body)
	err := s.writeUpdateFile("settings.json", b)
	s.audit(r, "updates.settings", "", err == nil, string(b))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not save the settings")
		return
	}
	writeJSON(w, http.StatusOK, body)
}

// checkUpdates: POST /api/updates/check (admin, home or tailnet) asks the
// host to check (and install) the latest release now.
func (s *Server) checkUpdates(w http.ResponseWriter, r *http.Request) {
	if !s.loginRL.Allow("update-check") {
		writeErr(w, http.StatusTooManyRequests, "wait a minute before checking again")
		return
	}
	err := s.writeUpdateFile("request", []byte(strconv.FormatInt(time.Now().Unix(), 10)+"\n"))
	s.audit(r, "updates.check", "", err == nil, "")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not ask the server")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"requested": true})
}
