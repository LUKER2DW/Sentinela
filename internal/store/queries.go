package store

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// ---------- users ----------

// User is an account. PWHash is never serialized.
type User struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	Role      string `json:"role"`
	CreatedAt string `json:"created_at"`
	// Cameras limits a non-admin user to these camera ids; empty = all.
	Cameras []string `json:"cameras"`
	// TOTP reports whether two-step login is on (ADR-0019).
	TOTP bool `json:"totp"`
	// Disabled accounts cannot sign in (sessions are dropped when set).
	Disabled bool `json:"disabled"`
	// Email is an optional second sign-in name ("" = none).
	Email string `json:"email"`
	// Pending: authorized but no password yet (invite outstanding).
	Pending bool `json:"pending"`
	// InviteExpires is when the outstanding invite code stops working.
	InviteExpires string `json:"invite_expires,omitempty"`
	PWHash        string `json:"-"`
	TOTPSecret    string `json:"-"`
	TOTPStep      int64  `json:"-"`
	InviteHash    []byte `json:"-"`
	// Notify is the JSON of the user's notification choices ("" = defaults).
	Notify string `json:"-"`
}

// userCols is the column list scanUser reads, in order.
const userCols = `id,username,pw_hash,role,created_at,cameras,totp_secret,totp_step,disabled,email,invite_hash,invite_expires,notify`

// CanSee reports whether the user may see a camera (live, recordings,
// events, control). Admins see every camera.
func (u *User) CanSee(cameraID string) bool {
	return u.Role == "admin" || len(u.Cameras) == 0 || slices.Contains(u.Cameras, cameraID)
}

// joinCameras and splitCameras store the list as "a,b" (” = all).
func joinCameras(ids []string) string { return strings.Join(ids, ",") }

func splitCameras(s string) []string {
	if s == "" {
		return []string{}
	}
	return strings.Split(s, ",")
}

// CountUsers returns the number of users.
func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.priv.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

// CreateUser inserts a user and returns its id.
func (s *Store) CreateUser(ctx context.Context, username, pwHash, role string) (int64, error) {
	res, err := s.priv.ExecContext(ctx, `INSERT INTO users(username,pw_hash,role,created_at) VALUES(?,?,?,?)`, username, pwHash, role, Now())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// CreateFirstUser inserts an admin only if no user exists (atomic).
func (s *Store) CreateFirstUser(ctx context.Context, username, pwHash string) (int64, error) {
	res, err := s.priv.ExecContext(ctx, `INSERT INTO users(username,pw_hash,role,created_at)
		SELECT ?,?,'admin',? WHERE NOT EXISTS (SELECT 1 FROM users)`, username, pwHash, Now())
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0, errors.New("setup already completed")
	}
	return res.LastInsertId()
}

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	var u User
	var cams string
	if err := row.Scan(&u.ID, &u.Username, &u.PWHash, &u.Role, &u.CreatedAt, &cams, &u.TOTPSecret, &u.TOTPStep, &u.Disabled,
		&u.Email, &u.InviteHash, &u.InviteExpires, &u.Notify); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	u.Cameras = splitCameras(cams)
	u.TOTP = u.TOTPSecret != ""
	u.Pending = u.PWHash == ""
	if !u.Pending {
		u.InviteExpires = ""
	}
	return &u, nil
}

// UserByLogin looks a user up by username or e-mail (case-insensitive).
func (s *Store) UserByLogin(ctx context.Context, login string) (*User, error) {
	return scanUser(s.priv.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE username=? OR (email<>'' AND email=? COLLATE NOCASE)
		ORDER BY username=? DESC LIMIT 1`, login, login, login))
}

// UserByName looks a user up by username (case-insensitive).
func (s *Store) UserByName(ctx context.Context, username string) (*User, error) {
	return scanUser(s.priv.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE username=?`, username))
}

