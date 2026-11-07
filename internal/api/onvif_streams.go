package api

import (
	"context"
	"net/http"
	"net/netip"
	"time"

	"github.com/LUKER2DW/sentinela/internal/auth/redact"
	"github.com/LUKER2DW/sentinela/internal/camera/onvif"
)

// onvifStreams: POST /api/cameras/onvif-streams (admin, home or tailnet)
// with host, ONVIF port and credentials. It asks the camera for its stream
// profiles, so the settings editor can add a camera of any brand without a
// vendor preset. The password is used for this request only and never
// returned or logged; the result carries no credentials.
func (s *Server) onvifStreams(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Host      string `json:"host"`
		ONVIFPort int    `json:"onvif_port"`
		User      string `json:"user"`
		Password  string `json:"password"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ip, err := netip.ParseAddr(body.Host)
	if err != nil || !(ip.IsPrivate() || ip.IsLinkLocalUnicast()) {
		writeErr(w, http.StatusBadRequest, "host must be a LAN IP address")
		return
	}
	if body.ONVIFPort <= 0 || body.ONVIFPort > 65535 {
		body.ONVIFPort = 80
	}
	if body.User == "" || body.Password == "" {
		writeErr(w, http.StatusBadRequest, "user and password are required")
		return
	}
	if !s.wizardMu.TryLock() {
		writeErr(w, http.StatusTooManyRequests, "a camera search or test is already running")
		return
	}
	defer s.wizardMu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	streams, err := onvif.NewClient(ip.String(), body.ONVIFPort, body.User, body.Password).Streams(ctx)
	if err != nil {
		writeErr(w, http.StatusBadGateway, redact.Secrets(err.Error(), body.Password))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"streams": streams})
}
