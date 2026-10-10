package api

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Self-hosted services (ADR-0021). The API never touches Docker: it checks
// the session (admin), the zone and CSRF like any other route, then
// forwards /api/hosting/* to the apps agent on loopback with a token. The
// agent's answers come back unchanged; nothing it says is trusted here.

// hostingPublicActions may be used from the internet (a 2FA session):
// they run what is already configured. Creating or changing a service,
// which decides what code runs and where it is exposed, needs home or
// the tailnet, like account administration.
var hostingPublicActions = regexp.MustCompile(`^apps/[a-z][a-z0-9-]{1,30}/(start|stop|restart|deploy|check)$`)

// loadToken reads or creates a shared secret file (0640, same uid as the
// sidecar that reads it).
func loadToken(p string) (string, error) {
	if b, err := os.ReadFile(p); err == nil && len(bytes.TrimSpace(b)) >= 32 {
		return string(bytes.TrimSpace(b)), nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return "", err
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(buf)
	if err := os.WriteFile(p, []byte(tok+"\n"), 0o640); err != nil {
		return "", err
	}
	return tok, nil
}

type hostingState struct {
	token string
	proxy *httputil.ReverseProxy
}

// openHosting prepares the proxy when the platform is enabled.
func (s *Server) openHosting() {
	if !s.Cfg.Apps.Enabled {
		return
	}
	tok, err := loadToken(filepath.Join(s.Cfg.DataDir, "apps-agent", "token"))
	if err != nil {
		s.Log.Warn("self-hosted services disabled: token", "err", err)
		return
	}
	agent := s.Cfg.Apps.Agent
	s.hosting.token = tok
	s.hosting.proxy = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme, pr.Out.URL.Host = "http", agent
			pr.Out.URL.Path = "/v1/" + strings.TrimPrefix(pr.In.URL.Path, "/api/hosting/")
			pr.Out.URL.RawPath = ""
			pr.Out.Host = agent
			// The agent sees only its token: no cookies, CSRF or origin.
			for _, h := range []string{"Cookie", "Origin", "Referer", "X-Csrf-Token", "Authorization", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto"} {
				pr.Out.Header.Del(h)
			}
			pr.Out.Header.Set("Authorization", "Bearer "+tok)
		},
		Transport: &http.Transport{
			DialContext:           (&net.Dialer{Timeout: 3 * time.Second}).DialContext,
			ResponseHeaderTimeout: 5 * time.Minute, // deletes and inspections can be slow
			MaxIdleConns:          4,
			IdleConnTimeout:       30 * time.Second,
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			s.Log.Warn("apps agent unreachable", "err", err)
			writeErr(w, http.StatusBadGateway, "o agente de serviços não respondeu")
		},
	}
}

// hostingProxy forwards one request to the agent (admin only, see routes).
func (s *Server) hostingProxy(w http.ResponseWriter, r *http.Request) {
	if s.hosting.proxy == nil {
		writeErr(w, http.StatusNotFound, "self-hosted services are off on this server")
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/hosting/")
	if strings.Contains(rest, "..") || strings.Contains(rest, "//") {
		writeErr(w, http.StatusBadRequest, "bad path")
		return
	}
	write := r.Method != http.MethodGet && r.Method != http.MethodHead
	if write && PublicZone(zoneFrom(r)) && !hostingPublicActions.MatchString(rest) {
		writeErr(w, http.StatusForbidden, "only available at home or over Tailscale")
		return
	}
	limit := int64(512 << 10)
	switch {
	case r.Method == http.MethodPost && rest == "uploads":
		limit = 300 << 20
	case r.Method == http.MethodPut && strings.HasSuffix(rest, "/files/upload"):
		limit = 50 << 20
	case r.Method == http.MethodPut && (strings.HasSuffix(rest, "/files/text") || strings.HasSuffix(rest, "/secrets")):
		limit = 4 << 20
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	sw := &statusWriter{ResponseWriter: w}
	s.hosting.proxy.ServeHTTP(sw, r)
	if write {
		// Bodies are never logged: they carry environment values.
		action, target := hostingAudit(r.Method, rest)
		s.audit(r, action, target, sw.code > 0 && sw.code < 400, r.Method+" "+rest)
	}
}

// hostingAudit names an audit entry: apps.create, apps.update, apps.delete,
// apps.deploy…, with the service id as the target.
func hostingAudit(method, rest string) (action, target string) {
	parts := strings.Split(rest, "/")
	switch {
	case len(parts) >= 3 && parts[0] == "apps":
		return "apps." + parts[2], parts[1]
	case len(parts) == 2 && parts[0] == "apps":
		return map[string]string{http.MethodPut: "apps.update", http.MethodDelete: "apps.delete"}[method], parts[1]
	case rest == "apps":
		return "apps.create", ""
	case rest == "github-token":
		return "apps.github_token", ""
	}
	return "apps." + strings.ReplaceAll(rest, "/", "."), ""
}
