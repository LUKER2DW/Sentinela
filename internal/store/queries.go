package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ---------- users ----------

// User is an account. PWHash is never serialized.
type User struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	Role      string `json:"role"`
	CreatedAt string `json:"created_at"`
	PWHash    string `json:"-"`
}

// CountUsers returns the number of users.
func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

// CreateUser inserts a user and returns its id.
func (s *Store) CreateUser(ctx context.Context, username, pwHash, role string) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO users(username,pw_hash,role,created_at) VALUES(?,?,?,?)`, username, pwHash, role, Now())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// CreateFirstUser inserts an admin only if no user exists (atomic).
func (s *Store) CreateFirstUser(ctx context.Context, username, pwHash string) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO users(username,pw_hash,role,created_at)
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
	if err := row.Scan(&u.ID, &u.Username, &u.PWHash, &u.Role, &u.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &u, nil
}

// UserByName looks a user up by username (case-insensitive).
func (s *Store) UserByName(ctx context.Context, username string) (*User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT id,username,pw_hash,role,created_at FROM users WHERE username=?`, username))
}

// UserByID looks a user up by id.
func (s *Store) UserByID(ctx context.Context, id int64) (*User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT id,username,pw_hash,role,created_at FROM users WHERE id=?`, id))
}

// ListUsers returns all users.
func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,username,pw_hash,role,created_at FROM users ORDER BY username`)
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
	return s.execOne(ctx, `UPDATE users SET role=? WHERE id=?`, role, id)
}

// UpdateUserPassword changes a user's password hash and drops their sessions.
func (s *Store) UpdateUserPassword(ctx context.Context, id int64, pwHash string) error {
	if err := s.execOne(ctx, `UPDATE users SET pw_hash=? WHERE id=?`, pwHash, id); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=?`, id)
	return err
}

// DeleteUser removes a user (sessions cascade).
func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	return s.execOne(ctx, `DELETE FROM users WHERE id=?`, id)
}

// CountAdmins returns the number of admin users.
func (s *Store) CountAdmins(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE role='admin'`).Scan(&n)
	return n, err
}

func (s *Store) execOne(ctx context.Context, q string, args ...any) error {
	res, err := s.db.ExecContext(ctx, q, args...)
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
}

// CreateSession stores a session keyed by the token hash.
func (s *Store) CreateSession(ctx context.Context, idHash []byte, userID int64, csrf string, expires time.Time, ua string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions(id_hash,user_id,csrf,created_at,expires_at,user_agent) VALUES(?,?,?,?,?,?)`,
		idHash, userID, csrf, Now(), FormatTime(expires), ua)
	return err
}

// SessionByHash returns a non-expired session with its user.
func (s *Store) SessionByHash(ctx context.Context, idHash []byte) (*Session, *User, error) {
	var sess Session
	var exp string
	var u User
	err := s.db.QueryRowContext(ctx, `SELECT s.id_hash,s.user_id,s.csrf,s.expires_at,u.id,u.username,u.pw_hash,u.role,u.created_at
		FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.id_hash=? AND s.expires_at>?`, idHash, Now()).
		Scan(&sess.IDHash, &sess.UserID, &sess.CSRF, &exp, &u.ID, &u.Username, &u.PWHash, &u.Role, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	sess.ExpiresAt = mustTime(exp)
	return &sess, &u, nil
}

// DeleteSession removes one session.
func (s *Store) DeleteSession(ctx context.Context, idHash []byte) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id_hash=?`, idHash)
	return err
}

// PurgeExpiredSessions deletes expired sessions.
func (s *Store) PurgeExpiredSessions(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at<=?`, Now())
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

// SegmentPaths returns the set of indexed paths for a camera.
func (s *Store) SegmentPaths(ctx context.Context, cameraID string) (map[string]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT path,bytes FROM segments WHERE camera_id=?`, cameraID)
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

// SetSegmentProtected marks a segment as protected from retention.
func (s *Store) SetSegmentProtected(ctx context.Context, id int64, protected bool) error {
	v := 0
	if protected {
		v = 1
	}
	return s.execOne(ctx, `UPDATE segments SET protected=? WHERE id=?`, v, id)
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

// ---------- events ----------

// Event is a camera event such as motion.
type Event struct {
	ID       int64      `json:"id"`
	CameraID string     `json:"camera_id"`
	Type     string     `json:"type"`
	Start    time.Time  `json:"start"`
	End      *time.Time `json:"end,omitempty"`
	Meta     string     `json:"meta,omitempty"`
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

// EventsBetween lists events of a camera ("" = all) that start in [from, to), newest first.
func (s *Store) EventsBetween(ctx context.Context, cameraID string, from, to time.Time, limit int) ([]Event, error) {
	q := `SELECT id,camera_id,type,start_utc,end_utc,meta FROM events WHERE start_utc>=? AND start_utc<?`
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
		if err := rows.Scan(&e.ID, &e.CameraID, &e.Type, &st, &en, &e.Meta); err != nil {
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

// PurgeEventsBefore deletes events older than t.
func (s *Store) PurgeEventsBefore(ctx context.Context, t time.Time) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM events WHERE start_utc<?`, FormatTime(t))
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

// Audit records an action. Detail must already be redacted.
func (s *Store) Audit(ctx context.Context, userID int64, username, action, target string, ok bool, detail string) error {
	var uid any
	if userID > 0 {
		uid = userID
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO audit(at,user_id,username,action,target,ok,detail) VALUES(?,?,?,?,?,?,?)`,
		Now(), uid, username, action, target, ok, detail)
	return err
}

// ListAudit returns the newest audit rows.
func (s *Store) ListAudit(ctx context.Context, limit int) ([]AuditEntry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,at,username,action,target,ok,detail FROM audit ORDER BY id DESC LIMIT ?`, limit)
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
	LastError   string    `json:"last_error"`
	CurrentFile string    `json:"current_file"`
}

// PutRecorderStatus upserts the status row. LastError must be redacted.
func (s *Store) PutRecorderStatus(ctx context.Context, st RecorderStatus) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO recorder_status(camera_id,state,since_utc,heartbeat_utc,restarts,last_error,current_file)
		VALUES(?,?,?,?,?,?,?) ON CONFLICT(camera_id) DO UPDATE SET state=excluded.state, since_utc=excluded.since_utc,
		heartbeat_utc=excluded.heartbeat_utc, restarts=excluded.restarts, last_error=excluded.last_error, current_file=excluded.current_file`,
		st.CameraID, st.State, FormatTime(st.Since), FormatTime(st.Heartbeat), st.Restarts, st.LastError, st.CurrentFile)
	return err
}

// ListRecorderStatus returns all recorder status rows.
func (s *Store) ListRecorderStatus(ctx context.Context) ([]RecorderStatus, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT camera_id,state,since_utc,heartbeat_utc,restarts,last_error,current_file FROM recorder_status ORDER BY camera_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RecorderStatus{}
	for rows.Next() {
		var st RecorderStatus
		var since, hb string
		if err := rows.Scan(&st.CameraID, &st.State, &since, &hb, &st.Restarts, &st.LastError, &st.CurrentFile); err != nil {
			return nil, err
		}
		st.Since, st.Heartbeat = mustTime(since), mustTime(hb)
		out = append(out, st)
	}
	return out, rows.Err()
}