// UserByID looks a user up by id.
func (s *Store) UserByID(ctx context.Context, id int64) (*User, error) {
	return scanUser(s.priv.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id=?`, id))
}

// ListUsers returns all users.
func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.priv.QueryContext(ctx, `SELECT `+userCols+` FROM users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

// UpdateUserRole changes a user's role.
func (s *Store) UpdateUserRole(ctx context.Context, id int64, role string) error {
	return s.privOne(ctx, `UPDATE users SET role=? WHERE id=?`, role, id)
}

// UpdateUserCameras sets the cameras a user may see (empty = all).
func (s *Store) UpdateUserCameras(ctx context.Context, id int64, cameras []string) error {
	return s.privOne(ctx, `UPDATE users SET cameras=? WHERE id=?`, joinCameras(cameras), id)
}

// UpdateUserPassword changes a user's password hash and drops their sessions.
func (s *Store) UpdateUserPassword(ctx context.Context, id int64, pwHash string) error {
	if err := s.privOne(ctx, `UPDATE users SET pw_hash=?, invite_hash=NULL, invite_expires='' WHERE id=?`, pwHash, id); err != nil {
		return err
	}
	return s.dropUserAccess(ctx, id)
}

// dropUserAccess ends every session of a user and forgets their Web Push
// subscriptions, including ones saved before subscriptions were tied to a
// session (they would otherwise outlive the sessions). One transaction.
func (s *Store) dropUserAccess(ctx context.Context, id int64) error {
	tx, err := s.priv.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM push_subscriptions WHERE user_id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// SetUserTOTP turns two-step login on (secret) or off (”); off also
// drops the user's 2FA sessions.
func (s *Store) SetUserTOTP(ctx context.Context, id int64, secret string) error {
	if err := s.privOne(ctx, `UPDATE users SET totp_secret=?, totp_step=0 WHERE id=?`, secret, id); err != nil {
		return err
	}
	if secret == "" {
		_, err := s.priv.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=? AND mfa=1`, id)
		return err
	}
	return nil
}

// UseTOTPStep records an accepted TOTP step. It fails if an equal or later
// step was already used (a code is good once, even across racing logins).
func (s *Store) UseTOTPStep(ctx context.Context, id, step int64) error {
	return s.privOne(ctx, `UPDATE users SET totp_step=? WHERE id=? AND totp_step<?`, step, id, step)
}

// SetUserDisabled switches an account off (dropping its sessions) or on.
func (s *Store) SetUserDisabled(ctx context.Context, id int64, disabled bool) error {
	if err := s.privOne(ctx, `UPDATE users SET disabled=? WHERE id=?`, disabled, id); err != nil {
		return err
	}
	if disabled {
		return s.dropUserAccess(ctx, id)
	}
	return nil
}

// CreateInvitedUser inserts an account without a password: it signs in
// only after redeeming the invite (inviteHash) before expires.
func (s *Store) CreateInvitedUser(ctx context.Context, username, email, role string, inviteHash []byte, expires time.Time) (int64, error) {
	res, err := s.priv.ExecContext(ctx, `INSERT INTO users(username,pw_hash,role,created_at,email,invite_hash,invite_expires) VALUES(?,?,?,?,?,?,?)`,
		username, "", role, Now(), email, inviteHash, FormatTime(expires))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// SetInvite issues a new invite: the password, two-step login and every
// session are dropped until the person redeems it (lost password).
func (s *Store) SetInvite(ctx context.Context, id int64, inviteHash []byte, expires time.Time) error {
	if err := s.privOne(ctx, `UPDATE users SET pw_hash='', totp_secret='', totp_step=0, invite_hash=?, invite_expires=? WHERE id=?`,
		inviteHash, FormatTime(expires), id); err != nil {
		return err
	}
	return s.dropUserAccess(ctx, id)
}

// RedeemInvite sets the password (and 2FA secret, may be "") of a pending
// account whose invite matches; it fails if the invite changed meanwhile.
func (s *Store) RedeemInvite(ctx context.Context, id int64, inviteHash []byte, pwHash, totpSecret string, totpStep int64) error {
	return s.privOne(ctx, `UPDATE users SET pw_hash=?, totp_secret=?, totp_step=?, invite_hash=NULL, invite_expires=''
		WHERE id=? AND pw_hash='' AND invite_hash=?`, pwHash, totpSecret, totpStep, id, inviteHash)
}

// UpdateUserEmail changes a user's e-mail ("" = none).
func (s *Store) UpdateUserEmail(ctx context.Context, id int64, email string) error {
	return s.privOne(ctx, `UPDATE users SET email=? WHERE id=?`, email, id)
}

// SetUserNotify stores the user's notification choices (JSON).
func (s *Store) SetUserNotify(ctx context.Context, id int64, prefs string) error {
	return s.privOne(ctx, `UPDATE users SET notify=? WHERE id=?`, prefs, id)
}

// LastLogin returns the time of a user's last successful sign-in ("" = never).
func (s *Store) LastLogin(ctx context.Context, username string) (string, error) {
	var at sql.NullString
	err := s.priv.QueryRowContext(ctx, `SELECT max(at) FROM audit WHERE action='login' AND ok=1 AND username=?`, username).Scan(&at)
	return at.String, err
}

// DeleteUser removes a user (sessions cascade).
func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	return s.privOne(ctx, `DELETE FROM users WHERE id=?`, id)
}

// CountAdmins returns the number of admin users.
func (s *Store) CountAdmins(ctx context.Context) (int, error) {
	var n int
	err := s.priv.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE role='admin' AND disabled=0`).Scan(&n)
	return n, err
}

// execOne runs a statement on the shared index and reports ErrNotFound when
// it changed no row; privOne does the same on the private database.
func (s *Store) execOne(ctx context.Context, q string, args ...any) error {
	return one(s.db.ExecContext(ctx, q, args...))
}

func (s *Store) privOne(ctx context.Context, q string, args ...any) error {
	return one(s.priv.ExecContext(ctx, q, args...))
}

func one(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------- sessions ----------

// Session is a login session; the token itself is never stored.
type Session struct {
	IDHash    []byte
	UserID    int64
	CSRF      string
	ExpiresAt time.Time
	// MFA: created with two-step login; required in public zones (ADR-0019).
	MFA bool
	// Remember: "keep me signed in" (long session, persistent cookie).
	Remember bool
	// Host is the Host (with port) the session was created on; '' for a
	// session made before 0012. Public zones accept the session on that
	// Host only.
	Host string
	// Zone is the access zone of the listener the session was created on
	// (decided by the server); '' for a session made before 0013. Public
	// zones accept only sessions created on that same zone.
	Zone string
}

// CreateSession stores a session keyed by the token hash, bound to host (the
// Host it was created on) and zone (the listener's access zone).
func (s *Store) CreateSession(ctx context.Context, idHash []byte, userID int64, csrf string, expires time.Time, ua string, mfa, remember bool, host, zone string) error {
	_, err := s.priv.ExecContext(ctx, `INSERT INTO sessions(id_hash,user_id,csrf,created_at,expires_at,user_agent,mfa,remember,host,zone) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		idHash, userID, csrf, Now(), FormatTime(expires), ua, mfa, remember, host, zone)
	return err
}

// SessionByHash returns a non-expired session with its user.
func (s *Store) SessionByHash(ctx context.Context, idHash []byte) (*Session, *User, error) {
	var sess Session
	var exp string
	err := s.priv.QueryRowContext(ctx, `SELECT s.id_hash,s.user_id,s.csrf,s.expires_at,s.mfa,s.remember,s.host,s.zone
		FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.id_hash=? AND s.expires_at>? AND u.disabled=0 AND u.pw_hash<>''`, idHash, Now()).
		Scan(&sess.IDHash, &sess.UserID, &sess.CSRF, &exp, &sess.MFA, &sess.Remember, &sess.Host, &sess.Zone)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	u, err := s.UserByID(ctx, sess.UserID)
	if err != nil {
		return nil, nil, err
	}
	sess.ExpiresAt = mustTime(exp)
	return &sess, u, nil
}

// ExtendSession moves a session's expiry (sliding "keep me signed in").
func (s *Store) ExtendSession(ctx context.Context, idHash []byte, expires time.Time) error {
	_, err := s.priv.ExecContext(ctx, `UPDATE sessions SET expires_at=? WHERE id_hash=?`, FormatTime(expires), idHash)
	return err
}

// DeleteSession removes one session.
func (s *Store) DeleteSession(ctx context.Context, idHash []byte) error {
	_, err := s.priv.ExecContext(ctx, `DELETE FROM sessions WHERE id_hash=?`, idHash)
	return err
}

// DeviceSession is a session as shown to its owner. ID is the first 8 bytes
// of the token hash in hex: enough to pick one, useless to log in with.
type DeviceSession struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	UserAgent string    `json:"user_agent"`
	Current   bool      `json:"current"`
	// MFA: signed in with two-step login, i.e. from outside (ADR-0019).
	MFA bool `json:"mfa"`
}

// UserSessions lists a user's live sessions, newest first.
func (s *Store) UserSessions(ctx context.Context, userID int64) ([]DeviceSession, error) {
	rows, err := s.priv.QueryContext(ctx, `SELECT lower(hex(substr(id_hash,1,8))),created_at,expires_at,user_agent,mfa FROM sessions
		WHERE user_id=? AND expires_at>? ORDER BY created_at DESC`, userID, Now())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DeviceSession{}
	for rows.Next() {
		var d DeviceSession
		var c, e string
		if err := rows.Scan(&d.ID, &c, &e, &d.UserAgent, &d.MFA); err != nil {
			return nil, err
		}
		d.CreatedAt, d.ExpiresAt = mustTime(c), mustTime(e)
		out = append(out, d)
	}
	return out, rows.Err()
}

// DeleteUserSession removes one of a user's sessions by its short id.
func (s *Store) DeleteUserSession(ctx context.Context, userID int64, id string) error {
	return s.privOne(ctx, `DELETE FROM sessions WHERE user_id=? AND lower(hex(substr(id_hash,1,8)))=?`, userID, id)
}

// PurgeExpiredSessions deletes expired sessions.
func (s *Store) PurgeExpiredSessions(ctx context.Context) error {
	_, err := s.priv.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at<=?`, Now())
	return err
}

// ---------- segments ----------

// Segment is one recorded file.
type Segment struct {
	ID        int64     `json:"id"`
	CameraID  string    `json:"camera_id"`
	Start     time.Time `json:"start"`
	End       time.Time `json:"end"`
	Path      string    `json:"-"`
	Bytes     int64     `json:"bytes"`
	Protected bool      `json:"protected"`
}

// UpsertSegment inserts or updates a segment by path.
func (s *Store) UpsertSegment(ctx context.Context, seg Segment) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO segments(camera_id,start_utc,end_utc,path,bytes) VALUES(?,?,?,?,?)
		ON CONFLICT(path) DO UPDATE SET end_utc=excluded.end_utc, bytes=excluded.bytes`,
		seg.CameraID, FormatTime(seg.Start), FormatTime(seg.End), seg.Path, seg.Bytes)
	return err
}

// SegmentPaths returns the indexed paths (and sizes) of a camera's segments
// starting at or after since; the zero time selects all of them. Segments
// archived from an earlier camera with the same id ("<id>~…", see
// SyncCameraIDs) still live in its folder, so they are included.
func (s *Store) SegmentPaths(ctx context.Context, cameraID string, since time.Time) (map[string]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT path,bytes FROM segments
		WHERE (camera_id=? OR (camera_id>? AND camera_id<?)) AND start_utc>=?`,
		cameraID, cameraID+ArchiveSep, cameraID+ArchiveSep+ArchiveSep, FormatTime(since)) // ArchiveID stamps are [0-9A-Z], below "~"
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var p string
		var b int64
		if err := rows.Scan(&p, &b); err != nil {
			return nil, err
		}
		out[p] = b
	}
	return out, rows.Err()
}

func scanSegments(rows *sql.Rows) ([]Segment, error) {
	defer rows.Close()
	out := []Segment{}
	for rows.Next() {
		var seg Segment
		var st, en string
		var prot int
		if err := rows.Scan(&seg.ID, &seg.CameraID, &st, &en, &seg.Path, &seg.Bytes, &prot); err != nil {
			return nil, err
		}
		seg.Start, seg.End, seg.Protected = mustTime(st), mustTime(en), prot == 1
		out = append(out, seg)
	}
	return out, rows.Err()
}

const segCols = `id,camera_id,start_utc,end_utc,path,bytes,protected`

// SegmentsBetween returns segments of a camera overlapping [from, to).
func (s *Store) SegmentsBetween(ctx context.Context, cameraID string, from, to time.Time) ([]Segment, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+segCols+` FROM segments
		WHERE camera_id=? AND start_utc<? AND end_utc>? ORDER BY start_utc`, cameraID, FormatTime(to), FormatTime(from))
	if err != nil {
		return nil, err
	}
	return scanSegments(rows)
}

