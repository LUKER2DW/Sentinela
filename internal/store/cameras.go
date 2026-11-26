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
// count as known (installs from before camera_ids). In the same
// transaction, retired ids leave every grant that names a configured camera
// too (a grant naming only retired ids is kept, so it does not turn into
// "every camera"), and the settings settingKeys names for them are deleted.
// A retired id that is configured again is revived as a new camera (see
// above). It returns every retired id, the ones retired by this call and
// the ones revived by it.
func (s *Store) SyncCameraIDs(ctx context.Context, configured []string, settingKeys func(id string) []string) (retired, removedNow, revived []string, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, nil, err
	}
	defer tx.Rollback()
	now := Now()
	// Known before this table existed: whatever left data behind.
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO camera_ids(id, added_at)
		SELECT DISTINCT camera_id, ? FROM segments UNION SELECT DISTINCT camera_id, ? FROM events`, now, now); err != nil {
		return nil, nil, nil, fmt.Errorf("camera ids: %w", err)
	}
	for _, id := range configured {
		var removedAt sql.NullString
		switch err := tx.QueryRowContext(ctx, `SELECT removed_at FROM camera_ids WHERE id=?`, id).Scan(&removedAt); {
		case errors.Is(err, sql.ErrNoRows), err == nil && !removedAt.Valid:
			continue
		case err != nil:
			return nil, nil, nil, err
		}
		if err := reviveCameraID(ctx, tx, id, removedAt.String, now, settingKeys); err != nil {
			return nil, nil, nil, fmt.Errorf("revive camera id %s: %w", id, err)
		}
		revived = append(revived, id)
	}
	grants, err := userGrants(ctx, tx)
	if err != nil {
		return nil, nil, nil, err
	}
	for _, g := range grants {
		for _, id := range g.cameras {
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO camera_ids(id, added_at) VALUES(?,?)`, id, now); err != nil {
				return nil, nil, nil, err
			}
		}
	}
	for _, id := range configured {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO camera_ids(id, added_at) VALUES(?,?)`, id, now); err != nil {
			return nil, nil, nil, err
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT id, removed_at IS NULL FROM camera_ids ORDER BY id`)
	if err != nil {
		return nil, nil, nil, err
	}
	for rows.Next() {
		var id string
		var active bool
		if err := rows.Scan(&id, &active); err != nil {
			rows.Close()
			return nil, nil, nil, err
		}
		if slices.Contains(configured, id) {
			continue
		}
		retired = append(retired, id)
		if active {
			removedNow = append(removedNow, id)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, nil, err
	}
	for _, id := range removedNow {
		if _, err := tx.ExecContext(ctx, `UPDATE camera_ids SET removed_at=? WHERE id=?`, now, id); err != nil {
			return nil, nil, nil, err
		}
	}
	for _, g := range grants {
		keep := slices.DeleteFunc(slices.Clone(g.cameras), func(id string) bool { return slices.Contains(retired, id) })
		if len(keep) == len(g.cameras) || len(keep) == 0 {
			continue // nothing retired, or only retired ids: never widen to all
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET cameras=? WHERE id=?`, joinCameras(keep), g.id); err != nil {
			return nil, nil, nil, err
		}
	}
	for _, id := range retired {
		if _, err := tx.ExecContext(ctx, `DELETE FROM recorder_status WHERE camera_id=?`, id); err != nil {
			return nil, nil, nil, err
		}
		if settingKeys == nil {
			continue
		}
		for _, k := range settingKeys(id) {
			if _, err := tx.ExecContext(ctx, `DELETE FROM settings WHERE key=?`, k); err != nil {
				return nil, nil, nil, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, nil, err
	}
	return retired, removedNow, revived, nil
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

// reviveCameraID makes a retired id that is configured again a new camera:
// the recordings and events the old camera made before it was retired, its
// grants and its row move to an archive id, and its settings go. Recordings
// the new camera made already (the recorder may start first) started after
// the old one was retired, and stay.
func reviveCameraID(ctx context.Context, tx *sql.Tx, id, removedAt, now string, settingKeys func(id string) []string) error {
	archive := ArchiveID(id, removedAt)
	for _, q := range []string{
		`UPDATE segments SET camera_id=? WHERE camera_id=? AND start_utc<?`,
		`UPDATE events SET camera_id=? WHERE camera_id=? AND start_utc<?`,
	} {
		if _, err := tx.ExecContext(ctx, q, archive, id, removedAt); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO camera_ids(id, added_at, removed_at)
		SELECT ?, added_at, removed_at FROM camera_ids WHERE id=?`, archive, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE camera_ids SET added_at=?, removed_at=NULL WHERE id=?`, now, id); err != nil {
		return err
	}
	// A grant still naming the id named only retired ids (the others left
	// it at retirement): it names the archive instead, so it neither sees
	// the new camera nor widens to every camera.
	grants, err := userGrants(ctx, tx)
	if err != nil {
		return err
	}
	for _, g := range grants {
		if !slices.Contains(g.cameras, id) {
			continue
		}
		cams := make([]string, 0, len(g.cameras))
		for _, c := range g.cameras {
			if c == id {
				c = archive
			}
			if !slices.Contains(cams, c) {
				cams = append(cams, c)
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET cameras=? WHERE id=?`, joinCameras(cams), g.id); err != nil {
			return err
		}
	}
	if settingKeys != nil {
		for _, k := range settingKeys(id) {
			if _, err := tx.ExecContext(ctx, `DELETE FROM settings WHERE key=?`, k); err != nil {
				return err
			}
		}
	}
	return nil
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
	rows, err := s.db.QueryContext(ctx, `SELECT cameras FROM users WHERE cameras<>''`)
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
