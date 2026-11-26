package api

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
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
				// A tunnel or reverse proxy on this host pointed at this port
				// (instead of remote.proxy.listen) would get the tailnet's
				// rights from the internet: no closed door, no privateOnly,
				// first-admin setup. Traffic that says it comes from a public
				// address is treated as the public zone it is.
				if forwardedFromPublicAddr(r) {
					z = ZoneProxy
				}
			}
		}
		ctx := context.WithValue(r.Context(), zoneKey, z)
		if !PublicZone(z) {
			// Home requests may use every password-hash slot; the rest
			// share one, so the internet cannot starve home sign-ins.
			ctx = auth.WithHomeLane(ctx)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// tailnetNets are the Tailscale address ranges (CGNAT and the ULA prefix):
// `tailscale serve` forwards peers with these addresses.
var tailnetNets = []netip.Prefix{netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("fd7a:115c:a1e0::/48")}

// publicAddr reports whether a is a globally routable address: not loopback,
// private, link-local, multicast, unspecified or a tailnet address.
func publicAddr(a netip.Addr) bool {
	a = a.Unmap()
	if !a.IsValid() || a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() ||
		a.IsMulticast() || a.IsUnspecified() {
		return false
	}
	for _, p := range tailnetNets {
		if p.Contains(a) {
			return false
		}
	}
	return true
}

// forwardedFromPublicAddr: a loopback proxy says (X-Forwarded-For last hop,
// X-Real-IP or CF-Connecting-IP) that the client has a public address.
func forwardedFromPublicAddr(r *http.Request) bool {
	check := func(v string) bool {
		if i := strings.LastIndex(v, ","); i >= 0 {
			v = v[i+1:]
		}
		a, err := netip.ParseAddr(strings.TrimSpace(v))
		return err == nil && publicAddr(a)
	}
	for _, h := range r.Header.Values("X-Forwarded-For") {
		if check(h) {
			return true
		}
	}
	return check(r.Header.Get("X-Real-IP")) || check(r.Header.Get("CF-Connecting-IP"))
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
		// net/http reads up to 256 KB of an unread body before it can answer,
		// and the server has no Read/WriteTimeout (long streams need that), so
		// a client could hold the connection open for ever by sending nothing.
		// Give requests with no session a bounded time.
		anonDeadlines(w)
		defer clearDeadlines(w)
		if !open {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Deadlines for requests with no session on a public listener: the body is
// at most 64 KiB (readJSON) and the largest static file a few MB.
const (
	anonReadTimeout  = 15 * time.Second
	anonWriteTimeout = 2 * time.Minute
)

func anonDeadlines(w http.ResponseWriter) {
	rc := http.NewResponseController(w)
	now := time.Now()
	_ = rc.SetReadDeadline(now.Add(anonReadTimeout))
	_ = rc.SetWriteDeadline(now.Add(anonWriteTimeout))
}

// clearDeadlines leaves a kept-alive connection as it was found.
func clearDeadlines(w http.ResponseWriter) {
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Time{})
	_ = rc.SetWriteDeadline(time.Time{})
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

// checkOwnPassword verifies the caller's password, rate limited per user. It
// answers the request itself (403, or 503 when no hash could run) and
// returns false unless the password is right.
func (s *Server) checkOwnPassword(w http.ResponseWriter, r *http.Request, u *store.User, pw string) bool {
	if !s.loginRL.Allow("pw:" + strconv.FormatInt(u.ID, 10)) {
		writeErr(w, http.StatusForbidden, "current password is wrong")
		return false
	}
	good, err := auth.VerifyPasswordContext(r.Context(), u.PWHash, pw)
	if notRun(err) {
		busy(w, err)
		return false
	}
	if !good {
		writeErr(w, http.StatusForbidden, "current password is wrong")
	}
	return good
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
	if !s.checkOwnPassword(w, r, u, body.Password) {
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
	if !s.checkOwnPassword(w, r, u, body.Password) {
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

// zoneHow says in words how a request reached the server.
func zoneHow(zone string) string {
	switch zone {
	case ZoneFunnel:
		return "via Tailscale Funnel"
	case ZoneProxy:
		return "via the tunnel / reverse proxy"
	case ZonePublic:
		return "via the domain"
	case ZoneTailnet:
		return "over the tailnet"
	}
	return "at home"
}

// alertOutsideLogin tells users who opted in about a login from a public zone.
func (s *Server) alertOutsideLogin(u *store.User, ip, zone string) {
	s.loginNotice(u, false, map[string]any{
		"title": "Novo acesso de fora de casa",
		"body":  fmt.Sprintf("%s signed in %s (IP %s). Not you? Sign it out in My account → Devices.", u.Username, zoneHow(zone), ip),
		"tag":   "sentinela-login",
		"url":   "/#/system",
	})
}

// alertCodeLocked tells the account and every admin that its two-step code
// locked after wrong codes. Whoever typed them had the password, so it does
// not wait for the login-notice opt-in (off by default); auth.CodeGuard sends
// it at most once a day per account and kind of zone.
func (s *Server) alertCodeLocked(u *store.User, ip, zone string, fails int) {
	advice := "Someone knows the password: change it in My account."
	if !PublicZone(zone) { // at home it is most likely the owner mistyping
		advice = "If that was not you, someone knows the password: change it in My account."
	}
	s.loginNotice(u, true, map[string]any{
		"title": "Login em duas etapas bloqueado",
		"body": fmt.Sprintf("%d wrong two-step codes in a row for %s after the right password, %s (IP %s). %s",
			fails, u.Username, zoneHow(zone), ip, advice),
		"tag": "sentinela-code-locked",
		"url": "/#/system",
	})
}

// loginNotice sends msg to u and to the active admins: all of them when
// always is set, otherwise only those who selected login notices (opt-in,
// like every notice).
func (s *Server) loginNotice(u *store.User, always bool, msg map[string]any) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	wants := func(x *store.User) bool { return always || prefsOf(x).Enabled("login") }
	sent := map[int64]bool{u.ID: true}
	if wants(u) {
		s.sendAll(ctx, u.ID, msg)
	}
	users, err := s.Store.ListUsers(ctx)
	if err != nil {
		return
	}
	for _, a := range users {
		if a.Role == auth.RoleAdmin && !sent[a.ID] && !a.Disabled && !a.Pending && wants(&a) {
			sent[a.ID] = true
			s.sendAll(ctx, a.ID, msg)
		}
	}
}