// SegmentByID returns one segment.
func (s *Store) SegmentByID(ctx context.Context, id int64) (*Segment, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+segCols+` FROM segments WHERE id=?`, id)
	if err != nil {
		return nil, err
	}
	segs, err := scanSegments(rows)
	if err != nil {
		return nil, err
	}
	if len(segs) == 0 {
		return nil, ErrNotFound
	}
	return &segs[0], nil
}

// OldestSegments returns up to limit unprotected segments, oldest first.
func (s *Store) OldestSegments(ctx context.Context, limit int) ([]Segment, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+segCols+` FROM segments WHERE protected=0 ORDER BY start_utc LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	return scanSegments(rows)
}

// SegmentsForAgeRetention returns unprotected segments of a camera that ended
// before t, except those that overlap an event of one of alertTypes and ended
// after alertCutoff (they are kept longer: tiered retention).
func (s *Store) SegmentsForAgeRetention(ctx context.Context, cameraID string, t, alertCutoff time.Time, alertTypes []string, limit int) ([]Segment, error) {
	if len(alertTypes) == 0 || !alertCutoff.Before(t) {
		return s.SegmentsOlderThan(ctx, cameraID, t, limit)
	}
	in := strings.TrimSuffix(strings.Repeat("?,", len(alertTypes)), ",")
	args := []any{cameraID, FormatTime(t), FormatTime(alertCutoff)}
	for _, a := range alertTypes {
		args = append(args, a)
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, `SELECT `+segCols+` FROM segments sg WHERE camera_id=? AND protected=0 AND end_utc<?
		AND NOT (end_utc>=? AND EXISTS (SELECT 1 FROM events e WHERE e.camera_id=sg.camera_id AND e.type IN (`+in+`)
			AND e.start_utc<sg.end_utc AND coalesce(e.end_utc, e.start_utc)>=sg.start_utc))
		ORDER BY start_utc LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	return scanSegments(rows)
}

