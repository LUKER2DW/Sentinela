package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Tailscale from the dashboard (ADR-0004): the welcome guide and System ->
// Server walk the person through it. The API never runs tailscale (it has no
// host access): it leaves a request in <data>/tailscale for the host agent
// (scripts/tailscale-agent.sh, run by sentinela-tailscale.path) and reads
// back the status the agent writes (/var/lib/sentinela/tailscale.json, seen
// as /host/tailscale.json). The auth key is written once, 0600, for the
// agent to move away and delete; it is never stored, logged or returned.

// authKeyRe is a Tailscale auth key (admin console -> Settings -> Keys).
var authKeyRe = regexp.MustCompile(`^tskey-auth-[A-Za-z0-9_-]{10,200}$`)

func (s *Server) tailscaleDir() string { return filepath.Join(s.Cfg.DataDir, "tailscale") }

// hostFile is a file the host writes next to the update status (/host).
func (s *Server) hostFile(name string) string {
	return filepath.Join(filepath.Dir(s.Cfg.UpdateStatusFile), name)
}

// tailscaleStatus is what the agent last saw (nil when never run).
type tailscaleStatus struct {
	Checked   string   `json:"checked"`
	Installed bool     `json:"installed"`
	Version   string   `json:"version"`
	State     string   `json:"state"`
	DNSName   string   `json:"dns_name"`
	IP        string   `json:"ip"`
	Tags      []string `json:"tags"`
	MagicDNS  bool     `json:"magic_dns"`
	HTTPS     bool     `json:"https"`
	Serve     bool     `json:"serve"`
	Funnel    []int    `json:"funnel"` // Tailscale Funnel ports open to the internet
	Busy      bool     `json:"busy"`
	Action    string   `json:"action"`
	Message   string   `json:"message"`
	EnableURL string   `json:"enable_url"`
}

func (s *Server) readTailscale() *tailscaleStatus {
	b, err := os.ReadFile(s.hostFile("tailscale.json"))
	if err != nil || len(b) > 64<<10 {
		return nil
	}
	var st tailscaleStatus
	if json.Unmarshal(b, &st) != nil {
		return nil
	}
	// Only links into Tailscale's own admin console are passed to the page.
	if u, err := url.Parse(st.EnableURL); err != nil || u.Scheme != "https" || u.Host != "login.tailscale.com" {
		st.EnableURL = ""
	}
	return &st
}

// tailnetURLFor is the address the app and browser use over the tailnet, when
// the server is connected and publishes the web UI with HTTPS; "" otherwise.
func tailnetURLFor(st *tailscaleStatus) string {
	if st == nil || st.State != "Running" || !st.Serve || st.DNSName == "" {
		return ""
	}
	return "https://" + st.DNSName
}

// funnelURL is the public Tailscale link (Funnel), when it is on and reaches
// the public listener; "" otherwise.
func (s *Server) funnelURL(st *tailscaleStatus) string {
	if st == nil || st.State != "Running" || st.DNSName == "" || len(st.Funnel) == 0 || s.Cfg.Remote.FunnelListen == "" {
		return ""
	}
	if st.Funnel[0] == 443 {
		return "https://" + st.DNSName
	}
	return "https://" + st.DNSName + ":" + strconv.Itoa(st.Funnel[0])
}

// tailscaleCheck says whether a configured remote.tailnet_url is this server
// as Tailscale itself names it: "ok", "mismatch", or "unknown" (no status).
func tailscaleCheck(configured string, st *tailscaleStatus) string {
	want := tailnetURLFor(st)
	switch {
	case configured == "" || want == "":
		return "unknown"
	case strings.EqualFold(strings.TrimRight(configured, "/"), want):
		return "ok"
	default:
		return "mismatch"
	}
}

// getTailscale: GET /api/tailscale (admin).
func (s *Server) getTailscale(w http.ResponseWriter, _ *http.Request) {
	st := s.readTailscale()
	configured := strings.TrimRight(s.Cfg.Remote.TailnetURL, "/")
	writeJSON(w, http.StatusOK, map[string]any{
		// The host agent's units exist (installer/deploy); without them the
		// page only explains the manual way.
		"host":       fileExists(s.hostFile("tailscale-units")),
		"status":     st,
		"pending":    pendingRequest(filepath.Join(s.tailscaleDir(), "request")),
		"configured": configured,
		"suggested":  tailnetURLFor(st),
		"check":      tailscaleCheck(configured, st),
		// The public link (Funnel) and whether its listener is configured.
		"funnel_url":      s.funnelURL(st),
		"funnel_listener": s.Cfg.Remote.FunnelListen != "",
	})
}

// postTailscale: POST /api/tailscale (admin, home or tailnet)
// {"action": "status"|"connect"|"serve"|"disconnect"|"funnel"|"funnel_off",
// "auth_key": "...", "port": 443|8443|10000}.
func (s *Server) postTailscale(w http.ResponseWriter, r *http.Request) {
	if !fileExists(s.hostFile("tailscale-units")) {
		writeErr(w, http.StatusConflict, "this server cannot set up Tailscale from the dashboard yet (run a deploy or scripts/setup-tailscale.sh units)")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var body struct {
		Action  string `json:"action"`
		AuthKey string `json:"auth_key"`
		Port    int    `json:"port"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	arg := ""
	switch body.Action {
	case "status", "serve", "disconnect", "funnel_off":
	case "funnel":
		if body.Port != 443 && body.Port != 8443 && body.Port != 10000 {
			writeErr(w, http.StatusBadRequest, "Funnel works on ports 443, 8443 or 10000")
			return
		}
		arg = " " + strconv.Itoa(body.Port)
	case "connect":
		body.AuthKey = strings.TrimSpace(body.AuthKey)
		if !authKeyRe.MatchString(body.AuthKey) {
			writeErr(w, http.StatusBadRequest, "that is not a Tailscale auth key (it starts with tskey-auth-)")
			return
		}
	default:
		writeErr(w, http.StatusBadRequest, "unknown action")
		return
	}
	if body.Action != "status" && !s.loginRL.Allow("tailscale-"+body.Action) {
		writeErr(w, http.StatusTooManyRequests, "wait a minute before trying again")
		return
	}
	dir := s.tailscaleDir()
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
	if body.Action == "connect" {
		err = write("authkey", []byte(body.AuthKey+"\n"), 0o600)
	}
	if err == nil {
		err = write("request", []byte(body.Action+arg+" "+time.Now().UTC().Format(time.RFC3339)+"\n"), 0o644)
	}
	if body.Action != "status" {
		s.audit(r, "tailscale."+body.Action, "", err == nil, "")
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not ask the server")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"requested": body.Action})
}
