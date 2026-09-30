package api

import (
	"context"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/LUKER2DW/sentinela/internal/auth"
	"github.com/LUKER2DW/sentinela/internal/nas"
	"github.com/LUKER2DW/sentinela/internal/store"
)

var (
	usernamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{3,32}$`)
	emailPattern    = regexp.MustCompile(`^[^\s@]{1,64}@[^\s@]+\.[^\s@]+$`)
)

// InviteTTL is how long an invite code works.
const InviteTTL = 7 * 24 * time.Hour

// shortSessionTTL: "keep me signed in" off.
const shortSessionTTL = 12 * time.Hour

// maxUA: long enough for an Android WebView user agent plus "SentinelaApp/1".
const maxUA = 400

type credentials struct {
	// Username is the username or the e-mail.
	Username string `json:"username"`
	Password string `json:"password"`
	// Code is the TOTP code of accounts with two-step login (ADR-0019).
	Code string `json:"code,omitempty"`
	// Remember: keep this device signed in for 30 days (default true).
	Remember *bool `json:"remember,omitempty"`
}

func (c credentials) remember() bool { return c.Remember == nil || *c.Remember }

// tooManyFailures: an address that failed 20 sign-ins (or invite codes)
// in an hour is refused until the hour is over, whatever the account.
func (s *Server) tooManyFailures(ip string) bool { return s.failRL.Over("fail:" + ip) }

func (s *Server) failed(ip string) { s.failRL.Allow("fail:" + ip) }

func (s *Server) setupStatus(w http.ResponseWriter, r *http.Request) {
	n, err := s.Store.CountUsers(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "database error")
		return
	}
	z := zoneFrom(r)
	writeJSON(w, http.StatusOK, map[string]any{"needs_setup": n == 0 && !PublicZone(z), "version": Version,
		"zone": z, "mfa": PublicZone(z)})
}

// setup creates the first admin. It only works while no user exists.
func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	if !s.loginRL.Allow("setup:" + clientIP(r)) {
		writeErr(w, http.StatusTooManyRequests, "too many attempts, wait a minute")
		return
	}
	var c credentials
	if err := readJSON(r, &c); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if !usernamePattern.MatchString(c.Username) {
		writeErr(w, http.StatusBadRequest, "username: 3-32 letters, digits, '.', '_' or '-'")
		return
	}
	hash, err := auth.HashPassword(c.Password)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	id, err := s.Store.CreateFirstUser(r.Context(), c.Username, hash)
	if err != nil {
		writeErr(w, http.StatusConflict, "setup already completed")
		return
	}
	s.Log.Info("first admin created", "user", c.Username)
	_ = s.Store.Audit(r.Context(), id, c.Username, "setup", c.Username, true, "")
	s.startSession(w, r, id, false, true)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if s.tooManyFailures(ip) {
		writeErr(w, http.StatusTooManyRequests, "too many failed attempts from this address, wait an hour")
		return
	}
	if !s.loginRL.Allow("login:" + ip) {
		writeErr(w, http.StatusTooManyRequests, "too many attempts, wait a minute")
		return
	}
	var c credentials
	if err := readJSON(r, &c); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	zone := zoneFrom(r)
	public := PublicZone(zone)
	name := strings.TrimSpace(c.Username)
	// From the internet, an account takes 10 attempts per hour (ADR-0019).
	if public && !s.acctRL.Allow("acct:"+strings.ToLower(name)) {
		_ = s.Store.Audit(r.Context(), 0, name, "login", ip, false, zone+": account locked")
		writeErr(w, http.StatusTooManyRequests, "too many attempts for this account, wait an hour")
		return
	}
	u, err := s.Store.UserByLogin(r.Context(), name)
	if err != nil {
		auth.VerifyDummy(c.Password)
		s.failed(ip)
		_ = s.Store.Audit(r.Context(), 0, c.Username, "login", ip, false, zone+": unknown user")
		writeErr(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	if u.Pending {
		// Invited, no password yet: same answer and timing as a wrong one.
		auth.VerifyDummy(c.Password)
	}
	if u.Pending || !auth.VerifyPassword(u.PWHash, c.Password) {
		s.failed(ip)
		_ = s.Store.Audit(r.Context(), u.ID, u.Username, "login", ip, false, zone+": bad password")
		writeErr(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	// Checked after the password, so it does not reveal which accounts exist.
	if u.Disabled {
		_ = s.Store.Audit(r.Context(), u.ID, u.Username, "login", ip, false, zone+": account disabled")
		writeErr(w, http.StatusForbidden, "account disabled")
		return
	}
	// Two-step login is mandatory outside (a leaked password alone opens
	// nothing remotely) and asked at home too of accounts that have it.
	if public && !u.TOTP {
		_ = s.Store.Audit(r.Context(), u.ID, u.Username, "login", ip, false, zone+": no 2fa")
		writeErr(w, http.StatusForbidden, "two-step login is not set up for this account")
		return
	}
	if u.TOTP {
		if strings.TrimSpace(c.Code) == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "code required", "mfa": true})
			return
		}
		step, ok := auth.VerifyTOTP(u.TOTPSecret, c.Code, time.Now(), u.TOTPStep)
		if !ok || s.Store.UseTOTPStep(r.Context(), u.ID, step) != nil {
			s.failed(ip)
			_ = s.Store.Audit(r.Context(), u.ID, u.Username, "login", ip, false, zone+": bad code")
			writeErr(w, http.StatusUnauthorized, "invalid code")
			return
		}
	}
	_ = s.Store.Audit(r.Context(), u.ID, u.Username, "login", ip, true, zone)
	if public {
		go s.alertOutsideLogin(u, ip, zone)
	}
	s.startSession(w, r, u.ID, u.TOTP, c.remember())
}

// startSession logs userID in on this device; mfa marks a two-step login.
// Without remember the session lasts 12 hours and its cookie ends with the
// browser.
func (s *Server) startSession(w http.ResponseWriter, r *http.Request, userID int64, mfa, remember bool) {
	tok, csrf := auth.NewToken(), auth.NewToken()
	ttl := SessionTTL
	if !remember {
		ttl = shortSessionTTL
	}
	exp := time.Now().Add(ttl)
	ua := r.UserAgent()
	if len(ua) > maxUA {
		ua = ua[:maxUA]
	}
	if err := s.Store.CreateSession(r.Context(), auth.HashToken(tok), userID, csrf, exp, ua, mfa, remember); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not create session")
		return
	}
	ck := &http.Cookie{Name: sessionCookie, Value: tok, Path: "/", HttpOnly: true,
		Secure: s.Cfg.SecureCookies || isHTTPS(r), SameSite: http.SameSiteStrictMode}
	if remember {
		ck.Expires = exp
	}
	http.SetCookie(w, ck)
	u, _ := s.Store.UserByID(r.Context(), userID)
	writeJSON(w, http.StatusOK, map[string]any{"user": u, "csrf": csrf, "zone": zoneFrom(r)})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if sess := sessionFrom(r); sess != nil {
		_ = s.Store.DeleteSession(r.Context(), sess.IDHash)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true,
		Secure: s.Cfg.SecureCookies || isHTTPS(r), SameSite: http.SameSiteStrictMode})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// mySessions lists the caller's devices (Account page, Stage B2).
func (s *Server) mySessions(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.UserSessions(r.Context(), userFrom(r).ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "database error")
		return
	}
	cur := hex.EncodeToString(sessionFrom(r).IDHash[:8])
	for i := range list {
		list[i].Current = list[i].ID == cur
	}
	writeJSON(w, http.StatusOK, list)
}

// revokeSession signs one of the caller's devices out.
func (s *Server) revokeSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if len(id) != 16 {
		writeErr(w, http.StatusBadRequest, "invalid session id")
		return
	}
	err := s.Store.DeleteUserSession(r.Context(), userFrom(r).ID, id)
	s.audit(r, "session.revoke", id, err == nil, "")
	if err != nil {
		writeErr(w, http.StatusNotFound, "session not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"user": userFrom(r), "csrf": sessionFrom(r).CSRF, "zone": zoneFrom(r)})
}

func (s *Server) changeOwnPassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	u := userFrom(r)
	if !s.loginRL.Allow("pw:"+strconv.FormatInt(u.ID, 10)) || !auth.VerifyPassword(u.PWHash, body.Current) {
		writeErr(w, http.StatusForbidden, "current password is wrong")
		return
	}
	hash, err := auth.HashPassword(body.New)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.Store.UpdateUserPassword(r.Context(), u.ID, hash); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not update password")
		return
	}
	s.audit(r, "user.password", u.Username, true, "self")
	// Old sessions were revoked; keep this device logged in.
	s.startSession(w, r, u.ID, sessionFrom(r).MFA, sessionFrom(r).Remember)
}

// ---------- users (admin) ----------

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.Store.ListUsers(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "database error")
		return
	}
	writeJSON(w, http.StatusOK, users)
}

// createUser authorizes an account. Without a password (the normal way)
// it gets an invite code: the person redeems it on first access and picks
// their own password. Username may be left out when an e-mail is given.
func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string   `json:"username"`
		Email    string   `json:"email"`
		Password string   `json:"password"`
		Role     string   `json:"role"`
		Cameras  []string `json:"cameras"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	body.Email = strings.TrimSpace(body.Email)
	if body.Email != "" && (len(body.Email) > 254 || !emailPattern.MatchString(body.Email)) {
		writeErr(w, http.StatusBadRequest, "invalid e-mail")
		return
	}
	body.Username = strings.TrimSpace(body.Username)
	if body.Username == "" && body.Email != "" {
		body.Username = s.usernameFromEmail(r.Context(), body.Email)
	}
	if !usernamePattern.MatchString(body.Username) || !auth.ValidRole(body.Role) {
		writeErr(w, http.StatusBadRequest, "invalid username or role")
		return
	}
	if err := s.validCameras(body.Cameras); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var (
		id     int64
		err    error
		invite string
	)
	if body.Password != "" {
		hash, herr := auth.HashPassword(body.Password)
		if herr != nil {
			writeErr(w, http.StatusBadRequest, herr.Error())
			return
		}
		if id, err = s.Store.CreateUser(r.Context(), body.Username, hash, body.Role); err == nil && body.Email != "" {
			err = s.Store.UpdateUserEmail(r.Context(), id, body.Email)
		}
	} else {
		invite = auth.NewInviteCode()
		id, err = s.Store.CreateInvitedUser(r.Context(), body.Username, body.Email, body.Role,
			auth.HashToken(auth.NormalizeInviteCode(invite)), time.Now().Add(InviteTTL))
	}
	if err != nil {
		writeErr(w, http.StatusConflict, "username or e-mail already exists")
		return
	}
	if len(body.Cameras) > 0 {
		if err := s.Store.UpdateUserCameras(r.Context(), id, body.Cameras); err != nil {
			writeErr(w, http.StatusInternalServerError, "database error")
			return
		}
	}
	s.audit(r, "user.create", body.Username, true, body.Role+" cameras="+strings.Join(body.Cameras, ",")+
		map[bool]string{true: " invite", false: ""}[invite != ""])
	u, _ := s.Store.UserByID(r.Context(), id)
	writeJSON(w, http.StatusCreated, s.invited(u, invite))
}

