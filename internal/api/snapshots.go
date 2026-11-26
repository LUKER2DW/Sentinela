package api

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/LUKER2DW/sentinela/internal/config"
	"github.com/LUKER2DW/sentinela/internal/store"
	"github.com/LUKER2DW/sentinela/internal/sysinfo"
)

// Event snapshots (ADR-0015): one substream keyframe from go2rtc per event,
// at most one per camera every snapshotEvery; the detector's own picture
// for its events, at most one per camera every snapshotEvery too.
const (
	snapshotEvery   = 10 * time.Second
	snapshotTimeout = 12 * time.Second
	snapshotMax     = 2 << 20
)

// Snapshots live on the recordings disk, where the recorder's disk valve
// can free space only by deleting recordings: whatever opens events, they
// never take more than 1/snapshotShare of that disk (at least
// snapshotMinBudget). Past it the oldest pictures go first, at most once
// per snapshotPruneEvery between the hourly purges; until then new ones
// are not stored.
const (
	snapshotShare      = 100
	snapshotMinBudget  = 256 << 20
	snapshotPruneEvery = time.Minute
)

var errSnapshotBudget = errors.New("event snapshots use their whole share of the disk")

type snapshotter struct {
	mu   sync.Mutex
	last map[string]time.Time // gate key -> last snapshot

	budgetMu sync.Mutex
	bytes    int64 // size of the stored snapshots, once counted
	counted  bool
	prunedAt time.Time
	limit    int64 // tests: the budget (0 = the disk's share)
}

// HandleEvent receives ONVIF event transitions: live push to browsers, a
// snapshot when an event starts, then a notification (ADR-0013). Motion
// also wakes the object detector (ADR-0016).
func (s *Server) HandleEvent(cameraID, typ string, eventID int64, active bool, at time.Time) {
	if typ == "motion" {
		s.noteMotion(cameraID, active)
	}
	if eventID == 0 {
		// Not stored (the camera opens events too fast): its motion still
		// wakes the detector, nothing is pushed, snapshotted or notified.
		return
	}
	if !active {
		s.hub.Publish(LiveEvent{CameraID: cameraID, Type: typ, Active: false, At: at.UTC()})
		return
	}
	s.eventStarted(cameraID, typ, eventID, at, false)
}

// eventStarted publishes a new event, takes a snapshot unless the caller
// already stored one, then notifies.
func (s *Server) eventStarted(cameraID, typ string, eventID int64, at time.Time, haveSnapshot bool) {
	s.hub.Publish(LiveEvent{CameraID: cameraID, Type: typ, Active: true, At: at.UTC()})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), snapshotTimeout+5*time.Second)
		defer cancel()
		if !haveSnapshot {
			s.takeSnapshot(ctx, cameraID, eventID)
		}
		s.notify(ctx, cameraID, typ, eventID, at)
	}()
}

func (s *Server) snapshotPath(cameraID string, eventID int64) string {
	// Camera ids come from the configuration (validated when it loads);
	// Base keeps the id a single path element whatever the caller passes.
	return filepath.Join(s.Cfg.DataDir, "snapshots", filepath.Base(cameraID), strconv.FormatInt(eventID, 10)+".jpg")
}

// snapshotSlot takes the gate key's slot: false when its last snapshot
// was less than snapshotEvery ago. Keys are camera ids (go2rtc pictures)
// and "detector|"+camera id (the detector's), both from the configuration.
func (s *Server) snapshotSlot(key string) bool {
	s.snaps.mu.Lock()
	defer s.snaps.mu.Unlock()
	if s.snaps.last == nil {
		s.snaps.last = map[string]time.Time{}
	}
	if time.Since(s.snaps.last[key]) < snapshotEvery {
		return false
	}
	s.snaps.last[key] = time.Now()
	return true
}

func (s *Server) takeSnapshot(ctx context.Context, cameraID string, eventID int64) {
	if !s.snapshotSlot(cameraID) {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, snapshotTimeout)
	defer cancel()
	b, err := s.grabFrame(ctx, cameraID)
	if err == nil {
		err = s.saveSnapshot(cameraID, eventID, b)
	}
	if err != nil {
		s.Log.Warn("event snapshot failed", "camera", cameraID, "event", eventID, "err", err)
	}
}

// grabFrame asks go2rtc for one substream keyframe as JPEG.
func (s *Server) grabFrame(ctx context.Context, cameraID string) ([]byte, error) {
	if s.Cfg.Go2RTCStreamOff(cameraID + config.Go2RTCSubSuffix) {
		return nil, errors.New("substream off: its go2rtc name clashes with another camera")
	}
	u := s.go2rtc.String() + "/api/frame.jpeg?src=" + cameraID + config.Go2RTCSubSuffix
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	s.go2rtcAuth(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, errors.New("go2rtc unreachable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("go2rtc: HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, snapshotMax+1))
	if err != nil {
		return nil, err
	}
	if len(b) == 0 || len(b) > snapshotMax {
		return nil, fmt.Errorf("unexpected snapshot size %d", len(b))
	}
	return b, nil
}

// saveSnapshot stores an event picture atomically, within the snapshots'
// share of the disk.
func (s *Server) saveSnapshot(cameraID string, eventID int64, jpg []byte) (err error) {
	if len(jpg) == 0 || len(jpg) > snapshotMax {
		return fmt.Errorf("unexpected snapshot size %d", len(jpg))
	}
	if err := s.reserveSnapshot(int64(len(jpg))); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			s.snaps.budgetMu.Lock()
			s.snaps.bytes -= int64(len(jpg))
			s.snaps.budgetMu.Unlock()
		}
	}()
	dst := s.snapshotPath(cameraID, eventID)
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".snap-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(jpg)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}

