package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Cloudflare Tunnel from the dashboard (ADR-0025). As with Tailscale, the
// API never runs anything on the host: it leaves a request in
// <data>/cloudflared for scripts/cloudflared-agent.sh and reads the status
// it writes (/host/cloudflared.json). The tunnel token is written once,
// 0600, for the agent to move into a root-only file; it is never stored
// here, logged or returned.

// tunnelTokenRe finds a tunnel token in what the person pasted: the token
// alone, or Cloudflare's whole "cloudflared service install <token>" line.
var tunnelTokenRe = regexp.MustCompile(`eyJ[A-Za-z0-9+/=_-]{50,}`)

type cloudflaredStatus struct {
	Checked     string `json:"checked"`
	Installed   bool   `json:"installed"`
	Version     string `json:"version"`
	Configured  bool   `json:"configured"`
	Running     bool   `json:"running"`
	Connections int    `json:"connections"`
	Busy        bool   `json:"busy"`
	Action      string `json:"action"`
	Message     string `json:"message"`
}

func (s *Server) cloudflaredDir() string { return filepath.Join(s.Cfg.DataDir, "cloudflared") }

func (s *Server) readCloudflared() *cloudflaredStatus {
	b, err := os.ReadFile(s.hostFile("cloudflared.json"))
	if err != nil || len(b) > 64<<10 {
		return nil
	}
	var st cloudflaredStatus
	if json.Unmarshal(b, &st) != nil {
		return nil
	}
	return &st
}

// getCloudflared: GET /api/cloudflared (admin).
func (s *Server) getCloudflared(w http.ResponseWriter, _ *http.Request) {
	p := s.Cfg.Remote.Proxy
	writeJSON(w, http.StatusOK, map[string]any{
		"host":    fileExists(s.hostFile("cloudflared-units")),
		"status":  s.readCloudflared(),
		"pending": pendingRequest(filepath.Join(s.cloudflaredDir(), "request")),
		// Where the tunnel's public hostname must point (Cloudflare's
		// "Service" field), and the address saved in the settings.
		"service": "http://" + func() string {
			if p.Listen != "" {
				return p.Listen
			}
			return "127.0.0.1:7444"
		}(),
		"url":  strings.TrimRight(p.URL, "/"),
		"kind": p.Kind,
	})
}

// postCloudflared: POST /api/cloudflared (admin, home or tailnet)
// {"action": "status"|"connect"|"disconnect", "token": "..."}.
func (s *Server) postCloudflared(w http.ResponseWriter, r *http.Request) {
	if !fileExists(s.hostFile("cloudflared-units")) {
		writeErr(w, http.StatusConflict, "this server cannot run a Cloudflare Tunnel from the dashboard yet (run a deploy or scripts/setup-cloudflared.sh units)")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var body struct {
		Action string `json:"action"`
		Token  string `json:"token"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	token := ""
	switch body.Action {
	case "status", "disconnect":
	case "connect":
		if token = tunnelTokenRe.FindString(body.Token); token == "" || len(token) > 4096 {
			writeErr(w, http.StatusBadRequest, "that is not a tunnel token (copy the install command from Cloudflare; the token starts with eyJ)")
			return
		}
	default:
		writeErr(w, http.StatusBadRequest, "unknown action")
		return
	}
	if body.Action != "status" && !s.loginRL.Allow("cloudflared-"+body.Action) {
		writeErr(w, http.StatusTooManyRequests, "wait a minute before trying again")
		return
	}
	dir := s.cloudflaredDir()
	write := func(name string, b []byte, mode os.FileMode) error {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		tmp := filepath.Join(dir, "."+name+".tmp")
		if err := os.WriteFile(tmp, b, mode); err != nil {
			return err
		}
		return os.Rename(tmp, filepath.Join(dir, name))
	}
	var err error
	if token != "" {
		err = write("token", []byte(token+"\n"), 0o600)
	}
	if err == nil {
		err = write("request", []byte(body.Action+" "+time.Now().UTC().Format(time.RFC3339)+"\n"), 0o644)
	}
	if body.Action != "status" {
		s.audit(r, "cloudflared."+body.Action, "", err == nil, "")
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not ask the server")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"requested": body.Action})
}