// SegmentsOlderThan returns unprotected segments of a camera that ended before t.
func (s *Store) SegmentsOlderThan(ctx context.Context, cameraID string, t time.Time, limit int) ([]Segment, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+segCols+` FROM segments WHERE camera_id=? AND protected=0 AND end_utc<? ORDER BY start_utc LIMIT ?`,
		cameraID, FormatTime(t), limit)
	if err != nil {
		return nil, err
	}
	return scanSegments(rows)
}

// DeleteSegment removes a segment row.
func (s *Store) DeleteSegment(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM segments WHERE id=?`, id)
	return err
}

// DeleteSegmentByPath removes a segment row by path.
func (s *Store) DeleteSegmentByPath(ctx context.Context, path string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM segments WHERE path=?`, path)
	return err
}

// SetSegmentProtected marks a segment as protected from retention, or not.
// Callers that let people protect use ProtectSegmentWithin (capped).
func (s *Store) SetSegmentProtected(ctx context.Context, id int64, protected bool) error {
	v := 0
	if protected {
		v = 1
	}
	return s.execOne(ctx, `UPDATE segments SET protected=? WHERE id=?`, v, id)
}

// ErrProtectLimit: protecting the segment would pass a cap.
var ErrProtectLimit = errors.New("protected recordings limit reached")

// ProtectSegmentWithin protects a segment of one of the configured cameras
// only while the protected bytes of its camera stay within cameraCap and
// those of every configured camera within totalCap (one statement, so two
// requests cannot both pass a cap). A removed camera's protected clips do
// not count: nobody can unprotect them, and retention ages them out
// (recording.Retention). An already protected segment is left as it is.
func (s *Store) ProtectSegmentWithin(ctx context.Context, id, cameraCap, totalCap int64, configured []string) error {
	if len(configured) == 0 {
		return ErrProtectLimit
	}
	in := strings.TrimSuffix(strings.Repeat("?,", len(configured)), ",")
	args := []any{id}
	for _, c := range configured {
		args = append(args, c)
	}
	args = append(args, cameraCap)
	for _, c := range configured {
		args = append(args, c)
	}
	args = append(args, totalCap)
	res, err := s.db.ExecContext(ctx, `UPDATE segments SET protected=1 WHERE id=? AND protected=0 AND camera_id IN (`+in+`)
		AND bytes + (SELECT coalesce(sum(p.bytes),0) FROM segments p WHERE p.protected=1 AND p.camera_id=segments.camera_id) <= ?
		AND bytes + (SELECT coalesce(sum(p.bytes),0) FROM segments p WHERE p.protected=1 AND p.camera_id IN (`+in+`)) <= ?`, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return nil
	}
	var protected bool
	switch err := s.db.QueryRowContext(ctx, `SELECT protected FROM segments WHERE id=?`, id).Scan(&protected); {
	case errors.Is(err, sql.ErrNoRows):
		return ErrNotFound
	case err != nil:
		return err
	case protected:
		return nil
	}
	return ErrProtectLimit
}

// ProtectedBytes returns the protected bytes of one camera and of all.
func (s *Store) ProtectedBytes(ctx context.Context, cameraID string) (camera, total int64, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT coalesce(sum(CASE WHEN camera_id=? THEN bytes END),0), coalesce(sum(bytes),0)
		FROM segments WHERE protected=1`, cameraID).Scan(&camera, &total)
	return camera, total, err
}

// ProtectedSegmentsOlderThan returns up to limit protected segments of a
// camera that ended before t, oldest first (a removed camera's, which
// nobody can unprotect any more).
func (s *Store) ProtectedSegmentsOlderThan(ctx context.Context, cameraID string, t time.Time, limit int) ([]Segment, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+segCols+` FROM segments WHERE camera_id=? AND protected=1 AND end_utc<? ORDER BY start_utc LIMIT ?`,
		cameraID, FormatTime(t), limit)
	if err != nil {
		return nil, err
	}
	return scanSegments(rows)
}

// OldestProtectedSegments returns up to limit protected segments, oldest
// first: what retention frees only when nothing unprotected is left and
// recording would otherwise stop on a full disk.
func (s *Store) OldestProtectedSegments(ctx context.Context, limit int) ([]Segment, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+segCols+` FROM segments WHERE protected=1 ORDER BY start_utc LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	return scanSegments(rows)
}