// invitedUser is a user plus the invite code and link, shown to the admin
// once. The link carries the code in the URL fragment, which browsers never
// send to a server (so it stays out of logs).
type invitedUser struct {
	*store.User
	InviteCode string `json:"invite_code,omitempty"`
	// InviteURL is the first-access link on the public address ("" when
	// remote.public.url is not set; the page then uses its own address).
	InviteURL string `json:"invite_url,omitempty"`
}

func (s *Server) invited(u *store.User, code string) invitedUser {
	out := invitedUser{User: u, InviteCode: code}
	if base := strings.TrimRight(s.Cfg.Remote.Public.URL, "/"); base != "" && code != "" {
		login := u.Username
		if u.Email != "" {
			login = u.Email
		}
		out.InviteURL = base + "/#/convite?" + url.Values{"u": {login}, "c": {code}}.Encode()
	}
	return out
}

// usernameFromEmail makes a free username from an e-mail's local part.
func (s *Server) usernameFromEmail(ctx context.Context, email string) string {
	local, _, _ := strings.Cut(strings.ToLower(email), "@")
	var b strings.Builder
	for _, r := range local {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-' {
			b.WriteRune(r)
		}
	}
	base := b.String()
	if len(base) > 28 {
		base = base[:28]
	}
	for len(base) < 3 {
		base += "_"
	}
	name := base
	for i := 2; i < 100; i++ {
		if _, err := s.Store.UserByName(ctx, name); errors.Is(err, store.ErrNotFound) {
			return name
		}
		name = fmt.Sprintf("%s%d", base, i)
	}
	return name
}

