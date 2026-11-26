package recording

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/LUKER2DW/sentinela/internal/config"
	"github.com/LUKER2DW/sentinela/internal/review"
	"github.com/LUKER2DW/sentinela/internal/store"
	"github.com/LUKER2DW/sentinela/internal/sysinfo"
)

// Retention deletes old segments: first by per-camera age (segments that
// overlap an Alert event are kept for AlertRetentionDays instead), then
// oldest-first while the disk is above MaxDiskPercent, alert or not.
// A retention of 0 days means no age limit: the disk fills up to
// MaxDiskPercent and the oldest recordings are overwritten from then on.
// Protected segments are kept from both, with one exception: when the disk
// is above the limit and nothing unprotected is left, the oldest protected
// ones go too, so recording never stops on a full disk. (Protection is
// capped per camera and in total, so this only happens after the caps were
// lowered or something else filled the disk.) A removed camera's protected
// segments age out too: nobody can unprotect them any more.
type Retention struct {
	Root  string
	Store *store.Store
	Cfg   *config.Config
	Log   *slog.Logger
	Disk  func(path string) (sysinfo.Disk, error)
	now   func() time.Time
}

// NewRetention returns a retention worker.
func NewRetention(root string, st *store.Store, cfg *config.Config, log *slog.Logger) *Retention {
	return &Retention{Root: root, Store: st, Cfg: cfg, Log: log, Disk: sysinfo.DiskUsage, now: time.Now}
}