// SegmentCameraIDs returns the camera ids that have recordings, configured
// or not (a removed camera's stay until retention).
func (s *Store) SegmentCameraIDs(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT camera_id FROM segments ORDER BY camera_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// SegmentStats summarizes stored recordings per camera.
type SegmentStats struct {
	CameraID string    `json:"camera_id"`
	Count    int64     `json:"count"`
	Bytes    int64     `json:"bytes"`
	Oldest   time.Time `json:"oldest"`
	Newest   time.Time `json:"newest"`
}

// Stats returns per-camera totals.
func (s *Store) Stats(ctx context.Context) ([]SegmentStats, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT camera_id,count(*),coalesce(sum(bytes),0),min(start_utc),max(end_utc) FROM segments GROUP BY camera_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SegmentStats{}
	for rows.Next() {
		var st SegmentStats
		var o, n string
		if err := rows.Scan(&st.CameraID, &st.Count, &st.Bytes, &o, &n); err != nil {
			return nil, err
		}
		st.Oldest, st.Newest = mustTime(o), mustTime(n)
		out = append(out, st)
	}
	return out, rows.Err()
}

// RateSince returns bytes and recorded seconds for segments starting after t.
func (s *Store) RateSince(ctx context.Context, cameraID string, t time.Time) (bytes int64, seconds float64, err error) {
	rows, err := s.db.QueryContext(ctx, `SELECT start_utc,end_utc,bytes FROM segments WHERE camera_id=? AND start_utc>=?`, cameraID, FormatTime(t))
	if err != nil {
		return 0, 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var st, en string
		var b int64
		if err := rows.Scan(&st, &en, &b); err != nil {
			return 0, 0, err
		}
		bytes += b
		seconds += mustTime(en).Sub(mustTime(st)).Seconds()
	}
	return bytes, seconds, rows.Err()
}

// Gap is a stretch without recording between two segments.
type Gap struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

// GapsSince returns holes longer than minGap between the camera's segments
// that end after t, oldest first. Overlapping segments are merged.
func (s *Store) GapsSince(ctx context.Context, cameraID string, t time.Time, minGap time.Duration) ([]Gap, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT start_utc,end_utc FROM segments WHERE camera_id=? AND end_utc>? ORDER BY start_utc`,
		cameraID, FormatTime(t))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Gap{}
	var prevEnd time.Time
	for rows.Next() {
		var st, en string
		if err := rows.Scan(&st, &en); err != nil {
			return nil, err
		}
		start, end := mustTime(st), mustTime(en)
		if !prevEnd.IsZero() && start.Sub(prevEnd) > minGap {
			out = append(out, Gap{From: prevEnd, To: start})
		}
		if end.After(prevEnd) {
			prevEnd = end
		}
	}
	return out, rows.Err()
}

// ---------- events ----------

// Event is a camera event such as motion.
type Event struct {
	ID       int64      `json:"id"`
	CameraID string     `json:"camera_id"`
	Type     string     `json:"type"`
	Start    time.Time  `json:"start"`
	End      *time.Time `json:"end,omitempty"`
	Meta     string     `json:"meta,omitempty"`
	// Snapshot is set by the API when a picture exists (ADR-0015).
	Snapshot bool `json:"snapshot,omitempty"`
}

// OpenEvent inserts an event without end and returns its id.
func (s *Store) OpenEvent(ctx context.Context, cameraID, typ string, start time.Time, meta string) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO events(camera_id,type,start_utc,meta) VALUES(?,?,?,?)`, cameraID, typ, FormatTime(start), meta)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// CloseEvent sets an event's end time.
func (s *Store) CloseEvent(ctx context.Context, id int64, end time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE events SET end_utc=? WHERE id=? AND end_utc IS NULL`, FormatTime(end), id)
	return err
}

// CloseDanglingEvents closes events left open (e.g. after a restart).
func (s *Store) CloseDanglingEvents(ctx context.Context, cameraID string, maxLen time.Duration) error {
	rows, err := s.db.QueryContext(ctx, `SELECT id,start_utc FROM events WHERE camera_id=? AND end_utc IS NULL`, cameraID)
	if err != nil {
		return err
	}
	type open struct {
		id    int64
		start time.Time
	}
	var list []open
	for rows.Next() {
		var o open
		var st string
		if err := rows.Scan(&o.id, &st); err != nil {
			rows.Close()
			return err
		}
		o.start = mustTime(st)
		list = append(list, o)
	}
	rows.Close()
	for _, o := range list {
		end := o.start.Add(maxLen)
		if end.After(time.Now()) {
			end = time.Now()
		}
		if err := s.CloseEvent(ctx, o.id, end); err != nil {
			return err
		}
	}
	return nil
}

// EventMetaLoadMax is the longest event meta (bytes) EventByID returns; a
// longer one, which only rows written before the API capped detector meta
// can have, reads as "".
const EventMetaLoadMax = 16384

// eventCols are the columns EventByID reads (meta up to EventMetaLoadMax
// bytes).
var eventCols = `id,camera_id,type,start_utc,end_utc,CASE WHEN octet_length(meta)<=` + strconv.Itoa(EventMetaLoadMax) + ` THEN meta ELSE '' END`

// EventsBetween lists events of a camera ("" = all) that start in [from, to),
// newest first, without their meta (Meta is ""): lists load thousands of
// rows on every poll and nothing that lists events reads it.
func (s *Store) EventsBetween(ctx context.Context, cameraID string, from, to time.Time, limit int) ([]Event, error) {
	q := `SELECT id,camera_id,type,start_utc,end_utc FROM events WHERE start_utc>=? AND start_utc<?`
	args := []any{FormatTime(from), FormatTime(to)}
	if cameraID != "" {
		q += ` AND camera_id=?`
		args = append(args, cameraID)
	}
	q += ` ORDER BY start_utc DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		var st string
		var en sql.NullString
		if err := rows.Scan(&e.ID, &e.CameraID, &e.Type, &st, &en); err != nil {
			return nil, err
		}
		e.Start = mustTime(st)
		if en.Valid {
			t := mustTime(en.String)
			e.End = &t
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// EventByID returns one event, with its meta.
func (s *Store) EventByID(ctx context.Context, id int64) (*Event, error) {
	var e Event
	var st string
	var en sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT `+eventCols+` FROM events WHERE id=?`, id).
		Scan(&e.ID, &e.CameraID, &e.Type, &st, &en, &e.Meta)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	e.Start = mustTime(st)
	if en.Valid {
		t := mustTime(en.String)
		e.End = &t
	}
	return &e, nil
}

// OldestEventStart returns the start of the oldest stored event (ok=false
// when there is none).
func (s *Store) OldestEventStart(ctx context.Context) (time.Time, bool, error) {
	var v sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT min(start_utc) FROM events`).Scan(&v); err != nil {
		return time.Time{}, false, err
	}
	if !v.Valid || v.String == "" {
		return time.Time{}, false, nil
	}
	return mustTime(v.String), true, nil
}

// PurgeEventsBefore deletes events older than t.
func (s *Store) PurgeEventsBefore(ctx context.Context, t time.Time) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM events WHERE start_utc<?`, FormatTime(t))
	return err
}

// ---------- review ----------

// SeenItems returns which of ids the user has marked seen.
func (s *Store) SeenItems(ctx context.Context, userID int64, ids []int64) (map[int64]bool, error) {
	out := map[int64]bool{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.priv.QueryContext(ctx, `SELECT item_id FROM review_seen WHERE user_id=? AND item_id>=? AND item_id<=?`,
		userID, slices.Min(ids), slices.Max(ids))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	want := map[int64]bool{}
	for _, id := range ids {
		want[id] = true
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		if want[id] {
			out[id] = true
		}
	}
	return out, rows.Err()
}

// MarkSeen records review items as seen by the user.
func (s *Store) MarkSeen(ctx context.Context, userID int64, ids []int64) error {
	tx, err := s.priv.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := FormatTime(time.Now())
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO review_seen(user_id,item_id,seen_at) VALUES(?,?,?)`, userID, id, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// PurgeSeenBefore drops seen marks older than t, and those whose event
// retention deleted: a user keeps at most one mark per existing event.
func (s *Store) PurgeSeenBefore(ctx context.Context, t time.Time) error {
	if _, err := s.priv.ExecContext(ctx, `DELETE FROM review_seen WHERE seen_at<?`, FormatTime(t)); err != nil {
		return err
	}
	// Events live in the shared index, marks in the private database: event
	// ids only grow and retention deletes the oldest, so marks below the
	// oldest event left are of deleted events (all of them when none is).
	var oldest sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT min(id) FROM events`).Scan(&oldest); err != nil {
		return err
	}
	if !oldest.Valid {
		_, err := s.priv.ExecContext(ctx, `DELETE FROM review_seen`)
		return err
	}
	_, err := s.priv.ExecContext(ctx, `DELETE FROM review_seen WHERE item_id<?`, oldest.Int64)
	return err
}

// EventCameras returns the camera of each of ids that is an existing event.
func (s *Store) EventCameras(ctx context.Context, ids []int64) (map[int64]string, error) {
	out := make(map[int64]string, len(ids))
	for chunk := range slices.Chunk(ids, 500) {
		q := `SELECT id, camera_id FROM events WHERE id IN (?` + strings.Repeat(",?", len(chunk)-1) + `)`
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		rows, err := s.db.QueryContext(ctx, q, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id int64
			var cam string
			if err := rows.Scan(&id, &cam); err != nil {
				rows.Close()
				return nil, err
			}
			out[id] = cam
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// ---------- push and settings ----------

// PushSub is a stored Web Push subscription.
type PushSub struct {
	ID       int64
	UserID   int64
	Endpoint string
	P256dh   string
	Auth     string
}

// Devices one user keeps: registering one more drops the one used least
// recently (its last delivery, or its registration).
const (
	MaxPushSubs  = 10 // browsers (Web Push subscriptions)
	MaxFCMTokens = 10 // Android apps
)

// deadPushFails failed deliveries in a row drop a subscription that never
// delivered: an endpoint a push service does not know (PushFailed).
const deadPushFails = 3

// SavePushSub inserts or refreshes a subscription (keyed by endpoint) and
// binds it to the session that made it: it ends with that session. The
// user keeps at most MaxPushSubs.
func (s *Store) SavePushSub(ctx context.Context, userID int64, sessionHash []byte, endpoint, p256dh, auth, ua string) error {
	var sh any // a nil session stores NULL (a subscription not tied to one)
	if len(sessionHash) > 0 {
		sh = sessionHash
	}
	tx, err := s.priv.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO push_subscriptions(user_id,session_hash,endpoint,p256dh,auth,user_agent,created_at) VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(endpoint) DO UPDATE SET user_id=excluded.user_id, session_hash=excluded.session_hash, p256dh=excluded.p256dh, auth=excluded.auth,
		user_agent=excluded.user_agent, created_at=excluded.created_at, fails=0`,
		userID, sh, endpoint, p256dh, auth, ua, FormatTime(time.Now())); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM push_subscriptions WHERE user_id=? AND id NOT IN (
		SELECT id FROM push_subscriptions WHERE user_id=? ORDER BY max(created_at, COALESCE(last_ok,'')) DESC, id DESC LIMIT ?)`,
		userID, userID, MaxPushSubs); err != nil {
		return err
	}
	return tx.Commit()
}

// DeletePushSub removes a subscription; userID 0 removes it for anyone (expired).
func (s *Store) DeletePushSub(ctx context.Context, userID int64, endpoint string) error {
	if userID == 0 {
		_, err := s.priv.ExecContext(ctx, `DELETE FROM push_subscriptions WHERE endpoint=?`, endpoint)
		return err
	}
	_, err := s.priv.ExecContext(ctx, `DELETE FROM push_subscriptions WHERE endpoint=? AND user_id=?`, endpoint, userID)
	return err
}

// PushSubs lists subscriptions, all users when userID is 0.
func (s *Store) PushSubs(ctx context.Context, userID int64) ([]PushSub, error) {
	// A subscription of an expired session (not purged yet) is not used.
	q, args := `SELECT p.id,p.user_id,p.endpoint,p.p256dh,p.auth FROM push_subscriptions p
		LEFT JOIN sessions s ON s.id_hash=p.session_hash
		WHERE (p.session_hash IS NULL OR s.expires_at>?)`, []any{Now()}
	if userID != 0 {
		q += ` AND p.user_id=?`
		args = append(args, userID)
	}
	rows, err := s.priv.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PushSub
	for rows.Next() {
		var p PushSub
		if err := rows.Scan(&p.ID, &p.UserID, &p.Endpoint, &p.P256dh, &p.Auth); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// MarkPushOK records a successful delivery.
func (s *Store) MarkPushOK(ctx context.Context, id int64) {
	_, _ = s.priv.ExecContext(ctx, `UPDATE push_subscriptions SET last_ok=?, fails=0 WHERE id=?`, FormatTime(time.Now()), id)
}

// PushFailed counts a failed delivery to subscription id. One that never
// delivered and failed deadPushFails times in a row is deleted (true).
func (s *Store) PushFailed(ctx context.Context, id int64) (bool, error) {
	if _, err := s.priv.ExecContext(ctx, `UPDATE push_subscriptions SET fails=fails+1 WHERE id=?`, id); err != nil {
		return false, err
	}
	res, err := s.priv.ExecContext(ctx, `DELETE FROM push_subscriptions WHERE id=? AND last_ok IS NULL AND fails>=?`, id, deadPushFails)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// SaveFCMToken registers (or moves to userID) an app's FCM token (ADR-0020).
// The user keeps at most MaxFCMTokens.
func (s *Store) SaveFCMToken(ctx context.Context, userID int64, sessionHash []byte, token, ua string) error {
	tx, err := s.priv.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO fcm_tokens(token,user_id,session_hash,user_agent,created_at) VALUES(?,?,?,?,?)
		ON CONFLICT(token) DO UPDATE SET user_id=excluded.user_id, session_hash=excluded.session_hash, user_agent=excluded.user_agent, created_at=excluded.created_at`,
		token, userID, sessionHash, ua, FormatTime(time.Now())); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM fcm_tokens WHERE user_id=? AND token NOT IN (
		SELECT token FROM fcm_tokens WHERE user_id=? ORDER BY max(created_at, last_ok) DESC, rowid DESC LIMIT ?)`,
		userID, userID, MaxFCMTokens); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteFCMToken forgets a token; userID 0 removes it for anyone (expired).
func (s *Store) DeleteFCMToken(ctx context.Context, userID int64, token string) error {
	if userID == 0 {
		_, err := s.priv.ExecContext(ctx, `DELETE FROM fcm_tokens WHERE token=?`, token)
		return err
	}
	_, err := s.priv.ExecContext(ctx, `DELETE FROM fcm_tokens WHERE token=? AND user_id=?`, token, userID)
	return err
}

// FCMTokens lists one user's app tokens.
func (s *Store) FCMTokens(ctx context.Context, userID int64) ([]string, error) {
	rows, err := s.priv.QueryContext(ctx, `SELECT f.token FROM fcm_tokens f
		JOIN sessions s ON s.id_hash=f.session_hash
		WHERE f.user_id=? AND s.expires_at>?`, userID, Now())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// MarkFCMOK records a successful delivery to a token.
func (s *Store) MarkFCMOK(ctx context.Context, token string) {
	_, _ = s.priv.ExecContext(ctx, `UPDATE fcm_tokens SET last_ok=? WHERE token=?`, FormatTime(time.Now()), token)
}

// Setting returns a setting value, or def when unset.
func (s *Store) Setting(ctx context.Context, key, def string) string {
	var v string
	if err := s.priv.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, key).Scan(&v); err != nil {
		return def
	}
	return v
}

// SetSetting stores a setting value.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.priv.ExecContext(ctx, `INSERT INTO settings(key,value,updated_at) VALUES(?,?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`, key, value, FormatTime(time.Now()))
	return err
}

// ---------- audit ----------

// AuditEntry is one audit log row.
type AuditEntry struct {
	ID       int64  `json:"id"`
	At       string `json:"at"`
	Username string `json:"username"`
	Action   string `json:"action"`
	Target   string `json:"target"`
	OK       bool   `json:"ok"`
	Detail   string `json:"detail"`
}

// Audit field limits, in bytes. Audit text can come from a client (login
// names, paths), so each field is capped to keep the table small.
const (
	auditMaxUser   = 64
	auditMaxAction = 64
	auditMaxTarget = 512
	auditMaxDetail = 512
)

// clip shortens s to at most n bytes without cutting a UTF-8 character and
// replaces invalid UTF-8, so the stored text is always valid.
func clip(s string, n int) string {
	s = strings.ToValidUTF8(s, "�")
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// Audit records an action. Detail must already be redacted. Fields are capped
// (auditMax*) so a long login name or path cannot grow the log.
func (s *Store) Audit(ctx context.Context, userID int64, username, action, target string, ok bool, detail string) error {
	var uid any
	if userID > 0 {
		uid = userID
	}
	_, err := s.priv.ExecContext(ctx, `INSERT INTO audit(at,user_id,username,action,target,ok,detail) VALUES(?,?,?,?,?,?,?)`,
		Now(), uid, clip(username, auditMaxUser), clip(action, auditMaxAction), clip(target, auditMaxTarget), ok, clip(detail, auditMaxDetail))
	return err
}

// AuditProtectValve is the audit action of protected clips deleted by the
// full-disk valve (recording.Retention); the API notifies the admins.
const AuditProtectValve = "segment.protect.valve"

// AuditAfter returns up to limit audit rows of action with an id above
// afterID, oldest first.
func (s *Store) AuditAfter(ctx context.Context, action string, afterID int64, limit int) ([]AuditEntry, error) {
	rows, err := s.priv.QueryContext(ctx, `SELECT id,at,username,action,target,ok,detail FROM audit WHERE action=? AND id>? ORDER BY id LIMIT ?`, action, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.At, &e.Username, &e.Action, &e.Target, &e.OK, &e.Detail); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// LastAuditID returns the id of the newest audit row (0 when none).
func (s *Store) LastAuditID(ctx context.Context) (int64, error) {
	var id int64
	err := s.priv.QueryRowContext(ctx, `SELECT coalesce(max(id),0) FROM audit`).Scan(&id)
	return id, err
}

// RecorderNotice records something the recorder must tell the API (it has
// no audit log of its own): a row of the shared index's recorder_notices.
func (s *Store) RecorderNotice(ctx context.Context, action, target, detail string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO recorder_notices(at,action,target,detail) VALUES(?,?,?,?)`,
		Now(), clip(action, auditMaxAction), clip(target, auditMaxTarget), clip(detail, auditMaxDetail))
	return err
}

