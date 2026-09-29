package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/LUKER2DW/sentinela/internal/auth"
	"github.com/LUKER2DW/sentinela/internal/auth/redact"
	"github.com/LUKER2DW/sentinela/internal/store"
)

const sessionCookie = "sentinela_session"

// SessionTTL is how long a login lasts (docs/SECURITY.md).
const SessionTTL = 30 * 24 * time.Hour

type ctxKey int

const (
	userKey ctxKey = iota
	sessKey
	zoneKey
)

func userFrom(r *http.Request) *store.User {
	u, _ := r.Context().Value(userKey).(*store.User)
	return u
}

func sessionFrom(r *http.Request) *store.Session {
	s, _ := r.Context().Value(sessKey).(*store.Session)
	return s
}

// session attaches the logged-in user (if any) to the request context. In
// public zones only sessions made with two-step login count (ADR-0019).
func (s *Server) session(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err == nil && c.Value != "" && len(c.Value) < 100 {
			sess, u, err := s.Store.SessionByHash(r.Context(), auth.HashToken(c.Value))
			if err == nil && PublicZone(zoneFrom(r)) && (!sess.MFA || !u.TOTP) {
				err = store.ErrNotFound
			}
			if err == nil {
				ctx := context.WithValue(r.Context(), userKey, u)
				ctx = context.WithValue(ctx, sessKey, sess)
				r = r.WithContext(ctx)
			} else if !errors.Is(err, store.ErrNotFound) {
				s.Log.Error("session lookup failed", "err", err)
			}
		}
		next.ServeHTTP(w, r)
	})
}

// require enforces a minimum role.
func (s *Server) require(role string, h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := userFrom(r)
		if u == nil {
			writeErr(w, http.StatusUnauthorized, "login required")
			return
		}
		if !auth.RoleAtLeast(u.Role, role) {
			writeErr(w, http.StatusForbidden, "insufficient role")
			return
		}
		h(w, r)
	})
}

// csrf requires the X-CSRF-Token header on unsafe methods for logged-in
// users, and a same-origin Origin header when one is sent.
func (s *Server) csrf(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if o := r.Header.Get("Origin"); o != "" && !sameOrigin(o, r) {
			writeErr(w, http.StatusForbidden, "cross-origin request refused")
			return
		}
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if sess := sessionFrom(r); sess != nil && !auth.EqualToken(r.Header.Get("X-CSRF-Token"), sess.CSRF) {
				writeErr(w, http.StatusForbidden, "missing or invalid CSRF token")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func sameOrigin(origin string, r *http.Request) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := r.Host
	if fh := r.Header.Get("X-Forwarded-Host"); fh != "" && fromLoopback(r) {
		host = fh
	}
	return strings.EqualFold(u.Host, host)
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; "+
			"img-src 'self' data: blob:; media-src 'self' blob:; connect-src 'self'; worker-src 'self'; "+
			"font-src 'self'; manifest-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Permissions-Policy", "camera=(), geolocation=(), microphone=(self)")
		if isHTTPS(r) {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	code  int
	bytes int64
}

func (w *statusWriter) WriteHeader(c int) { w.code = c; w.ResponseWriter.WriteHeader(c) }
func (w *statusWriter) Write(b []byte) (int, error) {
	if w.code == 0 {
		w.code = 200
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += int64(n)
	return n, err
}
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		// Static assets and health checks are not interesting.
		if r.URL.Path == "/healthz" || strings.HasPrefix(r.URL.Path, "/assets/") {
			return
		}
		s.Log.Info("http", "method", r.Method, "path", redact.URL(r.URL.RequestURI()), "status", sw.code,
			"bytes", sw.bytes, "ms", time.Since(start).Milliseconds(), "ip", clientIP(r), "zone", zoneFrom(r))
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				s.Log.Error("panic", "value", fmt.Sprint(v), "stack", string(debug.Stack()))
				writeErr(w, http.StatusInternalServerError, "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// fromLoopback reports whether the TCP peer is this host. The only local
// client is `tailscale serve`, which proxies the tailnet's HTTPS to
// 127.0.0.1:8080 (ARCHITECTURE §2.5); LAN clients never arrive from loopback.
// Forwarded headers are trusted from this peer only (SECURITY.md rule 4).
func fromLoopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	ip := net.ParseIP(host)
	return err == nil && ip != nil && ip.IsLoopback()
}

// clientIP is the TCP peer address or, behind the loopback proxy, the hop
// that proxy appended to X-Forwarded-For (earlier hops are client-supplied).
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	if xff := r.Header.Values("X-Forwarded-For"); len(xff) > 0 && fromLoopback(r) {
		hops := strings.Split(xff[len(xff)-1], ",")
		if last := strings.TrimSpace(hops[len(hops)-1]); net.ParseIP(last) != nil {
			return last
		}
	}
	return host
}

// isHTTPS reports whether the browser reached us over HTTPS: TLS here, or
// `tailscale serve` on loopback saying so. Session cookies get Secure then,
// while plain-HTTP LAN logins keep working (PLAN Phase 9 D6).
func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || fromLoopback(r) && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func hostPort(host string, port int) string { return net.JoinHostPort(host, strconv.Itoa(port)) }
