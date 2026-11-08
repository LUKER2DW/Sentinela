package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

// Reachability checks for the ways in from outside (ADR-0019, ADR-0025).
// GET /.well-known/sentinela answers this process's random id on every
// listener (it is outside /api, so the closed door lets it through, and it
// says nothing about the house). GET /api/remote/check asks the configured
// public address or tunnel for it over the internet: the same id means the
// address really reaches this server. The address always comes from the
// config, never from the request, so the check cannot be pointed elsewhere.

func newInstanceID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Server) wellKnown(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"sentinela": true, "id": s.instance})
}

type remoteCheck struct {
	Target string   `json:"target"`
	URL    string   `json:"url"`
	OK     bool     `json:"ok"`
	Reason string   `json:"reason,omitempty"` // off | dns | connect | tls | other | http
	Detail string   `json:"detail,omitempty"`
	IPs    []string `json:"ips,omitempty"`     // what the name resolves to
	HomeIP string   `json:"home_ip,omitempty"` // the home's public IP, seen from outside
	Hint   string   `json:"hint,omitempty"`    // loopback: the router may not allow its own address from inside
	Took   int64    `json:"took_ms"`
}

// homePublicIP asks a public service which address the home leaves from
// (only when a person runs a check).
func homePublicIP(ctx context.Context) string {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.ipify.org", nil)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64))
	if ip := net.ParseIP(strings.TrimSpace(string(b))); ip != nil {
		return ip.String()
	}
	return ""
}

// checkRemote: GET /api/remote/check?target=public|proxy|funnel (admin).
func (s *Server) checkRemote(w http.ResponseWriter, r *http.Request) {
	target := r.URL.Query().Get("target")
	res := remoteCheck{Target: target}
	pin := ""
	switch target {
	case "public":
		res.URL = publicOrigin(s.Cfg.Remote.Public)
		if s.Cfg.Remote.Public.SelfSigned {
			pin = s.publicCertPin()
		}
	case "proxy":
		res.URL = strings.TrimRight(s.Cfg.Remote.Proxy.URL, "/")
	case "funnel":
		res.URL = s.funnelURL(s.readTailscale())
	default:
		writeErr(w, http.StatusBadRequest, "target must be public, proxy or funnel")
		return
	}
	if res.URL == "" {
		res.Reason = "off"
		writeJSON(w, http.StatusOK, res)
		return
	}
	if !s.loginRL.Allow("remote-check") {
		writeErr(w, http.StatusTooManyRequests, "wait a minute before checking again")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	start := time.Now()
	u, err := url.Parse(res.URL)
	if err != nil {
		res.Reason, res.Detail = "other", "invalid address"
		writeJSON(w, http.StatusOK, res)
		return
	}
	host := u.Hostname()
	if target == "public" {
		res.HomeIP = homePublicIP(ctx)
	}
	if net.ParseIP(host) == nil {
		addrs, err := net.DefaultResolver.LookupHost(ctx, host)
		if err != nil || len(addrs) == 0 {
			res.Reason, res.Detail = "dns", "the name does not resolve yet"
			res.Took = time.Since(start).Milliseconds()
			writeJSON(w, http.StatusOK, res)
			return
		}
		res.IPs = addrs
	}

	tc := &tls.Config{MinVersion: tls.VersionTLS12}
	if pin != "" {
		// The home's own certificate: checked by its fingerprint, as the app does.
		tc.InsecureSkipVerify = true //nolint:gosec // verified below by the pin
		tc.VerifyConnection = func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("no certificate")
			}
			sum := sha256.Sum256(cs.PeerCertificates[0].Raw)
			if hex.EncodeToString(sum[:]) != pin {
				return errors.New("a different certificate answered")
			}
			return nil
		}
	}
	client := &http.Client{
		Timeout:       15 * time.Second,
		Transport:     &http.Transport{TLSClientConfig: tc, ForceAttemptHTTP2: true},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, res.URL+"/.well-known/sentinela", nil)
	req.Header.Set("User-Agent", "sentinela-reachability-check")
	resp, err := client.Do(req)
	res.Took = time.Since(start).Milliseconds()
	if err != nil {
		var cert *tls.CertificateVerificationError
		switch {
		case errors.As(err, &cert) || strings.Contains(err.Error(), "certificate"):
			res.Reason, res.Detail = "tls", "the certificate is not valid for this address"
		default:
			res.Reason, res.Detail = "connect", "no answer"
			if target == "public" && res.HomeIP != "" && (host == res.HomeIP || slices.Contains(res.IPs, res.HomeIP)) {
				res.Hint = "loopback"
			}
		}
		writeJSON(w, http.StatusOK, res)
		return
	}
	defer resp.Body.Close()
	var body struct {
		Sentinela bool   `json:"sentinela"`
		ID        string `json:"id"`
	}
	switch {
	case resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 4<<10)).Decode(&body) != nil || !body.Sentinela:
		res.Reason, res.Detail = "http", "something else answered (HTTP "+resp.Status+")"
	case body.ID != s.instance:
		res.Reason, res.Detail = "other", "another Sentinela answered"
	default:
		res.OK = true
	}
	writeJSON(w, http.StatusOK, res)
}
