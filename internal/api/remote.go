package api

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"rsc.io/qr"

	"github.com/LUKER2DW/sentinela/internal/auth"
	"github.com/LUKER2DW/sentinela/internal/store"
)

// Access zones (ADR-0019). The zone comes from the listener a request
// arrived on, never from a header.
const (
	ZoneLAN     = "lan"     // listen, peer on the LAN
	ZoneTailnet = "tailnet" // listen, peer = `tailscale serve` on loopback
	ZoneFunnel  = "funnel"  // remote.funnel_listen (Tailscale Funnel), public
	ZonePublic  = "public"  // remote.public.listen (port-forward + TLS), public
	ZoneProxy   = "proxy"   // remote.proxy.listen (local reverse proxy or tunnel), public
)

// PublicZone reports whether anyone on the internet can reach zone z.
func PublicZone(z string) bool { return z == ZoneFunnel || z == ZonePublic || z == ZoneProxy }

func zoneFrom(r *http.Request) string {
	if z, _ := r.Context().Value(zoneKey).(string); z != "" {
		return z
	}
	return ZoneLAN
}

// withZone tags requests with their zone. fixed is the zone of a public
// listener; "" means the private listener (LAN, or tailnet via loopback).
func withZone(fixed string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		z := fixed
		if z == "" {
			z = ZoneLAN
			if fromLoopback(r) {
				z = ZoneTailnet
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), zoneKey, z)))
	})
}

// closedDoor: in public zones, a request without a 2FA session sees only
// the SPA shell and the login endpoints; everything else is 404, so a bug
// behind the login is not reachable from the internet (ADR-0019).
func (s *Server) closedDoor(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !PublicZone(zoneFrom(r)) || userFrom(r) != nil {
			next.ServeHTTP(w, r)
			return
		}
		p := r.URL.Path
		open := r.Method == http.MethodGet && p == "/api/setup" ||
			r.Method == http.MethodPost && (p == "/api/login" || p == "/api/logout" || p == "/api/invite/accept") ||
			(r.Method == http.MethodGet || r.Method == http.MethodHead) && p != "/healthz" &&
				!strings.HasPrefix(p, "/api/") && !strings.HasPrefix(p, "/stream/") && !strings.HasPrefix(p, "/internal/")
		if !open {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// privateOnly refuses h in public zones: actions that grant access (users,
// 2FA, app keys, first setup) are done from home or the tailnet.
func privateOnly(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if PublicZone(zoneFrom(r)) {
			writeErr(w, http.StatusForbidden, "only available at home or over Tailscale")
			return
		}
		h(w, r)
	}
}

// ---------- two-step login (TOTP) ----------

// pendingTOTP holds secrets shown during enrollment until the first code
// confirms them (10 minutes, in memory).
type pendingTOTP struct {
	mu sync.Mutex
	m  map[int64]pendingSecret
}

type pendingSecret struct {
	secret string
	exp    time.Time
}

func (p *pendingTOTP) put(uid int64, secret string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.m == nil {
		p.m = map[int64]pendingSecret{}
	}
	now := time.Now()
	for k, v := range p.m {
		if now.After(v.exp) {
			delete(p.m, k)
		}
	}
	p.m[uid] = pendingSecret{secret, now.Add(10 * time.Minute)}
}

func (p *pendingTOTP) get(uid int64) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	v, ok := p.m[uid]
	if !ok || time.Now().After(v.exp) {
		return ""
	}
	return v.secret
}

func (p *pendingTOTP) drop(uid int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.m, uid)
}

// checkOwnPassword verifies the caller's password, rate limited per user.
func (s *Server) checkOwnPassword(u *store.User, pw string) bool {
	return s.loginRL.Allow("pw:"+strconv.FormatInt(u.ID, 10)) && auth.VerifyPassword(u.PWHash, pw)
}

