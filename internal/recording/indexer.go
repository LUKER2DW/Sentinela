package recording

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/LUKER2DW/sentinela/internal/store"
)

// ParseSegmentPath returns the UTC start time encoded in a path relative to
// the recordings root ("cam1/2026/09/28/13-05-00.mp4") and the camera id.
func ParseSegmentPath(rel string) (camera string, start time.Time, ok bool) {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) != 5 || !strings.HasSuffix(parts[4], ".mp4") {
		return "", time.Time{}, false
	}
	// Parsed by hand: in Go layouts "-04"/"-05" can be read as zone offsets.
	var y, mo, d, h, mi, s int
	name := strings.Join(parts[1:], "/")
	if n, err := fmt.Sscanf(name, "%4d/%2d/%2d/%2d-%2d-%2d.mp4", &y, &mo, &d, &h, &mi, &s); err != nil || n != 6 {
		return "", time.Time{}, false
	}
	t := time.Date(y, time.Month(mo), d, h, mi, s, 0, time.UTC)
	if t.Format("2006/01/02/15")+fmt.Sprintf("-%02d-%02d.mp4", mi, s) != name {
		return "", time.Time{}, false // out-of-range fields normalized by time.Date
	}
	return parts[0], t, true
}

// Indexer keeps the segments table in sync with the files on disk.
type Indexer struct {
	Root    string // recordings root
	Store   *store.Store
	Log     *slog.Logger
	FFprobe string
	// Probe returns a file's duration; defaults to ffprobe.
	Probe func(ctx context.Context, path string) (time.Duration, error)
	// FullEvery is how often Run walks the whole tree; the other ticks walk
	// only the folders the recorder is writing to (see ScanRecent).
	FullEvery time.Duration
}

// NewIndexer returns an indexer using ffprobe.
func NewIndexer(root string, st *store.Store, log *slog.Logger) *Indexer {
	ix := &Indexer{Root: root, Store: st, Log: log, FFprobe: "ffprobe", FullEvery: time.Hour}
	ix.Probe = ix.ffprobe
	return ix
}

func (ix *Indexer) ffprobe(ctx context.Context, path string) (time.Duration, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, ix.FFprobe, "-v", "error", "-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1", path).Output()
	if err != nil {
		return 0, err
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		return 0, err
	}
	return time.Duration(f * float64(time.Second)), nil
}

// Run indexes every interval until ctx is cancelled. The first pass and one
// pass every FullEvery reconcile the whole tree (manual deletions, disk
// swaps); the others walk only today's and yesterday's folders, so a tick
// costs the same with 3 or 60 days of recordings on disk.
func (ix *Indexer) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	var lastFull time.Time
	for {
		var err error
		if now := time.Now(); lastFull.IsZero() || now.Sub(lastFull) >= ix.FullEvery {
			if err = ix.Scan(ctx); err == nil {
				lastFull = now
			}
		} else {
			err = ix.ScanRecent(ctx, now)
		}
		if err != nil && ctx.Err() == nil {
			ix.Log.Warn("index scan failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

type fileInfo struct {
	rel   string
	abs   string
	size  int64
	mtime time.Time
	start time.Time
}

// Scan walks all camera directories once.
func (ix *Indexer) Scan(ctx context.Context) error { return ix.scan(ctx, time.Time{}) }

// ScanRecent walks only the day folders (UTC) of now and of the day before,
// where the live segment and the ones just closed are. Rows of older days
// are left alone until the next full Scan.
func (ix *Indexer) ScanRecent(ctx context.Context, now time.Time) error {
	d := now.UTC()
	return ix.scan(ctx, time.Date(d.Year(), d.Month(), d.Day()-1, 0, 0, 0, 0, time.UTC))
}

// scan indexes segments starting at or after since (zero: all of them).
func (ix *Indexer) scan(ctx context.Context, since time.Time) error {
	cams, err := os.ReadDir(ix.Root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, c := range cams {
		if c.IsDir() {
			if err := ix.scanCamera(ctx, c.Name(), since); err != nil {
				return err
			}
		}
	}
	return nil
}

func (ix *Indexer) scanCamera(ctx context.Context, cam string, since time.Time) error {
	dirs := []string{filepath.Join(ix.Root, cam)}
	until := since.AddDate(0, 0, 2)
	if !since.IsZero() {
		dirs = dirs[:0]
		for d := since; d.Before(until); d = d.AddDate(0, 0, 1) {
			dirs = append(dirs, filepath.Join(ix.Root, cam, filepath.FromSlash(d.Format("2006/01/02"))))
		}
	}
	var files []fileInfo
	walk := func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".mp4") {
			return nil
		}
		rel, _ := filepath.Rel(ix.Root, p)
		rel = filepath.ToSlash(rel)
		_, start, ok := ParseSegmentPath(rel)
		if !ok {
			return nil
		}
		info, err := os.Stat(p) // not d.Info(): listings can lag for open files
		if err != nil {
			return nil
		}
		files = append(files, fileInfo{rel: rel, abs: p, size: info.Size(), mtime: info.ModTime(), start: start})
		return nil
	}
	for _, dir := range dirs {
		if err := filepath.WalkDir(dir, walk); err != nil { // a missing day folder is skipped
			return err
		}
	}
	indexed, err := ix.Store.SegmentPaths(ctx, cam, since)
	if err != nil {
		return err
	}
	stale := make(map[string]bool, len(indexed))
	for p := range indexed {
		if !since.IsZero() {
			if _, start, ok := ParseSegmentPath(p); !ok || !start.Before(until) {
				continue // outside the walked folders (clock jumped back)
			}
		}
		stale[p] = true
	}
	// The live file is the one with the latest start time.
	var live string
	for _, f := range files {
		if live == "" || f.rel > live {
			live = f.rel
		}
	}
	now := time.Now()
	for _, f := range files {
		delete(stale, f.rel)
		isLive := f.rel == live && now.Sub(f.mtime) < 2*time.Minute
		if f.size < 1024 {
			// ffmpeg created the file but never wrote data (failed connect).
			if !isLive && now.Sub(f.mtime) > time.Minute {
				_ = os.Remove(f.abs)
				_ = ix.Store.DeleteSegmentByPath(ctx, f.rel)
			}
			continue
		}
		if prevBytes, ok := indexed[f.rel]; ok && prevBytes == f.size && !isLive {
			continue
		}
		end := f.mtime.UTC()
		if !isLive {
			if d, err := ix.Probe(ctx, f.abs); err == nil && d > 0 {
				end = f.start.Add(d)
			}
		}
		if end.Before(f.start) {
			end = f.start
		}
		if err := ix.Store.UpsertSegment(ctx, store.Segment{CameraID: cam, Start: f.start, End: end, Path: f.rel, Bytes: f.size}); err != nil {
			return err
		}
	}
	// Rows whose file disappeared (manual deletion, disk swap).
	for p := range stale {
		if err := ix.Store.DeleteSegmentByPath(ctx, p); err != nil {
			return err
		}
	}
	return nil
}
