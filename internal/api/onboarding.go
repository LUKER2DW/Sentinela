package api

import (
	"net/http"
	"strings"

	"github.com/LUKER2DW/sentinela/internal/auth"
)

// First-run setup guide. Creating the first admin (POST /api/setup) marks
// it pending; the admin then walks through cameras, recording, Cloud,
// notifications and two-step login, and may skip any step. Installs that
// already had users never see it (the setting is absent = done). It can be
// reopened from the System page.

const onboardingKey = "onboarding"

// onboardingPending reports whether the guide should open by itself.
func (s *Server) onboardingPending(r *http.Request) bool {
	return s.Store.Setting(r.Context(), onboardingKey, "done") == "pending"
}

type onboardingCamera struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Vendor  string `json:"vendor"`
	Control bool   `json:"control"`
	Record  bool   `json:"recording"`
}

// getOnboarding: GET /api/onboarding (admin). What the server already has,
// so each step can say what is done and what is left.
func (s *Server) getOnboarding(w http.ResponseWriter, r *http.Request) {
	cams := make([]onboardingCamera, 0, len(s.Cfg.Cameras))
	for _, c := range s.Cfg.Cameras {
		cams = append(cams, onboardingCamera{ID: c.ID, Name: c.Name, Vendor: c.Vendor, Control: c.HasDVRIP(), Record: c.Recording()})
	}
	u := userFrom(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"pending":          s.onboardingPending(r),
		"cameras":          cams,
		"timezone":         s.Cfg.Timezone,
		"max_disk_percent": s.Cfg.Recording.MaxDiskPercent,
		"retention_days":   s.Cfg.Recording.DefaultRetentionDays,
		"volume_marker":    s.Cfg.Recording.VolumeMarker != "none",
		"cloud":            s.nas != nil,
		"nas_host":         s.Cfg.NASHost,
		"push":             s.push.sender != nil,
		"hosting":          s.hosting.proxy != nil,
		"totp":             u.TOTP,
		"zone":             zoneFrom(r),
		"tailnet_url":      s.Cfg.Remote.TailnetURL,
		"public_url":       publicOrigin(s.Cfg.Remote.Public),
		"proxy_url":        strings.TrimRight(s.Cfg.Remote.Proxy.URL, "/"),
		"public_pinned":    s.Cfg.Remote.Public.SelfSigned,
	})
}

// putOnboarding: PUT /api/onboarding {"pending": false} closes the guide
// (finished or skipped); true reopens it.
func (s *Server) putOnboarding(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Pending bool `json:"pending"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	v := "done"
	if body.Pending {
		v = "pending"
	}
	if err := s.Store.SetSetting(r.Context(), onboardingKey, v); err != nil {
		writeErr(w, http.StatusInternalServerError, "database error")
		return
	}
	s.audit(r, "setup.guide", v, true, "")
	writeJSON(w, http.StatusOK, map[string]bool{"pending": body.Pending})
}

// onboardingFor adds the flag to /api/me: admins with a pending guide.
func (s *Server) onboardingFor(r *http.Request) bool {
	u := userFrom(r)
	return u != nil && u.Role == auth.RoleAdmin && s.onboardingPending(r)
}