// totpSetup starts enrollment: a new secret, as text and as a QR code.
func (s *Server) totpSetup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	u := userFrom(r)
	if !s.checkOwnPassword(u, body.Password) {
		writeErr(w, http.StatusForbidden, "current password is wrong")
		return
	}
	secret := auth.NewTOTPSecret()
	s.totpPending.put(u.ID, secret)
	uri := auth.TOTPURI("Sentinela", u.Username, secret)
	svg, err := qrSVG(uri)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not draw the QR code")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"secret": secret, "uri": uri, "qr_svg": svg})
}

// totpEnable confirms enrollment with the first code from the app.
func (s *Server) totpEnable(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code string `json:"code"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	u := userFrom(r)
	secret := s.totpPending.get(u.ID)
	if secret == "" {
		writeErr(w, http.StatusConflict, "start the setup again")
		return
	}
	if !s.loginRL.Allow("totp:" + strconv.FormatInt(u.ID, 10)) {
		writeErr(w, http.StatusTooManyRequests, "too many attempts, wait a minute")
		return
	}
	step, ok := auth.VerifyTOTP(secret, body.Code, time.Now(), 0)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid code")
		return
	}
	if err := s.Store.SetUserTOTP(r.Context(), u.ID, secret); err != nil {
		writeErr(w, http.StatusInternalServerError, "database error")
		return
	}
	_ = s.Store.UseTOTPStep(r.Context(), u.ID, step)
	s.totpPending.drop(u.ID)
	s.audit(r, "user.totp", u.Username, true, "enabled")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// totpDisable turns two-step login off (and drops the 2FA sessions).
func (s *Server) totpDisable(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	u := userFrom(r)
	if !s.checkOwnPassword(u, body.Password) {
		writeErr(w, http.StatusForbidden, "current password is wrong")
		return
	}
	if err := s.Store.SetUserTOTP(r.Context(), u.ID, ""); err != nil {
		writeErr(w, http.StatusInternalServerError, "database error")
		return
	}
	s.audit(r, "user.totp", u.Username, true, "disabled")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// qrSVG draws text as a QR code (medium error correction, 4-module quiet
// zone) in one SVG path; the page shows it as a data: image.
func qrSVG(text string) (string, error) {
	c, err := qr.Encode(text, qr.M)
	if err != nil {
		return "", err
	}
	const quiet = 4
	n := c.Size + 2*quiet
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges">`, n, n)
	fmt.Fprintf(&b, `<rect width="%d" height="%d" fill="#fff"/><path fill="#000" d="`, n, n)
	for y := 0; y < c.Size; y++ {
		for x := 0; x < c.Size; x++ {
			if c.Black(x, y) {
				fmt.Fprintf(&b, "M%d %dh1v1h-1z", x+quiet, y+quiet)
			}
		}
	}
	b.WriteString(`"/></svg>`)
	return b.String(), nil
}

// alertOutsideLogin tells users who opted in about a login from a public zone.
func (s *Server) alertOutsideLogin(u *store.User, ip, zone string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	how := "via the domain"
	switch zone {
	case ZoneFunnel:
		how = "via Tailscale Funnel"
	case ZoneProxy:
		how = "via the tunnel / reverse proxy"
	}
	msg := map[string]any{
		"title": "Novo acesso de fora de casa",
		"body":  fmt.Sprintf("%s signed in %s (IP %s). Not you? Sign it out in My account → Devices.", u.Username, how, ip),
		"tag":   "sentinela-login",
		"url":   "/#/system",
	}
	sent := map[int64]bool{u.ID: true}
	if prefsOf(u).Enabled("login") {
		s.sendAll(ctx, u.ID, msg)
	}
	users, err := s.Store.ListUsers(ctx)
	if err != nil {
		return
	}
	for _, a := range users {
		if a.Role == auth.RoleAdmin && !sent[a.ID] && !a.Disabled && !a.Pending && prefsOf(&a).Enabled("login") {
			sent[a.ID] = true
			s.sendAll(ctx, a.ID, msg)
		}
	}
}