// Run enforces retention every interval.
func (r *Retention) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if n, err := r.Enforce(ctx); err != nil && ctx.Err() == nil {
			r.Log.Warn("retention failed", "err", err)
		} else if n > 0 {
			r.Log.Info("retention deleted segments", "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Enforce runs one pass and returns how many segments were deleted.
func (r *Retention) Enforce(ctx context.Context) (int, error) {
	deleted := 0
	alertDays := r.Cfg.Recording.AlertRetentionDays
	// unlimited: some tier has no age limit, so events are kept as long as
	// recordings exist instead of for a fixed number of days.
	unlimited := alertDays <= 0
	maxDays := alertDays
	// Zero time: alert segments are never deleted by age.
	var alertCutoff time.Time
	if alertDays > 0 {
		alertCutoff = r.now().Add(-time.Duration(alertDays) * 24 * time.Hour)
	}
	alertTypes := review.AlertTypes()
	ageOut := func(cameraID string, days int) error {
		cutoff := r.now().Add(-time.Duration(days) * 24 * time.Hour)
		for {
			segs, err := r.Store.SegmentsForAgeRetention(ctx, cameraID, cutoff, alertCutoff, alertTypes, 200)
			if err != nil {
				return err
			}
			if len(segs) == 0 {
				return nil
			}
			for _, s := range segs {
				if err := r.remove(ctx, s); err != nil {
					return err
				}
				deleted++
			}
		}
	}
	for i := range r.Cfg.Cameras {
		cam := &r.Cfg.Cameras[i]
		if cam.RetentionDays <= 0 {
			unlimited = true
			continue
		}
		maxDays = max(maxDays, cam.RetentionDays)
		if err := ageOut(cam.ID, cam.RetentionDays); err != nil {
			return deleted, err
		}
	}
	// A removed camera's recordings stay under its id (never given to
	// another camera) until default_retention_days, or disk pressure when
	// that is 0. Nobody can unprotect its protected clips any more, so they
	// go as well, after the longer of alert_retention_days and
	// default_retention_days (kept like its alert clips when that is 0).
	if days := r.Cfg.Recording.DefaultRetentionDays; days > 0 {
		ids, err := r.Store.SegmentCameraIDs(ctx)
		if err != nil {
			return deleted, err
		}
		for _, id := range ids {
			if r.Cfg.Camera(id) != nil {
				continue
			}
			maxDays = max(maxDays, days)
			if err := ageOut(id, days); err != nil {
				return deleted, err
			}
			if alertDays <= 0 {
				continue
			}
			n, err := r.ageOutProtected(ctx, id, max(days, alertDays))
			deleted += n
			if err != nil {
				return deleted, err
			}
		}
	}
	// Disk pressure: delete oldest until 2 points below the limit.
	limit := float64(r.Cfg.Recording.MaxDiskPercent)
	valve, valveCams := 0, map[string]bool{}
	defer func() {
		if valve > 0 {
			r.auditValve(ctx, valve, valveCams)
		}
	}()
	for guard := 0; guard < 10000; guard++ {
		d, err := r.Disk(r.Root)
		if err != nil {
			return deleted, err
		}
		// Start only above the limit; once started, go on to 1 point below
		// it, so the disk does not sit at the edge deleting every pass.
		if d.Total == 0 || (guard == 0 && d.Pct < limit) || (guard > 0 && d.Pct < limit-1) {
			break
		}
		segs, err := r.Store.OldestSegments(ctx, 20)
		if err != nil {
			return deleted, err
		}
		if len(segs) == 0 {
			if segs, err = r.Store.OldestProtectedSegments(ctx, 20); err != nil {
				return deleted, err
			}
			if len(segs) == 0 {
				r.Log.Error("disk above limit but no recordings left to delete", "pct", d.Pct)
				break
			}
			r.Log.Error("disk above limit with only protected recordings left: deleting the oldest protected ones so recording goes on", "pct", d.Pct, "count", len(segs))
		}
		for _, s := range segs {
			if err := r.remove(ctx, s); err != nil {
				return deleted, err
			}
			deleted++
			if s.Protected {
				valve++
				valveCams[s.CameraID] = true
			}
		}
	}
	// Events (and their review state) live as long as the longest tier;
	// without an age limit, as long as the recordings they point into (a
	// day of slack keeps an event that started just before the oldest
	// segment).
	switch {
	case !unlimited && maxDays > 0:
		_ = r.Store.PurgeEventsBefore(ctx, r.now().Add(-time.Duration(maxDays)*24*time.Hour))
	case unlimited:
		if oldest, err := r.Store.OldestSegments(ctx, 1); err == nil && len(oldest) == 1 {
			_ = r.Store.PurgeEventsBefore(ctx, oldest[0].Start.Add(-24*time.Hour))
		}
	}
	return deleted, nil
}

// auditValve records that the full-disk valve deleted protected clips: the
// API turns the row into a notice to the admins (the recorder never calls
// the API). A failed write is only logged; recording goes on.
func (r *Retention) auditValve(ctx context.Context, n int, cams map[string]bool) {
	ids := make([]string, 0, len(cams))
	for id := range cams {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	detail := fmt.Sprintf("%d protected clips deleted: the recordings disk was above %d%% with nothing unprotected left", n, r.Cfg.Recording.MaxDiskPercent)
	if err := r.Store.Audit(context.WithoutCancel(ctx), 0, "", store.AuditProtectValve, strings.Join(ids, ","), true, detail); err != nil {
		r.Log.Warn("audit of deleted protected recordings failed", "err", err)
	}
}

// ageOutProtected deletes a removed camera's protected segments that ended
// more than days ago.
func (r *Retention) ageOutProtected(ctx context.Context, cameraID string, days int) (int, error) {
	cutoff := r.now().Add(-time.Duration(days) * 24 * time.Hour)
	n := 0
	for {
		segs, err := r.Store.ProtectedSegmentsOlderThan(ctx, cameraID, cutoff, 200)
		if err != nil || len(segs) == 0 {
			if n > 0 {
				r.Log.Info("retention: removed camera's protected recordings aged out", "camera", cameraID, "count", n)
			}
			return n, err
		}
		for _, s := range segs {
			if err := r.remove(ctx, s); err != nil {
				return n, err
			}
			n++
		}
	}
}

func (r *Retention) remove(ctx context.Context, s store.Segment) error {
	abs := filepath.Join(r.Root, filepath.FromSlash(s.Path))
	if err := os.Remove(abs); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// Remove now-empty day/month/year directories (best effort).
	dir := filepath.Dir(abs)
	for i := 0; i < 3; i++ {
		if os.Remove(dir) != nil {
			break
		}
		dir = filepath.Dir(dir)
	}
	return r.Store.DeleteSegment(ctx, s.ID)
}
