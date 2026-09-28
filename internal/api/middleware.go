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
)

func userFrom(r *http.Request) *store.User {
	u, _ := r.Context().Value(userKey).(*store.User)
	return u
}

func sessionFrom(r *http.Request) *store.Session {
	s, _ := r.Context().Value(sessKey).(*store.Session)
	return s
}

// session attaches the logged-in user (if any) to the request context.
func (s *Server) session(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err == nil && c.Value != "" && len(c.Value) < 100 {
			sess, u, err := s.Store.SessionByHash(r.Context(), auth.HashToken(c.Value))
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
	return strings.EqualFold(u.Host, r.Host)
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
		if r.TLS != nil || s.Cfg.SecureCookies {
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
			"bytes", sw.bytes, "ms", time.Since(start).Milliseconds(), "ip", clientIP(r))
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

// clientIP is the TCP peer address. X-Forwarded-For is ignored on purpose:
// Sentinela is not behind a proxy (Tailscale serve preserves the peer).
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func hostPort(host string, port int) string { return net.JoinHostPort(host, strconv.Itoa(port)) }