// reinvite issues a new invite code for an account (lost password): its
// password, two-step login and sessions are dropped until it is redeemed.
func (s *Server) reinvite(w http.ResponseWriter, r *http.Request) {
	u := s.targetUser(w, r)
	if u == nil {
		return
	}
	if u.ID == userFrom(r).ID {
		writeErr(w, http.StatusConflict, "use Minha conta to change your own password")
		return
	}
	code := auth.NewInviteCode()
	if err := s.Store.SetInvite(r.Context(), u.ID, auth.HashToken(auth.NormalizeInviteCode(code)), time.Now().Add(InviteTTL)); err != nil {
		writeErr(w, http.StatusInternalServerError, "database error")
		return
	}
	s.audit(r, "user.invite", u.Username, true, "new invite")
	u, _ = s.Store.UserByID(r.Context(), u.ID)
	writeJSON(w, http.StatusOK, s.invited(u, code))
}

// acceptInvite is the first access: the invited person proves the code and
// picks a password. From outside (public zone) the account also gets
// two-step login right away: the first call answers totp_required with a
// QR code, the second brings the authenticator's code (ADR-0019).
func (s *Server) acceptInvite(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if s.tooManyFailures(ip) {
		writeErr(w, http.StatusTooManyRequests, "too many failed attempts from this address, wait an hour")
		return
	}
	if !s.loginRL.Allow("invite:" + ip) {
		writeErr(w, http.StatusTooManyRequests, "too many attempts, wait a minute")
		return
	}
	var body struct {
		Login    string `json:"login"`
		Code     string `json:"code"`
		Password string `json:"password"`
		TOTPCode string `json:"totp_code,omitempty"`
		Remember *bool  `json:"remember,omitempty"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx := r.Context()
	zone := zoneFrom(r)
	public := PublicZone(zone)
	login := strings.TrimSpace(body.Login)
	u, err := s.Store.UserByLogin(ctx, login)
	sum := auth.HashToken(auth.NormalizeInviteCode(body.Code))
	if err != nil || !u.Pending || len(u.InviteHash) == 0 || subtle.ConstantTimeCompare(sum, u.InviteHash) != 1 ||
		store.FormatTime(time.Now()) > u.InviteExpires {
		s.failed(ip)
		_ = s.Store.Audit(ctx, 0, login, "invite.accept", ip, false, zone+": bad or expired code")
		writeErr(w, http.StatusUnauthorized, "invalid or expired invite")
		return
	}
	if public && !s.acctRL.Allow("acct:"+strings.ToLower(u.Username)) {
		writeErr(w, http.StatusTooManyRequests, "too many attempts for this account, wait an hour")
		return
	}
	if u.Disabled {
		writeErr(w, http.StatusForbidden, "account disabled")
		return
	}
	if err := auth.CheckPasswordPolicy(body.Password); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	secret, step := "", int64(0)
	if public {
		secret = s.totpPending.get(u.ID)
		if secret == "" || strings.TrimSpace(body.TOTPCode) == "" {
			if secret == "" {
				secret = auth.NewTOTPSecret()
				s.totpPending.put(u.ID, secret)
			}
			uri := auth.TOTPURI("Sentinela", u.Username, secret)
			svg, err := qrSVG(uri)
			if err != nil {
				writeErr(w, http.StatusInternalServerError, "could not draw the QR code")
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"totp_required": true, "secret": secret, "uri": uri, "qr_svg": svg})
			return
		}
		var ok bool
		if step, ok = auth.VerifyTOTP(secret, body.TOTPCode, time.Now(), 0); !ok {
			s.failed(ip)
			writeErr(w, http.StatusUnauthorized, "invalid code")
			return
		}
	}
	hash, err := auth.HashPassword(body.Password)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.Store.RedeemInvite(ctx, u.ID, u.InviteHash, hash, secret, step); err != nil {
		writeErr(w, http.StatusConflict, "invalid or expired invite")
		return
	}
	s.totpPending.drop(u.ID)
	_ = s.Store.Audit(ctx, u.ID, u.Username, "invite.accept", ip, true, zone)
	if public {
		go s.alertOutsideLogin(u, ip, zone)
	}
	s.startSession(w, r, u.ID, public, body.Remember == nil || *body.Remember)
}

func (s *Server) targetUser(w http.ResponseWriter, r *http.Request) *store.User {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return nil
	}
	u, err := s.Store.UserByID(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "user not found")
		return nil
	}
	return u
}

// lastAdminGuard refuses changes that would leave no admin.
func (s *Server) lastAdminGuard(ctx context.Context, u *store.User) error {
	if u.Role != auth.RoleAdmin || u.Disabled {
		return nil
	}
	n, err := s.Store.CountAdmins(ctx)
	if err != nil {
		return err
	}
	if n <= 1 {
		return errors.New("cannot remove the last admin")
	}
	return nil
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	u := s.targetUser(w, r)
	if u == nil {
		return
	}
	var body struct {
		Role     *string   `json:"role"`
		Password *string   `json:"password"`
		Cameras  *[]string `json:"cameras"`
		// TOTPOff turns the user's two-step login off (lost phone).
		TOTPOff  bool    `json:"totp_off"`
		Disabled *bool   `json:"disabled"`
		Email    *string `json:"email"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.Disabled != nil {
		if u.ID == userFrom(r).ID && *body.Disabled {
			writeErr(w, http.StatusConflict, "you cannot disable yourself")
			return
		}
		if *body.Disabled {
			if err := s.lastAdminGuard(r.Context(), u); err != nil {
				writeErr(w, http.StatusConflict, err.Error())
				return
			}
		}
		if err := s.Store.SetUserDisabled(r.Context(), u.ID, *body.Disabled); err != nil {
			writeErr(w, http.StatusInternalServerError, "database error")
			return
		}
		s.audit(r, "user.disabled", u.Username, true, strconv.FormatBool(*body.Disabled))
	}
	if body.Email != nil {
		e := strings.TrimSpace(*body.Email)
		if e != "" && (len(e) > 254 || !emailPattern.MatchString(e)) {
			writeErr(w, http.StatusBadRequest, "invalid e-mail")
			return
		}
		if err := s.Store.UpdateUserEmail(r.Context(), u.ID, e); err != nil {
			writeErr(w, http.StatusConflict, "e-mail already used")
			return
		}
		s.audit(r, "user.email", u.Username, true, "")
	}
	if body.Role != nil {
		if !auth.ValidRole(*body.Role) {
			writeErr(w, http.StatusBadRequest, "invalid role")
			return
		}
		if *body.Role != auth.RoleAdmin {
			if err := s.lastAdminGuard(r.Context(), u); err != nil {
				writeErr(w, http.StatusConflict, err.Error())
				return
			}
		}
		if err := s.Store.UpdateUserRole(r.Context(), u.ID, *body.Role); err != nil {
			writeErr(w, http.StatusInternalServerError, "database error")
			return
		}
		s.audit(r, "user.role", u.Username, true, *body.Role)
	}
	if body.Cameras != nil {
		if err := s.validCameras(*body.Cameras); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := s.Store.UpdateUserCameras(r.Context(), u.ID, *body.Cameras); err != nil {
			writeErr(w, http.StatusInternalServerError, "database error")
			return
		}
		s.audit(r, "user.cameras", u.Username, true, strings.Join(*body.Cameras, ","))
	}
	if body.Password != nil {
		hash, err := auth.HashPassword(*body.Password)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := s.Store.UpdateUserPassword(r.Context(), u.ID, hash); err != nil {
			writeErr(w, http.StatusInternalServerError, "database error")
			return
		}
		s.audit(r, "user.password", u.Username, true, "reset by admin")
	}
	if body.TOTPOff {
		if err := s.Store.SetUserTOTP(r.Context(), u.ID, ""); err != nil {
			writeErr(w, http.StatusInternalServerError, "database error")
			return
		}
		s.audit(r, "user.totp", u.Username, true, "reset by admin")
	}
	u, _ = s.Store.UserByID(r.Context(), u.ID)
	writeJSON(w, http.StatusOK, u)
}

// validCameras accepts an empty list (all cameras) or known, unique ids.
func (s *Server) validCameras(ids []string) error {
	seen := map[string]bool{}
	for _, id := range ids {
		if s.Cfg.Camera(id) == nil || seen[id] {
			return fmt.Errorf("unknown or repeated camera %q", id)
		}
		seen[id] = true
	}
	return nil
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	u := s.targetUser(w, r)
	if u == nil {
		return
	}
	if u.ID == userFrom(r).ID {
		writeErr(w, http.StatusConflict, "you cannot delete yourself")
		return
	}
	if err := s.lastAdminGuard(r.Context(), u); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	if err := s.Store.DeleteUser(r.Context(), u.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, "database error")
		return
	}
	s.audit(r, "user.delete", u.Username, true, "")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// userDetail is the admin's view of one account: sessions, last sign-in,
// Nuvem usage and recent actions.
func (s *Server) userDetail(w http.ResponseWriter, r *http.Request) {
	u := s.targetUser(w, r)
	if u == nil {
		return
	}
	ctx := r.Context()
	sessions, err := s.Store.UserSessions(ctx, u.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "database error")
		return
	}
	last, _ := s.Store.LastLogin(ctx, u.Username)
	audit, err := s.Store.UserAudit(ctx, u.Username, 30)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "database error")
		return
	}
	out := map[string]any{"user": u, "sessions": sessions, "last_login": last, "audit": audit}
	if s.nasRoot != "" {
		out["nuvem"] = map[string]any{
			"folder": u.Username,
			"files":  folderUsage(ctx, filepath.Join(s.nasRoot, u.Username)),
			"trash":  folderUsage(ctx, filepath.Join(s.nasRoot, nas.Trash, u.Username)),
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// usage is the size of a folder tree; Partial when the walk was cut short.
type usage struct {
	Bytes   int64 `json:"bytes"`
	Files   int   `json:"files"`
	Folders int   `json:"folders"`
	Partial bool  `json:"partial,omitempty"`
}

// folderUsage walks a folder for at most 2 s / 200 000 entries (an admin
// page must not keep the old disk busy).
func folderUsage(ctx context.Context, root string) usage {
	var u usage
	deadline := time.Now().Add(2 * time.Second)
	_ = filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil || time.Now().After(deadline) || u.Files+u.Folders > 200_000 {
			u.Partial = true
			return fs.SkipAll
		}
		if d.IsDir() {
			u.Folders++
			return nil
		}
		if info, err := d.Info(); err == nil {
			u.Files++
			u.Bytes += info.Size()
		}
		return nil
	})
	if u.Folders > 0 {
		u.Folders-- // the root itself
	}
	return u
}

func (s *Server) listAudit(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Store.ListAudit(r.Context(), 200)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "database error")
		return
	}
	writeJSON(w, http.StatusOK, rows)
}