// RecorderNoticesAfter lists recorder notices of action with id > afterID,
// oldest first (as audit entries: the API copies them to the audit log).
func (s *Store) RecorderNoticesAfter(ctx context.Context, action string, afterID int64, limit int) ([]AuditEntry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,at,action,target,detail FROM recorder_notices WHERE action=? AND id>? ORDER BY id LIMIT ?`, action, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		e := AuditEntry{OK: true}
		if err := rows.Scan(&e.ID, &e.At, &e.Action, &e.Target, &e.Detail); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// LastRecorderNoticeID returns the id of the newest recorder notice (0 when none).
func (s *Store) LastRecorderNoticeID(ctx context.Context) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT coalesce(max(id),0) FROM recorder_notices`).Scan(&id)
	return id, err
}

// PruneAudit deletes audit rows older than maxAge and, beyond that, the oldest
// ones above maxRows. Upload and folder rows (nas.*) are kept: they are the
// only record of who put a file in a shared folder (see Authors).
func (s *Store) PruneAudit(ctx context.Context, maxAge time.Duration, maxRows int) error {
	if _, err := s.priv.ExecContext(ctx, `DELETE FROM audit WHERE at<? AND action NOT LIKE 'nas.%'`, FormatTime(time.Now().Add(-maxAge))); err != nil {
		return err
	}
	if maxRows <= 0 {
		return nil
	}
	_, err := s.priv.ExecContext(ctx, `DELETE FROM audit WHERE action NOT LIKE 'nas.%' AND id <= (
		SELECT id FROM audit WHERE action NOT LIKE 'nas.%' ORDER BY id DESC LIMIT 1 OFFSET ?)`, maxRows)
	return err
}

