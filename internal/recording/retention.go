package recording

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/LUKER2DW/sentinela/internal/config"
	"github.com/LUKER2DW/sentinela/internal/review"
	"github.com/LUKER2DW/sentinela/internal/store"
	"github.com/LUKER2DW/sentinela/internal/sysinfo"
)

// Retention deletes old segments: first by per-camera age (segments that
// overlap an Alert event are kept for AlertRetentionDays instead), then
// oldest-first while the disk is above MaxDiskPercent, alert or not.
// Protected segments are never deleted.
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
	maxDays := alertDays
	alertCutoff := r.now().Add(-time.Duration(alertDays) * 24 * time.Hour)
	alertTypes := review.AlertTypes()
	for i := range r.Cfg.Cameras {
		cam := &r.Cfg.Cameras[i]
		maxDays = max(maxDays, cam.RetentionDays)
		cutoff := r.now().Add(-time.Duration(cam.RetentionDays) * 24 * time.Hour)
		for {
			segs, err := r.Store.SegmentsForAgeRetention(ctx, cam.ID, cutoff, alertCutoff, alertTypes, 200)
			if err != nil {
				return deleted, err
			}
			if len(segs) == 0 {
				break
			}
			for _, s := range segs {
				if err := r.remove(ctx, s); err != nil {
					return deleted, err
				}
				deleted++
			}
		}
	}
	// Disk pressure: delete oldest until 2 points below the limit.
	limit := float64(r.Cfg.Recording.MaxDiskPercent)
	for guard := 0; guard < 10000; guard++ {
		d, err := r.Disk(r.Root)
		if err != nil {
			return deleted, err
		}
		// Start only above the limit; once started, go on to 2 points below
		// it, so the disk does not sit at the edge deleting every pass.
		if d.Total == 0 || (guard == 0 && d.Pct < limit) || (guard > 0 && d.Pct < limit-2) {
			break
		}
		segs, err := r.Store.OldestSegments(ctx, 20)
		if err != nil {
			return deleted, err
		}
		if len(segs) == 0 {
			r.Log.Error("disk above limit but no deletable segments", "pct", d.Pct)
			break
		}
		for _, s := range segs {
			if err := r.remove(ctx, s); err != nil {
				return deleted, err
			}
			deleted++
		}
	}
	// Events (and their review state) live as long as the longest tier.
	if maxDays > 0 {
		_ = r.Store.PurgeEventsBefore(ctx, r.now().Add(-time.Duration(maxDays)*24*time.Hour))
	}
	return deleted, nil
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
