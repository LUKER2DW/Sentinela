package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Recognisable values the move must carry over and scrub from the index.
const (
	legacyTOTP   = "LEGACYTOTPSECRETMARKERJBSWY3DPEHPK3PXP"
	legacyPWHash = "$argon2id$v=19$m=65536,t=3,p=2$LEGACYPWHASHMARKER"
)

// legacyIndex builds an index as a release before the split left it: every
// table in one file, with an account, a session and the rest of the API's
// rows next to a segment and an event.
func legacyIndex(t *testing.T, path string) (userID int64, sessHash []byte) {
	t.Helper()
	ctx := context.Background()
	ix, err := OpenIndex(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	db := ix.DB()
	exec := func(q string, args ...any) sql.Result {
		t.Helper()
		res, err := db.ExecContext(ctx, q, args...)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return res
	}
	res := exec(`INSERT INTO users(username,pw_hash,role,created_at,totp_secret,email) VALUES('owner',?,'admin',?,?,'owner@example.com')`,
		legacyPWHash, Now(), legacyTOTP)
	userID, _ = res.LastInsertId()
	h := sha256.Sum256([]byte("legacy-cookie"))
	sessHash = h[:]
	exec(`INSERT INTO sessions(id_hash,user_id,csrf,created_at,expires_at,mfa,host,zone) VALUES(?,?,'csrf1',?,?,1,'nvr.example.com','lan')`,
		sessHash, userID, Now(), FormatTime(time.Now().Add(time.Hour)))
	exec(`INSERT INTO push_subscriptions(user_id,endpoint,p256dh,auth,created_at,session_hash) VALUES(?,'https://push.example.com/1','k','a',?,?)`,
		userID, Now(), sessHash)
	exec(`INSERT INTO fcm_tokens(token,user_id,session_hash,created_at) VALUES('fcm1',?,?,?)`, userID, sessHash, Now())
	exec(`INSERT INTO review_seen(user_id,item_id,seen_at) VALUES(?,7,?)`, userID, Now())
	exec(`INSERT INTO settings(key,value,updated_at) VALUES('notify_mode','{"mode":"all"}',?)`, Now())
	exec(`INSERT INTO audit(at,user_id,username,action,target,ok,detail) VALUES(?,?,'owner','login','',1,'')`, Now(), userID)
	if err := ix.UpsertSegment(ctx, Segment{CameraID: "cam1", Start: time.Now().Add(-time.Minute), End: time.Now(), Path: "cam1/a.mp4", Bytes: 10}); err != nil {
		t.Fatal(err)
	}
	if _, err := ix.OpenEvent(ctx, "cam1", "motion", time.Now(), ""); err != nil {
		t.Fatal(err)
	}
	return userID, sessHash
}

// indexBytes is everything on disk the recorder could read of the index.
func indexBytes(t *testing.T, path string) []byte {
	t.Helper()
	var all []byte
	for _, p := range []string{path, path + "-wal"} {
		b, err := os.ReadFile(p)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		all = append(all, b...)
	}
	return all
}

func TestLegacyIndexMovesToPrivate(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "db", "sentinela.db")
	userID, sessHash := legacyIndex(t, path)

	// The recorder (index only) never drops what the API has not copied.
	ix, err := OpenIndex(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := tablesIn(ctx, ix.DB()); len(got) != len(privateTables) {
		t.Fatalf("OpenIndex removed API tables: %v", got)
	}
	ix.Close()

	for round := 0; round < 2; round++ { // the second open must change nothing
		s, err := Open(ctx, path)
		if err != nil {
			t.Fatalf("open %d: %v", round, err)
		}
		if round == 0 { // the index keeps its copy until the move is final
			if got, _ := tablesIn(ctx, s.db); len(got) != len(privateTables) {
				t.Fatalf("the index lost its copy before the move was final: %v", got)
			}
			if err := s.FinalizeMove(ctx); err != nil {
				t.Fatal(err)
			}
		}
		if got, _ := tablesIn(ctx, s.db); len(got) != 0 {
			t.Fatalf("round %d: API tables left in the index: %v", round, got)
		}
		if n, _ := s.CountUsers(ctx); n != 1 {
			t.Fatalf("round %d: %d users", round, n)
		}
		u, err := s.UserByLogin(ctx, "owner@example.com")
		if err != nil || u.ID != userID || u.TOTPSecret != legacyTOTP || u.PWHash != legacyPWHash || u.Role != "admin" {
			t.Fatalf("round %d: user %+v, %v", round, u, err)
		}
		sess, su, err := s.SessionByHash(ctx, sessHash)
		if err != nil || su.ID != userID || sess.CSRF != "csrf1" || !sess.MFA || sess.Host != "nvr.example.com" || sess.Zone != "lan" {
			t.Fatalf("round %d: session %+v, %v", round, sess, err)
		}
		if subs, err := s.PushSubs(ctx, userID); err != nil || len(subs) != 1 {
			t.Fatalf("round %d: push %v, %v", round, subs, err)
		}
		if toks, err := s.FCMTokens(ctx, userID); err != nil || len(toks) != 1 {
			t.Fatalf("round %d: fcm %v, %v", round, toks, err)
		}
		if seen, err := s.SeenItems(ctx, userID, []int64{7}); err != nil || !seen[7] {
			t.Fatalf("round %d: seen %v, %v", round, seen, err)
		}
		if v := s.Setting(ctx, "notify_mode", ""); v != `{"mode":"all"}` {
			t.Fatalf("round %d: setting %q", round, v)
		}
		if a, err := s.ListAudit(ctx, 10); err != nil || len(a) != 1 {
			t.Fatalf("round %d: audit %v, %v", round, a, err)
		}
		if segs, err := s.OldestSegments(ctx, 10); err != nil || len(segs) != 1 {
			t.Fatalf("round %d: segments %v, %v", round, segs, err)
		}
		if _, ok, err := s.OldestEventStart(ctx); err != nil || !ok {
			t.Fatalf("round %d: events %v, %v", round, ok, err)
		}
		// Sessions still end with their account, inside the private file.
		if round == 1 {
			if err := s.DeleteUser(ctx, userID); err != nil {
				t.Fatal(err)
			}
			if _, _, err := s.SessionByHash(ctx, sessHash); err == nil {
				t.Fatal("session outlived its deleted user")
			}
		}
		s.Close()
	}
	// The secrets are gone from every byte of the index the recorder can read.
	b := indexBytes(t, path)
	for _, secret := range []string{legacyTOTP, "LEGACYPWHASHMARKER", "owner@example.com"} {
		if bytes.Contains(b, []byte(secret)) {
			t.Errorf("index still holds %q after the move", secret)
		}
	}
	fi, err := os.Stat(filepath.Dir(PrivatePath(path)))
	if err != nil {
		t.Fatal(err)
	}
	if os.PathSeparator == '/' && fi.Mode().Perm()&0o077 != 0 {
		t.Errorf("private folder mode %v", fi.Mode().Perm())
	}
}

// What a compromised recorder can write to the index after the move (an
// account, a session of its choosing) is never read: the API's tables there
// are dropped unread on its next start.
func TestIndexForgeryAfterMoveIsIgnored(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "db", "sentinela.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := s.CreateFirstUser(ctx, "admin", "hash")
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

	rec, err := OpenIndex(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := rec.DB().QueryRowContext(ctx, `SELECT count(*) FROM sessions`).Scan(new(int)); err == nil {
		t.Fatal("the recorder's handle reaches a sessions table")
	}
	forged := func(uid int64) []byte {
		h := sha256.Sum256([]byte{'f', byte(uid)})
		return h[:]
	}
	for _, q := range []string{
		`CREATE TABLE users(id INTEGER PRIMARY KEY, username TEXT, pw_hash TEXT, role TEXT, created_at TEXT, cameras TEXT DEFAULT '', totp_secret TEXT DEFAULT '', totp_step INTEGER DEFAULT 0, disabled INTEGER DEFAULT 0, email TEXT DEFAULT '', invite_hash BLOB, invite_expires TEXT DEFAULT '', notify TEXT DEFAULT '')`,
		`CREATE TABLE sessions(id_hash BLOB PRIMARY KEY, user_id INTEGER, csrf TEXT, created_at TEXT, expires_at TEXT, user_agent TEXT DEFAULT '', mfa INTEGER DEFAULT 0, remember INTEGER DEFAULT 1, host TEXT DEFAULT '', zone TEXT DEFAULT '')`,
		`INSERT INTO users(id,username,pw_hash,role,created_at) VALUES(99,'intruder','x','admin','2026-01-01T00:00:00.000Z')`,
	} {
		if _, err := rec.DB().ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for _, uid := range []int64{admin, 99} {
		if _, err := rec.DB().ExecContext(ctx, `INSERT INTO sessions(id_hash,user_id,csrf,created_at,expires_at,mfa) VALUES(?,?,'c',?,?,1)`,
			forged(uid), uid, Now(), FormatTime(time.Now().Add(time.Hour))); err != nil {
			t.Fatal(err)
		}
	}
	rec.Close()

	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, uid := range []int64{admin, 99} {
		if _, _, err := s.SessionByHash(ctx, forged(uid)); err == nil {
			t.Fatalf("forged session for user %d accepted", uid)
		}
	}
	if n, _ := s.CountUsers(ctx); n != 1 {
		t.Fatalf("%d users after the forgery", n)
	}
	if got, _ := tablesIn(ctx, s.db); len(got) != 0 {
		t.Fatalf("forged tables left in the index: %v", got)
	}
}

// A crash after the copy committed and before the drop: the next start only
// drops, it does not copy a second time.
func TestMoveResumesAfterCopy(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "db", "sentinela.db")
	userID, _ := legacyIndex(t, path)
	idx, err := openDB(ctx, path, 0o750, indexMigrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	priv, err := openDB(ctx, PrivatePath(path), 0o700, privateMigrations, "private_migrations")
	if err != nil {
		t.Fatal(err)
	}
	half := &Store{db: idx, priv: priv, indexPath: path}
	present, err := tablesIn(ctx, idx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := half.copyLegacy(ctx, present); err != nil {
		t.Fatal(err)
	}
	half.Close()

	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if n, _ := s.CountUsers(ctx); n != 1 {
		t.Fatalf("%d users: copied twice?", n)
	}
	if a, _ := s.UserAudit(ctx, "owner", 10); len(a) != 1 {
		t.Fatalf("%d audit rows: copied twice?", len(a))
	}
	if _, err := s.UserByID(ctx, userID); err != nil {
		t.Fatal(err)
	}
	if err := s.FinalizeMove(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := tablesIn(ctx, s.db); len(got) != 0 {
		t.Fatalf("API tables left in the index: %v", got)
	}
}

// The private schema is exactly what the index's migrations had built, so
// the move loses no column and later code sees the same tables.
func TestPrivateSchemaMatchesLegacy(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	ix, err := OpenIndex(ctx, filepath.Join(dir, "legacy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	priv, err := openDB(ctx, filepath.Join(dir, "p", "private.db"), 0o700, privateMigrations, "private_migrations")
	if err != nil {
		t.Fatal(err)
	}
	defer priv.Close()
	describe := func(db *sql.DB, table string) string {
		t.Helper()
		var b strings.Builder
		rows, err := db.QueryContext(ctx, `SELECT name,type,"notnull",coalesce(dflt_value,''),pk FROM pragma_table_info(?)`, table)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var name, typ, dflt string
			var notnull, pk int
			if err := rows.Scan(&name, &typ, &notnull, &dflt, &pk); err != nil {
				t.Fatal(err)
			}
			b.WriteString(name + " " + typ + " " + dflt + " " + string(rune('0'+notnull)) + string(rune('0'+pk)) + "\n")
		}
		irows, err := db.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type='index' AND tbl_name=? AND name NOT LIKE 'sqlite_autoindex%' ORDER BY name`, table)
		if err != nil {
			t.Fatal(err)
		}
		defer irows.Close()
		for irows.Next() {
			var n string
			_ = irows.Scan(&n)
			b.WriteString("index " + n + "\n")
		}
		return b.String()
	}
	for _, table := range privateTables {
		if got, want := describe(priv, table), describe(ix.DB(), table); got != want || want == "" {
			t.Errorf("%s:\nprivate:\n%s\nlegacy:\n%s", table, got, want)
		}
	}
}

// Index migrations after the split must leave the API's tables alone: they
// no longer exist in an index the API has moved.
func TestIndexMigrationsLeavePrivateTablesAlone(t *testing.T) {
	names := regexp.MustCompile(`(?i)\b(` + strings.Join(privateTables, "|") + `)\b`)
	comment := regexp.MustCompile(`--[^\n]*`)
	err := fs.WalkDir(indexMigrations, "migrations", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Base(p) <= "0013_~" {
			return err
		}
		b, err := indexMigrations.ReadFile(p)
		if err != nil {
			return err
		}
		if m := names.FindString(comment.ReplaceAllString(string(b), "")); m != "" {
			t.Errorf("%s touches %s: put it in private_migrations", p, m)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPrivatePath(t *testing.T) {
	sep := string(filepath.Separator)
	for _, tc := range []struct{ in, want string }{
		{"/data/db/sentinela.db", "/data/db-private/sentinela.db"},
		{"/srv/nvr/index/x.db", "/srv/nvr/index-private/x.db"},
	} {
		in, want := filepath.FromSlash(tc.in), filepath.FromSlash(tc.want)
		got := PrivatePath(in)
		if got != want {
			t.Errorf("PrivatePath(%q) = %q, want %q", in, got, want)
		}
		if strings.HasPrefix(got, filepath.Dir(in)+sep) {
			t.Errorf("PrivatePath(%q) = %q is inside the index's folder", in, got)
		}
	}
}

// The recorder and the API may open a fresh install's index together.
func TestConcurrentOpenIndexAndOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db", "race.db")
	errs := make(chan error, 6)
	for i := 0; i < 6; i++ {
		go func() {
			var s *Store
			var err error
			if i%2 == 0 {
				s, err = OpenIndex(context.Background(), path)
			} else {
				s, err = Open(context.Background(), path)
			}
			if err == nil {
				s.Close()
			}
			errs <- err
		}()
	}
	for i := 0; i < 6; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent open: %v", err)
		}
	}
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got, _ := tablesIn(context.Background(), s.db); len(got) != 0 {
		t.Fatalf("API tables left in the index: %v", got)
	}
}

// Until the move is final the index keeps its accounts, so a release from
// before the split (an automatic rollback) still signs people in; what that
// release changes is copied again on the next start, while restarts in
// between keep what the API itself wrote.
func TestRollbackKeepsAccountsUntilFinal(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "db", "sentinela.db")
	userID, _ := legacyIndex(t, path)
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

	// The older release runs on the index alone and changes a password.
	ix, err := OpenIndex(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ix.DB().ExecContext(ctx, `UPDATE users SET pw_hash='changed-by-old-release' WHERE id=?`, userID); err != nil {
		t.Fatalf("the index lost its accounts before the move was final: %v", err)
	}
	ix.Close()

	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if u, err := s.UserByID(ctx, userID); err != nil || u.PWHash != "changed-by-old-release" {
		t.Fatalf("the older release's change was not copied: %+v, %v", u, err)
	}
	if _, err := s.CreateUser(ctx, "second", "hash", "viewer"); err != nil {
		t.Fatal(err)
	}
	s.Close()

	// A restart with an unchanged index keeps what the API wrote.
	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountUsers(ctx); n != 2 {
		t.Fatalf("%d users after a restart: the API's own change was reverted", n)
	}
	if err := s.FinalizeMove(ctx); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if n, _ := s.CountUsers(ctx); n != 2 {
		t.Fatalf("%d users after the move was final", n)
	}
	if got, _ := tablesIn(ctx, s.db); len(got) != 0 {
		t.Fatalf("API tables left in the index: %v", got)
	}
}