// snapshotBudget is the most bytes the snapshots may take.
func (s *Server) snapshotBudget() int64 {
	if s.snaps.limit > 0 {
		return s.snaps.limit
	}
	d, err := sysinfo.DiskUsage(s.Cfg.DataDir)
	if b := int64(d.Total / snapshotShare); err == nil && b > snapshotMinBudget {
		return b
	}
	return snapshotMinBudget
}

// reserveSnapshot counts n more bytes of snapshots, making room by
// deleting the oldest ones when the budget is used up (at most once per
// snapshotPruneEvery); errSnapshotBudget when there is still no room.
func (s *Server) reserveSnapshot(n int64) error {
	b := &s.snaps
	b.budgetMu.Lock()
	defer b.budgetMu.Unlock()
	budget := s.snapshotBudget()
	if n > budget {
		return errSnapshotBudget // no room to make: keep what is there
	}
	if !b.counted || b.bytes+n > budget && time.Since(b.prunedAt) >= snapshotPruneEvery {
		b.bytes, b.counted, b.prunedAt = s.pruneSnapshots(time.Time{}, budget, n), true, time.Now()
	}
	if b.bytes+n > budget {
		return errSnapshotBudget
	}
	b.bytes += n
	return nil
}

func (s *Server) hasSnapshot(e store.Event) bool {
	_, err := os.Stat(s.snapshotPath(e.CameraID, e.ID))
	return err == nil
}

func (s *Server) eventSnapshot(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid event id")
		return
	}
	e, err := s.Store.EventByID(r.Context(), id)
	if err != nil || !s.canSee(r, e.CameraID) {
		writeErr(w, http.StatusNotFound, "event not found")
		return
	}
	f, err := os.Open(s.snapshotPath(e.CameraID, e.ID))
	if err != nil {
		writeErr(w, http.StatusNotFound, "no snapshot for this event")
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		writeErr(w, http.StatusNotFound, "no snapshot for this event")
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=86400, immutable")
	http.ServeContent(w, r, "", info.ModTime(), f)
}

// PruneCaches trims the disk caches the API keeps on the recordings disk:
// prepared timelapses (hours), Cloud thumbnails unused for 90 days, and the
// record of finished timelapse jobs. Called hourly.
func (s *Server) PruneCaches() {
	pruneTimelapses(s.timelapseDir(), time.Now())
	s.tl.pruneJobs()
	if s.thumbs != nil {
		s.thumbs.Prune(90 * 24 * time.Hour)
	}
}

// PurgeSnapshots deletes snapshot files older than maxAge (called hourly;
// events of that age are gone too), then the oldest others while they
// take more than their share of the disk.
func (s *Server) PurgeSnapshots(maxAge time.Duration) {
	b := &s.snaps
	b.budgetMu.Lock()
	defer b.budgetMu.Unlock()
	b.bytes, b.counted = s.pruneSnapshots(time.Now().Add(-maxAge), s.snapshotBudget(), 0), true
}

// snapshotBucket groups snapshot files by age for pruneSnapshots.
const snapshotBucket = 10 * 60 // seconds

// pruneSnapshots deletes the snapshot files last written before cut; then,
// when the rest and need more bytes do not fit in budget, the oldest of
// them until they fit in 90 % of it (with room for need). It returns the
// bytes kept. Memory stays small: files are tallied per ten minutes, whole
// ten minutes go while that keeps enough, and only the ten minutes where
// pruning stops are listed, to delete their oldest files one by one. So
// the newest pictures stay even when those ten minutes alone are over the
// target (snapshotSlot writes at most a few per camera a minute).
func (s *Server) pruneSnapshots(cut time.Time, budget, need int64) int64 {
	root := filepath.Join(s.Cfg.DataDir, "snapshots")
	walk := func(fn func(p string, info fs.FileInfo)) {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || !d.Type().IsRegular() {
				return nil
			}
			if info, err := d.Info(); err == nil {
				fn(p, info)
			}
			return nil
		})
	}
	tally := map[int64]int64{} // bucket -> bytes
	var total int64
	walk(func(p string, info fs.FileInfo) {
		if info.ModTime().Before(cut) {
			_ = os.Remove(p)
			return
		}
		tally[info.ModTime().Unix()/snapshotBucket] += info.Size()
		total += info.Size()
	})
	if total+need <= budget {
		return total
	}
	buckets := make([]int64, 0, len(tally))
	for k := range tally {
		buckets = append(buckets, k)
	}
	slices.Sort(buckets)
	target := min(budget/10*9, budget-need)
	whole, edge := 0, false // buckets[:whole] go; buckets[whole] file by file when edge
	for _, k := range buckets {
		if total <= target {
			break
		}
		if total-tally[k] < target {
			edge = true
			break
		}
		total -= tally[k]
		whole++
	}
	type file struct {
		path string
		mod  time.Time
		size int64
	}
	var last []file // the files of buckets[whole]
	var lastBytes int64
	walk(func(p string, info fs.FileInfo) {
		switch b := info.ModTime().Unix() / snapshotBucket; {
		case whole > 0 && b <= buckets[whole-1]:
			if os.Remove(p) != nil {
				total += info.Size() // still there
			}
		case edge && b == buckets[whole]:
			last = append(last, file{p, info.ModTime(), info.Size()})
			lastBytes += info.Size()
		}
	})
	if !edge {
		return total
	}
	// What is there now (a picture may have arrived since the tally).
	total += lastBytes - tally[buckets[whole]]
	slices.SortFunc(last, func(a, b file) int { return cmp.Or(a.mod.Compare(b.mod), strings.Compare(a.path, b.path)) })
	for _, f := range last {
		if total <= target {
			break
		}
		if os.Remove(f.path) == nil {
			total -= f.size
		}
	}
	return total
}
