package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Camera ids are the only identity a camera has: grants (users.cameras),
// recordings, events, snapshots and per-camera settings all name it. So an
// id is never given to a second camera. camera_ids keeps every id ever
// configured; one that is no longer configured is retired, and what was
// only about that camera (grants, settings) is dropped. Its recordings and
// events stay under the retired id until retention removes them.
//
// The dashboard, the wizard and scripts/add-camera.sh refuse a retired id.
// If one is configured again anyway (a hand edit of config.yaml), it is a
// new camera: what the old one left moves to an archive id
// ("<id>~<retired at>", which no configuration can use), so the new camera
// starts with none of the old one's grants, settings, recordings or events.

// ArchiveSep separates a revived camera id from when it was retired in the
// archive id of the old camera's data. Camera ids cannot contain it.
const ArchiveSep = "~"

// SyncCameraIDs records the configured camera ids and retires every known
// id that is no longer configured. Ids found in recordings, events or grants
// count as known (installs from before camera_ids). Retired ids leave every
// grant that names a configured camera too (a grant naming only retired ids
// is kept, so it does not turn into "every camera"), and the settings
// settingKeys names for them are deleted. A retired id that is configured
// again is revived as a new camera (see above). It returns every retired
// id, the ones retired by this call and the ones revived by it.
//
// Grants and settings live in the private database, ids, recordings and
// events in the shared index: the private changes are made first, then the
// index's. Both are idempotent, so a failure between them is completed by
// the next call (an id stays retired in the index until its revival there).
func (s *Store) SyncCameraIDs(ctx context.Context, configured []string, settingKeys func(id string) []string) (retired, removedNow, revived []string, err error) {
	if s.priv == nil {
		return nil, nil, nil, errors.New("camera ids: the private database is not open")
	}
	now := Now()
	// What the index knows: every id with its removal time ("" = active),
	// including ids only recordings or events name (before camera_ids).
	known := map[string]string{}
	rows, err := s.db.QueryContext(ctx, `SELECT id, coalesce(removed_at,'') FROM camera_ids
		UNION SELECT DISTINCT camera_id, '' FROM segments WHERE camera_id NOT IN (SELECT id FROM camera_ids)
		UNION SELECT DISTINCT camera_id, '' FROM events WHERE camera_id NOT IN (SELECT id FROM camera_ids)`)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("camera ids: %w", err)
	}
	for rows.Next() {
		var id, removedAt string
		if err := rows.Scan(&id, &removedAt); err != nil {
			rows.Close()
			return nil, nil, nil, err
		}
		known[id] = removedAt
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, nil, err
	}
	// Revived: configured again after it was retired. Its old data moves to
	// an archive id, which is retired like any other id no longer configured.
	archives := map[string]string{} // id -> archive id
	for _, id := range configured {
		if at := known[id]; at != "" {
			archives[id] = ArchiveID(id, at)
			revived = append(revived, id)
			known[archives[id]] = at
			known[id] = ""
		}
	}

	// Private database: grants and settings.
	ptx, err := s.priv.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, nil, err
	}
	defer ptx.Rollback()
	grants, err := userGrants(ctx, ptx)
	if err != nil {
		return nil, nil, nil, err
	}
	for i, g := range grants {
		// A grant still naming a revived id named only retired ids (the
		// others left it at retirement): it names the archive instead, so it
		// neither sees the new camera nor widens to every camera.
		cams := make([]string, 0, len(g.cameras))
		for _, c := range g.cameras {
			if a, ok := archives[c]; ok {
				c = a
			}
			if !slices.Contains(cams, c) {
				cams = append(cams, c)
			}
		}
		for _, c := range cams {
			if _, ok := known[c]; !ok {
				known[c] = "" // named by a grant only
			}
		}
		grants[i].cameras = cams
		if !slices.Equal(cams, g.cameras) {
			if _, err := ptx.ExecContext(ctx, `UPDATE users SET cameras=? WHERE id=?`, joinCameras(cams), g.id); err != nil {
				return nil, nil, nil, err
			}
		}
	}
	for _, id := range configured {
		if _, ok := known[id]; !ok {
			known[id] = ""
		}
	}
	for _, id := range sortedKeys(known) {
		if slices.Contains(configured, id) {
			continue
		}
		retired = append(retired, id)
		if known[id] == "" {
			removedNow = append(removedNow, id)
		}
	}
	for _, g := range grants {
		keep := slices.DeleteFunc(slices.Clone(g.cameras), func(id string) bool { return slices.Contains(retired, id) })
		if len(keep) == len(g.cameras) || len(keep) == 0 {
			continue // nothing retired, or only retired ids: never widen to all
		}
		if _, err := ptx.ExecContext(ctx, `UPDATE users SET cameras=? WHERE id=?`, joinCameras(keep), g.id); err != nil {
			return nil, nil, nil, err
		}
	}
	if settingKeys != nil {
		for _, id := range append(slices.Clone(retired), revived...) {
			for _, k := range settingKeys(id) {
				if _, err := ptx.ExecContext(ctx, `DELETE FROM settings WHERE key=?`, k); err != nil {
					return nil, nil, nil, err
				}
			}
		}
	}
	if err := ptx.Commit(); err != nil {
		return nil, nil, nil, err
	}

	// Shared index: ids, the revived cameras' old data, recorder status.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, nil, err
	}
	defer tx.Rollback()
	for _, id := range revived {
		if err := reviveCameraID(ctx, tx, id, archives[id], now); err != nil {
			return nil, nil, nil, fmt.Errorf("revive camera id %s: %w", id, err)
		}
	}
	for _, id := range sortedKeys(known) {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO camera_ids(id, added_at) VALUES(?,?)`, id, now); err != nil {
			return nil, nil, nil, err
		}
	}
	for _, id := range removedNow {
		if _, err := tx.ExecContext(ctx, `UPDATE camera_ids SET removed_at=? WHERE id=? AND removed_at IS NULL`, now, id); err != nil {
			return nil, nil, nil, err
		}
	}
	for _, id := range retired {
		if _, err := tx.ExecContext(ctx, `DELETE FROM recorder_status WHERE camera_id=?`, id); err != nil {
			return nil, nil, nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, nil, err
	}
	return retired, removedNow, revived, nil
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// ArchiveID is the id a revived camera's old data moves to.
func ArchiveID(id, removedAt string) string {
	stamp := removedAt
	if t, err := time.Parse(TimeFormat, removedAt); err == nil {
		stamp = t.UTC().Format("20060102T150405Z")
	}
	stamp = strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' {
			return r
		}
		return -1
	}, stamp)
	return id + ArchiveSep + stamp
}

// reviveCameraID makes a retired id that is configured again a new camera
// in the index: the recordings and events the old camera made before it was
// retired and its row move to the archive id (its grants and settings were
// handled in the private database). Recordings the new camera made already
// (the recorder may start first) started after the old one was retired,
// and stay.
func reviveCameraID(ctx context.Context, tx *sql.Tx, id, archive, now string) error {
	var removedAt sql.NullString
	switch err := tx.QueryRowContext(ctx, `SELECT removed_at FROM camera_ids WHERE id=?`, id).Scan(&removedAt); {
	case errors.Is(err, sql.ErrNoRows):
		return nil
	case err != nil:
		return err
	case !removedAt.Valid:
		return nil // revived already (a repeated call)
	}
	for _, q := range []string{
		`UPDATE segments SET camera_id=? WHERE camera_id=? AND start_utc<?`,
		`UPDATE events SET camera_id=? WHERE camera_id=? AND start_utc<?`,
	} {
		if _, err := tx.ExecContext(ctx, q, archive, id, removedAt.String); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO camera_ids(id, added_at, removed_at)
		SELECT ?, added_at, removed_at FROM camera_ids WHERE id=?`, archive, id); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE camera_ids SET added_at=?, removed_at=NULL WHERE id=?`, now, id)
	return err
}

// CameraIDKnown reports whether id was ever used: configured now or once,
// or named by recordings, events or a grant. scripts/add-camera.sh refuses
// such an id for a new camera.
func (s *Store) CameraIDKnown(ctx context.Context, id string) (bool, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM camera_ids WHERE id=?)
		+ (SELECT count(*) FROM (SELECT 1 FROM segments WHERE camera_id=? LIMIT 1))
		+ (SELECT count(*) FROM (SELECT 1 FROM events WHERE camera_id=? LIMIT 1))`, id, id, id).Scan(&n); err != nil {
		return false, err
	}
	if n > 0 {
		return true, nil
	}
	if s.priv == nil {
		return false, errors.New("camera ids: the private database is not open")
	}
	rows, err := s.priv.QueryContext(ctx, `SELECT cameras FROM users WHERE cameras<>''`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cams string
		if err := rows.Scan(&cams); err != nil {
			return false, err
		}
		if slices.Contains(splitCameras(cams), id) {
			return true, nil
		}
	}
	return false, rows.Err()
}

// RetiredCameraIDs returns the ids of cameras that were removed: never to be
// given to another camera.
func (s *Store) RetiredCameraIDs(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM camera_ids WHERE removed_at IS NOT NULL ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

type userGrant struct {
	id      int64
	cameras []string
}

// userGrants returns the users limited to some cameras.
func userGrants(ctx context.Context, tx *sql.Tx) ([]userGrant, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, cameras FROM users WHERE cameras<>''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []userGrant
	for rows.Next() {
		var g userGrant
		var cams string
		if err := rows.Scan(&g.id, &cams); err != nil {
			return nil, err
		}
		for _, id := range splitCameras(cams) {
			if id = strings.TrimSpace(id); id != "" {
				g.cameras = append(g.cameras, id)
			}
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
