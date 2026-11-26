// Package store owns the SQLite schema, migrations and queries.
// All timestamps are stored as UTC RFC 3339 strings with millisecond precision.
package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver
)

// The store is two SQLite files. The shared index (segments, events,
// recorder status) is written by both `sentinela record` and the API. The
// private database (accounts, sessions, two-step secrets, push
// subscriptions, settings, audit log) is opened by the API only and lives
// in a folder the recorder container does not mount: the recorder parses
// untrusted camera data and must not be able to read a password hash or
// mint a session. Releases before the split kept everything in the index;
// the API moves those tables once (moveLegacy).
//
// Index migrations must never touch a private table again: on a host that
// has moved, those tables no longer exist in the index
// (TestIndexMigrationsLeavePrivateTablesAlone).

//go:embed migrations/*.sql
var indexMigrations embed.FS

//go:embed private_migrations/*.sql
var privateMigrations embed.FS

// TimeFormat is the storage format for timestamps (sorts lexically).
const TimeFormat = "2006-01-02T15:04:05.000Z"

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("not found")

// privateTables are the API's tables, in the order they are dropped from the
// index (a table before the ones it references); they are copied in reverse.
var privateTables = []string{"fcm_tokens", "push_subscriptions", "review_seen", "sessions", "users", "audit", "settings"}

// movedMarker is the private schema_migrations row recording that the
// index's API tables were copied at least once. finalMarker records that the
// move is final (FinalizeMove): from then on nothing is ever copied from the
// index again, and tables that reappear there are dropped unread. Until
// then the index keeps its copy, so a release from before the split (an
// automatic rollback after a failed health check) still finds its accounts;
// fingerprintPrefix rows remember what was copied, so that the index is
// copied again only when that older release changed it meanwhile.
const (
	movedMarker       = "moved_from_index"
	finalMarker       = "moved_from_index_final"
	fingerprintPrefix = "moved_from_index_fp:"
)

// Store wraps the database handles. priv is nil for a store opened with
// OpenIndex, whose methods for accounts, sessions, push, settings and audit
// must not be called.
type Store struct {
	db        *sql.DB // shared index
	priv      *sql.DB // private database, API only
	indexPath string
}

// PrivatePath is where the API keeps the private database for an index at
// indexPath: the same file name in a sibling folder named after the index's
// folder plus "-private" (/data/db/sentinela.db -> /data/db-private/sentinela.db).
// The recorder container mounts only the index's folder.
func PrivatePath(indexPath string) string {
	dir := filepath.Clean(filepath.Dir(indexPath))
	return filepath.Join(dir+"-private", filepath.Base(indexPath))
}

// Open opens the API's store: the shared index at path and the private
// database at PrivatePath(path), moving the API's tables out of an index
// written by a release before the split.
func Open(ctx context.Context, path string) (*Store, error) {
	idx, err := openDB(ctx, path, 0o750, indexMigrations, "migrations")
	if err != nil {
		return nil, err
	}
	priv, err := openDB(ctx, PrivatePath(path), 0o700, privateMigrations, "private_migrations")
	if err != nil {
		idx.Close()
		return nil, fmt.Errorf("private database: %w", err)
	}
	// A folder that already existed keeps its mode with MkdirAll.
	_ = os.Chmod(filepath.Dir(PrivatePath(path)), 0o700)
	// The recorder can replace the index file with a link to the private one;
	// the API must not then use the private database as the index.
	a, errA := os.Stat(path)
	b, errB := os.Stat(PrivatePath(path))
	if errA == nil && errB == nil && os.SameFile(a, b) {
		idx.Close()
		priv.Close()
		return nil, errors.New("the index and the private database are the same file")
	}
	s := &Store{db: idx, priv: priv, indexPath: path}
	if err := s.moveLegacy(ctx); err != nil {
		s.Close()
		return nil, fmt.Errorf("move the API's tables out of the shared index: %w", err)
	}
	return s, nil
}

// OpenIndex opens only the shared index (the recorder). It never removes
// anything: an index from before the split keeps the API's tables until the
// API has copied them.
func OpenIndex(ctx context.Context, path string) (*Store, error) {
	idx, err := openDB(ctx, path, 0o750, indexMigrations, "migrations")
	if err != nil {
		return nil, err
	}
	return &Store{db: idx, indexPath: path}, nil
}

