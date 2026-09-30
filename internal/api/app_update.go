package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
)

// AppRelease describes the Android APK the server offers for in-app updates
// (scripts/publish-apk.sh writes <data_dir>/app/app.json next to the APK).
// The app compares VersionCode with its own and installs over itself; the
// APK is signed with the same key, so Android accepts it as an update.
type AppRelease struct {
	VersionCode int    `json:"version_code"`
	VersionName string `json:"version_name"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
	Notes       string `json:"notes,omitempty"`
}

var hexSHA256 = regexp.MustCompile(`^[0-9a-f]{64}$`)

func (s *Server) appDir() string { return filepath.Join(s.Cfg.DataDir, "app") }

// appRelease reads the published release; nil when none (or it is broken:
// a manifest that does not match its APK is never offered).
func (s *Server) appRelease() *AppRelease {
	b, err := os.ReadFile(filepath.Join(s.appDir(), "app.json"))
	if err != nil {
		return nil
	}
	var rel AppRelease
	if json.Unmarshal(b, &rel) != nil || rel.VersionCode <= 0 || !hexSHA256.MatchString(rel.SHA256) {
		return nil
	}
	st, err := os.Stat(filepath.Join(s.appDir(), "sentinela.apk"))
	if err != nil || st.Size() != rel.Size {
		return nil
	}
	return &rel
}

// appLatest: GET /api/app/latest → {"available":false} or the release.
func (s *Server) appLatest(w http.ResponseWriter, _ *http.Request) {
	rel := s.appRelease()
	if rel == nil {
		writeJSON(w, http.StatusOK, map[string]bool{"available": false})
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Available bool `json:"available"`
		*AppRelease
	}{true, rel})
}

// appAPK: GET /api/app/sentinela.apk streams the published APK (resumable).
func (s *Server) appAPK(w http.ResponseWriter, r *http.Request) {
	if s.appRelease() == nil {
		writeErr(w, http.StatusNotFound, "no app release published")
		return
	}
	w.Header().Set("Content-Type", "application/vnd.android.package-archive")
	w.Header().Set("Content-Disposition", `attachment; filename="sentinela.apk"`)
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFile(w, r, filepath.Join(s.appDir(), "sentinela.apk"))
}
