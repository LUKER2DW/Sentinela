package tailnet

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httputil"
	"strings"
	"time"
)

// TokenCookie is the cookie the app sets for the local origin; the proxy
// refuses requests without it and never forwards it.
const TokenCookie = "sentinela_local"

type dialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// newProxy forwards every request to https://server over dial, making the
// server see a normal request to its own origin.
func newProxy(dial dialFunc, server, token string, tlsConf *tls.Config) http.Handler {
	origin := "https://" + server
	tr := &http.Transport{
		DialContext:           dial,
		TLSClientConfig:       tlsConf,
		ForceAttemptHTTP2:     false, // WebSocket upgrades need HTTP/1.1
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
	}
	rp := &httputil.ReverseProxy{
		Transport:     tr,
		FlushInterval: -1, // SSE and streams flush at once
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = "https"
			pr.Out.URL.Host = server
			pr.Out.Host = server
			if pr.In.Header.Get("Origin") != "" {
				pr.Out.Header.Set("Origin", origin)
			}
			if ref := pr.In.Header.Get("Referer"); ref != "" {
				pr.Out.Header.Set("Referer", origin+"/")
			}
			stripTokenCookie(pr.Out.Header)
		},
		ModifyResponse: func(resp *http.Response) error {
			// The WebView talks plain HTTP to 127.0.0.1 (a secure context);
			// a Secure flag could make it drop the session cookie.
			if sc := resp.Header.Values("Set-Cookie"); len(sc) > 0 {
				resp.Header.Del("Set-Cookie")
				for _, c := range sc {
					resp.Header.Add("Set-Cookie", dropSecure(c))
				}
			}
			if loc := resp.Header.Get("Location"); strings.HasPrefix(loc, origin) {
				resp.Header.Set("Location", strings.TrimPrefix(loc, origin))
			}
			resp.Header.Del("Strict-Transport-Security")
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			http.Error(w, "Sentinela: sem conexão com a casa pela rede privada ("+errKind(err)+")", http.StatusBadGateway)
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(TokenCookie)
		if err != nil || subtle.ConstantTimeCompare([]byte(c.Value), []byte(token)) != 1 {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		rp.ServeHTTP(w, r)
	})
}

func stripTokenCookie(h http.Header) {
	var keep []string
	for _, line := range h.Values("Cookie") {
		for _, part := range strings.Split(line, ";") {
			p := strings.TrimSpace(part)
			if p == "" || strings.HasPrefix(p, TokenCookie+"=") {
				continue
			}
			keep = append(keep, p)
		}
	}
	h.Del("Cookie")
	if len(keep) > 0 {
		h.Set("Cookie", strings.Join(keep, "; "))
	}
}

func dropSecure(cookie string) string {
	parts := strings.Split(cookie, ";")
	out := parts[:1]
	for _, p := range parts[1:] {
		if !strings.EqualFold(strings.TrimSpace(p), "secure") {
			out = append(out, p)
		}
	}
	return strings.Join(out, ";")
}

// errKind keeps error pages short and free of addresses.
func errKind(err error) string {
	switch {
	case err == nil:
		return "?"
	case strings.Contains(err.Error(), "timeout"), strings.Contains(err.Error(), "deadline"):
		return "tempo esgotado"
	default:
		return "falha na conexão"
	}
}