// openDB opens (creating if needed) one database file and runs its migrations.
func openDB(ctx context.Context, path string, dirMode os.FileMode, fsys embed.FS, dir string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), dirMode); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}
	dsn := "file:" + filepath.ToSlash(path) +
		"?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)&_txlock=immediate"
	// The recorder and API processes may open a fresh DB at the same moment
	// (ADR-0007). Switching to WAL and the first schema write can fail with
	// SQLITE_BUSY without consulting busy_timeout, so retry with jitter.
	var lastErr error
	for attempt := 0; attempt < 40; attempt++ {
		db, err := sql.Open("sqlite", dsn)
		if err != nil {
			return nil, fmt.Errorf("open db: %w", err)
		}
		db.SetMaxOpenConns(4)
		if err := migrate(ctx, db, fsys, dir); err != nil {
			db.Close()
			if !isBusy(err) {
				return nil, err
			}
			lastErr = err
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(50+rand.IntN(200)) * time.Millisecond):
			}
			continue
		}
		return db, nil
	}
	return nil, fmt.Errorf("database stayed locked: %w", lastErr)
}

func isBusy(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "SQLITE_BUSY") || strings.Contains(msg, "database is locked")
}

// Close closes the database files.
func (s *Store) Close() error {
	err := s.db.Close()
	if s.priv != nil {
		err = errors.Join(err, s.priv.Close())
	}
	return err
}

// DB exposes the shared index's handle.
func (s *Store) DB() *sql.DB { return s.db }

// PrivateDB exposes the private database handle (nil for OpenIndex), for
// tests that inspect it.
func (s *Store) PrivateDB() *sql.DB { return s.priv }

// Ping checks that every open database file answers.
func (s *Store) Ping(ctx context.Context) error {
	if err := s.db.PingContext(ctx); err != nil {
		return err
	}
	if s.priv != nil {
		return s.priv.PingContext(ctx)
	}
	return nil
}

func migrate(ctx context.Context, db *sql.DB, fsys embed.FS, dir string) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations(version TEXT PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		return fmt.Errorf("migrations table: %w", err)
	}
	entries, err := fsys.ReadDir(dir)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		version := strings.TrimSuffix(name, ".sql")
		body, err := fsys.ReadFile(dir + "/" + name)
		if err != nil {
			return err
		}
		// _txlock=immediate: the check and the apply happen under one write
		// lock, so two processes can never both apply the same migration.
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations WHERE version=?`, version).Scan(&n); err != nil {
			tx.Rollback()
			return err
		}
		if n > 0 {
			tx.Rollback()
			continue
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations VALUES(?,?)`, version, Now()); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// moveLegacy moves the API's tables out of the shared index in two steps.
// Open copies them into the private database (one transaction there, synced
// to disk) and leaves the index's copy in place; FinalizeMove, which the API
// calls once it has been up past the updater's health check, records that
// the move is final and drops them from the index with their freed pages
// zeroed. A crash between the two steps only repeats the drop. An index
// with no account (a fresh install: its migrations still create these
// tables, empty) has nothing to roll back to and is final at once; once
// final, tables that reappear in the index are dropped unread.
func (s *Store) moveLegacy(ctx context.Context) error {
	present, err := tablesIn(ctx, s.db)
	if err != nil {
		return err
	}
	if len(present) == 0 {
		return nil
	}
	final, err := s.privMarker(ctx, finalMarker)
	if err != nil {
		return err
	}
	if final {
		return s.dropLegacy(ctx, present)
	}
	users, err := s.copyLegacy(ctx, present)
	if err != nil {
		return err
	}
	if users < 0 {
		return s.dropLegacy(ctx, present)
	}
	if users == 0 {
		return s.FinalizeMove(ctx)
	}
	return nil
}