// Authors maps each Cloud path to the user who last uploaded or created it
// (the audit log is the only record of who put a file in a shared folder).
func (s *Store) Authors(ctx context.Context, paths []string) (map[string]string, error) {
	out := map[string]string{}
	if len(paths) == 0 {
		return out, nil
	}
	q := `SELECT target, username FROM audit WHERE ok=1 AND action IN ('nas.upload','nas.mkdir') AND target IN (?` +
		strings.Repeat(",?", len(paths)-1) + `) ORDER BY id`
	args := make([]any, len(paths))
	for i, p := range paths {
		args[i] = p
	}
	rows, err := s.priv.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var t, u string
		if err := rows.Scan(&t, &u); err != nil {
			return nil, err
		}
		out[t] = u
	}
	return out, rows.Err()
}

// ListAudit returns the newest audit rows.
func (s *Store) ListAudit(ctx context.Context, limit int) ([]AuditEntry, error) {
	return s.UserAudit(ctx, "", limit)
}

// UserAudit lists the newest audit entries, only one user's when username is set.
func (s *Store) UserAudit(ctx context.Context, username string, limit int) ([]AuditEntry, error) {
	rows, err := s.priv.QueryContext(ctx, `SELECT id,at,username,action,target,ok,detail FROM audit WHERE ?='' OR username=? ORDER BY id DESC LIMIT ?`, username, username, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditEntry{}
	for rows.Next() {
		var a AuditEntry
		if err := rows.Scan(&a.ID, &a.At, &a.Username, &a.Action, &a.Target, &a.OK, &a.Detail); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ---------- recorder status ----------

// RecorderStatus is the recorder's heartbeat for one camera.
type RecorderStatus struct {
	CameraID    string    `json:"camera_id"`
	State       string    `json:"state"`
	Since       time.Time `json:"since"`
	Heartbeat   time.Time `json:"heartbeat"`
	Restarts    int       `json:"restarts"`
	Restarts1h  int       `json:"restarts_1h"`
	LastError   string    `json:"last_error"`
	CurrentFile string    `json:"current_file"`
}

// PutRecorderStatus upserts the status row. LastError must be redacted.
func (s *Store) PutRecorderStatus(ctx context.Context, st RecorderStatus) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO recorder_status(camera_id,state,since_utc,heartbeat_utc,restarts,restarts_1h,last_error,current_file)
		VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(camera_id) DO UPDATE SET state=excluded.state, since_utc=excluded.since_utc,
		heartbeat_utc=excluded.heartbeat_utc, restarts=excluded.restarts, restarts_1h=excluded.restarts_1h,
		last_error=excluded.last_error, current_file=excluded.current_file`,
		st.CameraID, st.State, FormatTime(st.Since), FormatTime(st.Heartbeat), st.Restarts, st.Restarts1h, st.LastError, st.CurrentFile)
	return err
}

// ListRecorderStatus returns all recorder status rows.
func (s *Store) ListRecorderStatus(ctx context.Context) ([]RecorderStatus, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT camera_id,state,since_utc,heartbeat_utc,restarts,restarts_1h,last_error,current_file FROM recorder_status ORDER BY camera_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RecorderStatus{}
	for rows.Next() {
		var st RecorderStatus
		var since, hb string
		if err := rows.Scan(&st.CameraID, &st.State, &since, &hb, &st.Restarts, &st.Restarts1h, &st.LastError, &st.CurrentFile); err != nil {
			return nil, err
		}
		st.Since, st.Heartbeat = mustTime(since), mustTime(hb)
		out = append(out, st)
	}
	return out, rows.Err()
}