// FinalizeMove makes the move final: the marker is synced to disk before
// the index's copy of the API's tables is dropped. The API calls it after it
// has been up for a while (an automatic rollback to a release from before
// the split can no longer happen); it is a no-op on a store without the
// private database, and harmless to repeat.
func (s *Store) FinalizeMove(ctx context.Context) error {
	if s.priv == nil {
		return nil
	}
	conn, err := s.priv.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `PRAGMA synchronous=FULL`); err != nil {
		return err
	}
	defer conn.ExecContext(context.WithoutCancel(ctx), `PRAGMA synchronous=NORMAL`) //nolint:errcheck
	if _, err := conn.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations VALUES(?,?)`, finalMarker, Now()); err != nil {
		return err
	}
	present, err := tablesIn(ctx, s.db)
	if err != nil || len(present) == 0 {
		return err
	}
	return s.dropLegacy(ctx, present)
}

// privMarker reports whether the private schema_migrations has version.
func (s *Store) privMarker(ctx context.Context, version string) (bool, error) {
	var n int
	err := s.priv.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations WHERE version=?`, version).Scan(&n)
	return n > 0, err
}

// tablesIn lists the private tables that exist in db's main schema.
func tablesIn(ctx context.Context, db *sql.DB) ([]string, error) {
	var out []string
	for _, t := range privateTables {
		var n int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, t).Scan(&n); err != nil {
			return nil, err
		}
		if n > 0 {
			out = append(out, t)
		}
	}
	return out, nil
}

// copyLegacy copies the present tables (the columns both sides have) from
// the index into the private database, replacing what is there, unless the
// index has not changed since the last copy (its fingerprint): restarts and
// the user command during the window before FinalizeMove keep what the API
// wrote since, and a rollback's changes are carried over on the next start.
// It returns how many accounts the index holds, or -1 when the move became
// final meanwhile (nothing to copy).
func (s *Store) copyLegacy(ctx context.Context, present []string) (int, error) {
	conn, err := s.priv.Conn(ctx)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `PRAGMA synchronous=FULL`); err != nil {
		return 0, err
	}
	defer conn.ExecContext(context.WithoutCancel(ctx), `PRAGMA synchronous=NORMAL`) //nolint:errcheck
	// The index enforced its own foreign keys; a stray orphan row there must
	// not keep the API from starting.
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
		return 0, err
	}
	defer conn.ExecContext(context.WithoutCancel(ctx), `PRAGMA foreign_keys=ON`) //nolint:errcheck
	if _, err := conn.ExecContext(ctx, `ATTACH DATABASE ? AS legacy`, s.indexPath); err != nil {
		return 0, fmt.Errorf("attach index: %w", err)
	}
	defer conn.ExecContext(context.WithoutCancel(ctx), `DETACH DATABASE legacy`) //nolint:errcheck
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	// Another process may have made the move final meanwhile (and is about
	// to drop the index's tables): this transaction holds the private
	// database's write lock, so that one waits for it or came before.
	var final int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM main.schema_migrations WHERE version=?`, finalMarker).Scan(&final); err != nil {
		return 0, err
	}
	if final > 0 {
		return -1, nil
	}
	users := 0
	if slices.Contains(present, "users") {
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM legacy.users`).Scan(&users); err != nil {
			return 0, err
		}
	}
	fp, err := legacyFingerprint(ctx, tx, present)
	if err != nil {
		return 0, err
	}
	var same int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM main.schema_migrations WHERE version=?`, fingerprintPrefix+fp).Scan(&same); err != nil {
		return 0, err
	}
	if same > 0 {
		return users, nil // copied already, and the index has not changed since
	}
	for _, t := range privateTables { // replace: what an older copy left goes first
		if _, err := tx.ExecContext(ctx, `DELETE FROM main.`+t); err != nil {
			return 0, fmt.Errorf("clear %s: %w", t, err)
		}
	}
	for i := len(privateTables) - 1; i >= 0; i-- {
		t := privateTables[i]
		if !slices.Contains(present, t) {
			continue
		}
		cols, err := sharedColumns(ctx, tx, t)
		if err != nil {
			return 0, err
		}
		list := strings.Join(cols, ",")
		if _, err := tx.ExecContext(ctx, `INSERT INTO main.`+t+`(`+list+`) SELECT `+list+` FROM legacy.`+t); err != nil {
			return 0, fmt.Errorf("copy %s: %w", t, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM main.schema_migrations WHERE version LIKE ?`, fingerprintPrefix+"%"); err != nil {
		return 0, err
	}
	for _, v := range []string{movedMarker, fingerprintPrefix + fp} {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO main.schema_migrations VALUES(?,?)`, v, Now()); err != nil {
			return 0, err
		}
	}
	return users, tx.Commit()
}

// legacyFingerprint hashes the index's copy of the API's tables (the audit
// log by its size and last row: it only grows), to tell whether a release
// from before the split changed them since they were copied.
func legacyFingerprint(ctx context.Context, tx *sql.Tx, present []string) (string, error) {
	h := sha256.New()
	for _, t := range present {
		q := `SELECT * FROM legacy.` + t
		if t == "audit" {
			q = `SELECT count(*), max(rowid) FROM legacy.audit`
		}
		rows, err := tx.QueryContext(ctx, q)
		if err != nil {
			return "", err
		}
		cols, err := rows.Columns()
		if err != nil {
			rows.Close()
			return "", err
		}
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		fmt.Fprintf(h, "%s\x00", t)
		for rows.Next() {
			if err := rows.Scan(ptrs...); err != nil {
				rows.Close()
				return "", err
			}
			for _, v := range vals {
				fmt.Fprintf(h, "%T:%v\x1f", v, v)
			}
			h.Write([]byte{0x1e})
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// sharedColumns returns the columns table t has in both main and legacy, in
// main's order, as quoted identifiers.
func sharedColumns(ctx context.Context, tx *sql.Tx, t string) ([]string, error) {
	names := func(schema string) ([]string, error) {
		rows, err := tx.QueryContext(ctx, `SELECT name FROM pragma_table_info(?, ?)`, t, schema)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var n string
			if err := rows.Scan(&n); err != nil {
				return nil, err
			}
			out = append(out, n)
		}
		return out, rows.Err()
	}
	mainCols, err := names("main")
	if err != nil {
		return nil, err
	}
	legacyCols, err := names("legacy")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, c := range mainCols {
		if slices.Contains(legacyCols, c) {
			out = append(out, `"`+strings.ReplaceAll(c, `"`, `""`)+`"`)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("copy %s: no column in common", t)
	}
	return out, nil
}

// dropLegacy drops the present tables from the index with secure_delete on,
// so their freed pages (password hashes, two-step secrets, session hashes)
// are zeroed, then checkpoints the WAL so the zeroed pages reach the main
// file and the WAL is emptied.
func (s *Store) dropLegacy(ctx context.Context, present []string) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `PRAGMA secure_delete=ON`); err != nil {
		return err
	}
	defer conn.ExecContext(context.WithoutCancel(ctx), `PRAGMA secure_delete=OFF`) //nolint:errcheck
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, t := range privateTables {
		if !slices.Contains(present, t) {
			continue
		}
		if _, err := tx.ExecContext(ctx, `DROP TABLE IF EXISTS `+t); err != nil {
			return fmt.Errorf("drop %s: %w", t, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	// Best effort: a recorder reading an older snapshot can hold the
	// checkpoint back; SQLite's automatic checkpoints finish it later.
	_, _ = conn.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
	return nil
}

// Now returns the current UTC time formatted for storage.
func Now() string { return FormatTime(time.Now()) }

// FormatTime formats t for storage.
func FormatTime(t time.Time) string { return t.UTC().Format(TimeFormat) }

// ParseTime parses a stored timestamp.
func ParseTime(s string) (time.Time, error) { return time.Parse(TimeFormat, s) }

func mustTime(s string) time.Time {
	t, _ := ParseTime(s)
	return t
}

// Backup writes a consistent copy of the shared index to dst (VACUUM INTO).
func (s *Store) Backup(ctx context.Context, dst string) error {
	return vacuumInto(ctx, s.db, dst)
}

// BackupPrivate writes a consistent copy of the private database to dst.
func (s *Store) BackupPrivate(ctx context.Context, dst string) error {
	return vacuumInto(ctx, s.priv, dst)
}

func vacuumInto(ctx context.Context, db *sql.DB, dst string) error {
	_ = os.Remove(dst)
	_, err := db.ExecContext(ctx, `VACUUM INTO ?`, dst)
	return err
}
