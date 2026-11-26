package recording

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LUKER2DW/sentinela/internal/config"
	"github.com/LUKER2DW/sentinela/internal/store"
	"github.com/LUKER2DW/sentinela/internal/sysinfo"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func testCfg(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Parse([]byte(`
recording: {segment_seconds: 300, max_disk_percent: 90, alert_retention_days: 30}
cameras:
  - {id: cam1, host: 192.168.1.20, user_env: U, password_env: P, retention_days: 7}
`), func(k string) string { return map[string]string{"U": "admin", "P": "Pa@ss"}[k] })
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestArgsCopyVideoAlways(t *testing.T) {
	cfg := testCfg(t)
	cam := &cfg.Cameras[0]
	args := strings.Join(Args(cam, cfg.Recording, "/data/recordings/cam1"), " ")
	for _, want := range []string{"-c:v copy", "-c:a aac", "-segment_time 300", "-rtsp_transport tcp",
		"/data/recordings/cam1/%Y/%m/%d/%H-%M-%S.mp4", "password=Pa@ss&channel=1&stream=0.sdp"} {
		if !strings.Contains(args, want) {
			t.Errorf("args missing %q: %s", want, args)
		}
	}
	for _, bad := range []string{"libx26", "-vf", "-c:v h"} {
		if strings.Contains(args, bad) {
			t.Errorf("args must not transcode video (%q)", bad)
		}
	}
	off := false
	cam.RecordAudio = &off
	if a := strings.Join(Args(cam, cfg.Recording, "x"), " "); !strings.Contains(a, "-an") || strings.Contains(a, "aac") {
		t.Errorf("audio off not honored: %s", a)
	}
}

func TestParseSegmentPath(t *testing.T) {
	cam, st, ok := ParseSegmentPath("cam1/2026/09/28/13-05-00.mp4")
	if !ok || cam != "cam1" || !st.Equal(time.Date(2026, 9, 28, 13, 5, 0, 0, time.UTC)) {
		t.Fatalf("got %s %v %v", cam, st, ok)
	}
	for _, bad := range []string{"cam1/2026/09/28/x.mp4", "cam1/13-05-00.mp4", "cam1/2026/09/28/13-05-00.mkv"} {
		if _, _, ok := ParseSegmentPath(bad); ok {
			t.Errorf("accepted %q", bad)
		}
	}
}

func writeSeg(t *testing.T, root, rel string, size int, mtime time.Time) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(p, mtime, mtime)
}

func TestIndexerAndRetention(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	root := filepath.Join(dir, "recordings")
	st, err := store.Open(ctx, filepath.Join(dir, "db", "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	now := time.Now().UTC().Truncate(time.Second)
	old := now.Add(-10 * 24 * time.Hour)
	oldRel := "cam1/" + old.Format("2006/01/02/15-04-05") + ".mp4"
	recentRel := "cam1/" + now.Add(-10*time.Minute).Format("2006/01/02/15-04-05") + ".mp4"
	liveRel := "cam1/" + now.Add(-2*time.Minute).Format("2006/01/02/15-04-05") + ".mp4"
	writeSeg(t, root, oldRel, 4096, old.Add(5*time.Minute))
	writeSeg(t, root, recentRel, 4096, now.Add(-5*time.Minute))
	writeSeg(t, root, liveRel, 4096, now)
	writeSeg(t, root, "cam1/"+now.Add(-time.Hour).Format("2006/01/02/15-04-05")+".mp4", 10, now.Add(-time.Hour)) // empty failed start

	ix := NewIndexer(root, st, quiet)
	probes := 0
	ix.Probe = func(context.Context, string) (time.Duration, error) { probes++; return 5 * time.Minute, nil }
	if err := ix.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if probes != 2 {
		t.Fatalf("probed %d files, want 2 (closed ones only)", probes)
	}
	segs, _ := st.SegmentsBetween(ctx, "cam1", old.Add(-time.Hour), now.Add(time.Hour))
	if len(segs) != 3 {
		t.Fatalf("indexed %d segments, want 3", len(segs))
	}
	// Second scan must not re-probe unchanged files.
	if err := ix.Scan(ctx); err != nil || probes != 2 {
		t.Fatalf("rescan probes=%d err=%v", probes, err)
	}

	cfg := testCfg(t)
	ret := NewRetention(root, st, cfg, quiet)
	ret.Disk = func(string) (sysinfo.Disk, error) { return sysinfo.Disk{Total: 100, Pct: 50}, nil }
	n, err := ret.Enforce(ctx)
	if err != nil || n != 1 {
		t.Fatalf("age retention deleted %d err=%v", n, err)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(oldRel))); !os.IsNotExist(err) {
		t.Fatal("old file still on disk")
	}

	// Disk pressure: pretend we're full until one more segment is gone.
	calls := 0
	ret.Disk = func(string) (sysinfo.Disk, error) {
		calls++
		if calls == 1 {
			return sysinfo.Disk{Total: 100, Pct: 95}, nil
		}
		return sysinfo.Disk{Total: 100, Pct: 80}, nil
	}
	if n, err := ret.Enforce(ctx); err != nil || n != 2 {
		t.Fatalf("disk retention deleted %d err=%v", n, err)
	}
}

func TestIndexerScanRecent(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	root := filepath.Join(dir, "recordings")
	st, err := store.Open(ctx, filepath.Join(dir, "db", "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	now := time.Now().UTC().Truncate(time.Second)
	rel := func(t time.Time) string { return "cam1/" + t.Format("2006/01/02/15-04-05") + ".mp4" }
	oldRel := rel(now.Add(-5 * 24 * time.Hour))
	yesterdayRel := rel(now.Add(-24 * time.Hour))
	writeSeg(t, root, oldRel, 4096, now.Add(-5*24*time.Hour))
	writeSeg(t, root, yesterdayRel, 4096, now.Add(-24*time.Hour))

	ix := NewIndexer(root, st, quiet)
	var probed []string
	ix.Probe = func(_ context.Context, p string) (time.Duration, error) {
		probed = append(probed, filepath.ToSlash(p))
		return 5 * time.Minute, nil
	}
	count := func() int {
		segs, _ := st.SegmentsBetween(ctx, "cam1", now.Add(-10*24*time.Hour), now.Add(time.Hour))
		return len(segs)
	}

	// Recent pass: only today's and yesterday's folders are walked.
	if err := ix.ScanRecent(ctx, now); err != nil {
		t.Fatal(err)
	}
	if count() != 1 || len(probed) != 1 || !strings.HasSuffix(probed[0], yesterdayRel) {
		t.Fatalf("recent scan indexed %d, probed %v; want only %s", count(), probed, yesterdayRel)
	}
	if err := ix.Scan(ctx); err != nil || count() != 2 {
		t.Fatalf("full scan indexed %d err=%v, want 2", count(), err)
	}

	// An old file deleted by hand keeps its row until the next full scan.
	os.Remove(filepath.Join(root, filepath.FromSlash(oldRel)))
	if err := ix.ScanRecent(ctx, now); err != nil || count() != 2 {
		t.Fatalf("recent scan touched old rows: %d err=%v", count(), err)
	}
	if err := ix.Scan(ctx); err != nil || count() != 1 {
		t.Fatalf("full scan kept a deleted file: %d err=%v", count(), err)
	}
	if len(probed) != 2 {
		t.Fatalf("probed %d files, want 2 (each closed file once)", len(probed))
	}
}

func TestNewestFileByName(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()
	day := filepath.Join(dir, now.Format("2006/01/02"))
	os.MkdirAll(day, 0o755)
	older := filepath.Join(day, "00-00-00.mp4")
	newer := filepath.Join(day, "00-05-00.mp4")
	os.WriteFile(newer, make([]byte, 10), 0o644)
	os.WriteFile(older, make([]byte, 99), 0o644) // written later, but older name
	got, size := newestFile(dir)
	if got != newer || size != 10 {
		t.Fatalf("newestFile = %s (%d), want %s", got, size, newer)
	}
}

func TestTieredRetention(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	root := filepath.Join(dir, "recordings")
	st, err := store.Open(ctx, filepath.Join(dir, "db", "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now().UTC().Truncate(time.Second)
	add := func(start time.Time) string {
		rel := "cam1/" + start.Format("2006/01/02/15-04-05") + ".mp4"
		writeSeg(t, root, rel, 4096, start.Add(5*time.Minute))
		if err := st.UpsertSegment(ctx, store.Segment{CameraID: "cam1", Start: start, End: start.Add(5 * time.Minute), Path: rel, Bytes: 4096}); err != nil {
			t.Fatal(err)
		}
		return rel
	}
	quietSeg := add(now.Add(-10 * 24 * time.Hour))
	alertStart := now.Add(-10*24*time.Hour + time.Hour)
	alertSeg := add(alertStart)
	motionSeg := add(alertStart.Add(time.Hour))
	id, _ := st.OpenEvent(ctx, "cam1", "line", alertStart.Add(time.Minute), "")
	_ = st.CloseEvent(ctx, id, alertStart.Add(2*time.Minute))
	m, _ := st.OpenEvent(ctx, "cam1", "motion", alertStart.Add(time.Hour+time.Minute), "")
	_ = st.CloseEvent(ctx, m, alertStart.Add(time.Hour+2*time.Minute))

	ret := NewRetention(root, st, testCfg(t), quiet) // 7 days, alerts 30 days
	ret.Disk = func(string) (sysinfo.Disk, error) { return sysinfo.Disk{Total: 100, Pct: 50}, nil }
	if n, err := ret.Enforce(ctx); err != nil || n != 2 {
		t.Fatalf("deleted %d err=%v, want 2 (quiet + motion)", n, err)
	}
	for rel, want := range map[string]bool{quietSeg: false, motionSeg: false, alertSeg: true} {
		_, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
		if (err == nil) != want {
			t.Errorf("%s exists=%v, want %v", rel, err == nil, want)
		}
	}
	// 25 days later the alert segment is 35 days old: gone too.
	ret.now = func() time.Time { return now.Add(25 * 24 * time.Hour) }
	if n, err := ret.Enforce(ctx); err != nil || n != 1 {
		t.Fatalf("second pass deleted %d err=%v", n, err)
	}
}

// Retention 0 keeps everything by age; only disk pressure deletes, and
// events older than the oldest remaining recording go with it.
func TestUnlimitedRetentionFillsDisk(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	root := filepath.Join(dir, "recordings")
	st, err := store.Open(ctx, filepath.Join(dir, "db", "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cfg, err := config.Parse([]byte(`
cameras:
  - {id: cam1, host: 192.168.1.20, user_env: U, password_env: P}
`), func(k string) string { return map[string]string{"U": "admin", "P": "Pa@ss"}[k] })
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	var rels []string
	for i := 0; i < 22; i++ { // 400 days old down to 190, 10 days apart
		s := now.Add(-time.Duration(400-10*i) * 24 * time.Hour)
		rel := "cam1/" + s.Format("2006/01/02/15-04-05") + ".mp4"
		writeSeg(t, root, rel, 4096, s.Add(5*time.Minute))
		if err := st.UpsertSegment(ctx, store.Segment{CameraID: "cam1", Start: s, End: s.Add(5 * time.Minute), Path: rel, Bytes: 4096}); err != nil {
			t.Fatal(err)
		}
		id, _ := st.OpenEvent(ctx, "cam1", "motion", s.Add(time.Minute), "")
		_ = st.CloseEvent(ctx, id, s.Add(2*time.Minute))
		rels = append(rels, rel)
	}
	ret := NewRetention(root, st, cfg, quiet)
	ret.Disk = func(string) (sysinfo.Disk, error) { return sysinfo.Disk{Total: 100, Pct: 50}, nil }
	if n, err := ret.Enforce(ctx); err != nil || n != 0 {
		t.Fatalf("age retention deleted %d err=%v, want 0", n, err)
	}
	if oldest, ok, _ := st.OldestEventStart(ctx); !ok || now.Sub(oldest) < 399*24*time.Hour {
		t.Fatalf("events purged without disk pressure: %v %v", oldest, ok)
	}
	// Full disk (95 % default): the oldest batch goes, then 93 % stops.
	pcts := []float64{96, 93}
	ret.Disk = func(string) (sysinfo.Disk, error) {
		p := pcts[0]
		if len(pcts) > 1 {
			pcts = pcts[1:]
		}
		return sysinfo.Disk{Total: 100, Pct: p}, nil
	}
	if n, err := ret.Enforce(ctx); err != nil || n != 20 {
		t.Fatalf("disk retention deleted %d err=%v, want one batch of 20", n, err)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rels[20]))); err != nil {
		t.Fatalf("newer recording deleted: %v", err)
	}
	// Events older than the oldest recording left (190+10 days) are gone.
	if oldest, ok, _ := st.OldestEventStart(ctx); !ok || now.Sub(oldest) > 201*24*time.Hour {
		t.Fatalf("oldest event %v ok=%v, want the one of the oldest kept recording", oldest, ok)
	}
}

// Disk pressure deletes until 1 point below the limit, not just under it.
func TestDiskPressureHysteresis(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	root := filepath.Join(dir, "recordings")
	st, err := store.Open(ctx, filepath.Join(dir, "db", "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	start := time.Now().UTC().Truncate(time.Hour).Add(-3 * time.Hour)
	for i := 0; i < 25; i++ { // recent: age retention keeps them all
		s := start.Add(time.Duration(i) * 5 * time.Minute)
		rel := "cam1/" + s.Format("2006/01/02/15-04-05") + ".mp4"
		writeSeg(t, root, rel, 4096, s.Add(5*time.Minute))
		if err := st.UpsertSegment(ctx, store.Segment{CameraID: "cam1", Start: s, End: s.Add(5 * time.Minute), Path: rel, Bytes: 4096}); err != nil {
			t.Fatal(err)
		}
	}
	ret := NewRetention(root, st, testCfg(t), quiet) // limit 90 %
	pcts := []float64{91, 89, 87}
	ret.Disk = func(string) (sysinfo.Disk, error) {
		p := pcts[0]
		if len(pcts) > 1 {
			pcts = pcts[1:]
		}
		return sysinfo.Disk{Total: 100, Pct: p}, nil
	}
	// 91 %: one batch of 20; 89 % is under the limit but within the margin:
	// a second batch; 87 %: done.
	if n, err := ret.Enforce(ctx); err != nil || n != 25 {
		t.Fatalf("deleted %d err=%v, want 25", n, err)
	}
	pcts = []float64{89}
	if n, err := ret.Enforce(ctx); err != nil || n != 0 {
		t.Fatalf("a pass under the limit deleted %d err=%v", n, err)
	}
}

func TestVolumeReadyNeedsMarker(t *testing.T) {
	dir := t.TempDir()
	r := &Recorder{Marker: filepath.Join(dir, ".sentinela-volume")}
	if err := r.volumeReady(); !errors.Is(err, ErrNoVolume) {
		t.Fatalf("missing marker: %v", err)
	}
	if err := os.WriteFile(r.Marker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.volumeReady(); err != nil {
		t.Fatalf("marker present: %v", err)
	}
	if err := (&Recorder{}).volumeReady(); err != nil {
		t.Fatalf("check off: %v", err)
	}
}

// Disk pressure frees unprotected recordings first, whatever camera they
// belong to; protected ones go only when nothing else is left, so a full
// disk never stops recording.
func TestDiskPressureProtectedLast(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	root := filepath.Join(dir, "recordings")
	st, err := store.Open(ctx, filepath.Join(dir, "db", "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	start := time.Now().UTC().Truncate(time.Hour).Add(-3 * time.Hour)
	var protected []string
	for i := 0; i < 6; i++ {
		cam := "cam1" // protected, and older
		if i >= 3 {
			cam = "cam2"
		}
		s := start.Add(time.Duration(i) * 5 * time.Minute)
		rel := cam + "/" + s.Format("2006/01/02/15-04-05") + ".mp4"
		writeSeg(t, root, rel, 4096, s.Add(5*time.Minute))
		if err := st.UpsertSegment(ctx, store.Segment{CameraID: cam, Start: s, End: s.Add(5 * time.Minute), Path: rel, Bytes: 4096}); err != nil {
			t.Fatal(err)
		}
		if cam == "cam1" {
			segs, _ := st.SegmentsBetween(ctx, cam, s, s.Add(time.Second))
			if err := st.SetSegmentProtected(ctx, segs[0].ID, true); err != nil {
				t.Fatal(err)
			}
			protected = append(protected, rel)
		}
	}
	ret := NewRetention(root, st, testCfg(t), quiet) // limit 90 %
	full := func() {
		pcts := []float64{96, 80}
		ret.Disk = func(string) (sysinfo.Disk, error) {
			p := pcts[0]
			if len(pcts) > 1 {
				pcts = pcts[1:]
			}
			return sysinfo.Disk{Total: 100, Pct: p}, nil
		}
	}
	full()
	if n, err := ret.Enforce(ctx); err != nil || n != 3 {
		t.Fatalf("first pass deleted %d err=%v, want the 3 unprotected", n, err)
	}
	for _, rel := range protected {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("protected %s deleted while unprotected footage was left: %v", rel, err)
		}
	}
	full()
	if n, err := ret.Enforce(ctx); err != nil || n != 3 {
		t.Fatalf("only protected left on a full disk: deleted %d err=%v, want 3", n, err)
	}
	if left, _ := st.OldestProtectedSegments(ctx, 10); len(left) != 0 {
		t.Fatalf("%d protected segments kept on a full disk", len(left))
	}
	// Not silently: an audit row names how many and whose (the API turns
	// it into a notice to the admins).
	rows, _ := st.RecorderNoticesAfter(ctx, store.AuditProtectValve, 0, 10)
	if len(rows) != 1 || rows[0].Target != "cam1" || !strings.HasPrefix(rows[0].Detail, "3 protected clips deleted") {
		t.Fatalf("valve audit rows: %+v", rows)
	}
}

// A removed camera's recordings (left under its retired id) age out with
// default_retention_days; with 0 they wait for disk pressure.
func TestRemovedCameraAgesOutWithDefault(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		days int
		left int
	}{{0, 2}, {10, 1}} {
		dir := t.TempDir()
		root := filepath.Join(dir, "recordings")
		st, err := store.Open(ctx, filepath.Join(dir, "db", "t.db"))
		if err != nil {
			t.Fatal(err)
		}
		cfg := testCfg(t)
		cfg.Recording.DefaultRetentionDays = tc.days
		for i, age := range []time.Duration{20 * 24 * time.Hour, time.Hour} {
			s := time.Now().UTC().Add(-age)
			rel := fmt.Sprintf("gone/%d.mp4", i)
			writeSeg(t, root, rel, 10, s)
			if err := st.UpsertSegment(ctx, store.Segment{CameraID: "gone", Start: s, End: s.Add(time.Minute), Path: rel, Bytes: 10}); err != nil {
				t.Fatal(err)
			}
		}
		ret := NewRetention(root, st, cfg, quiet)
		ret.Disk = func(string) (sysinfo.Disk, error) { return sysinfo.Disk{Total: 100, Pct: 10}, nil }
		if _, err := ret.Enforce(ctx); err != nil {
			t.Fatal(err)
		}
		left, _ := st.SegmentsBetween(ctx, "gone", time.Now().Add(-30*24*time.Hour), time.Now())
		if len(left) != tc.left {
			t.Errorf("default %d days: %d segments left, want %d", tc.days, len(left), tc.left)
		}
		st.Close()
	}
}

// A removed camera's protected clips cannot be unprotected by anyone: they
// age out after the longer of the alert and default tiers, and are kept
// like its alert clips when alerts have no age limit. A configured
// camera's protected clips never age out.
func TestRemovedCameraProtectedAgesOut(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		alert, def int
		goneLeft   int
	}{
		{30, 10, 2}, // 20 and 1 days old: both within 30 days
		{15, 10, 1}, // the 20-day-old one goes
		{0, 10, 2},  // alerts kept without an age limit: so are these
	} {
		dir := t.TempDir()
		root := filepath.Join(dir, "recordings")
		st, err := store.Open(ctx, filepath.Join(dir, "db", "t.db"))
		if err != nil {
			t.Fatal(err)
		}
		cfg := testCfg(t)
		cfg.Recording.AlertRetentionDays, cfg.Recording.DefaultRetentionDays = tc.alert, tc.def
		cfg.Cameras[0].RetentionDays = 7
		for _, cam := range []string{"gone", "cam1"} {
			for i, age := range []time.Duration{20 * 24 * time.Hour, 24 * time.Hour} {
				s := time.Now().UTC().Add(-age)
				rel := fmt.Sprintf("%s/%d.mp4", cam, i)
				writeSeg(t, root, rel, 10, s)
				if err := st.UpsertSegment(ctx, store.Segment{CameraID: cam, Start: s, End: s.Add(time.Minute), Path: rel, Bytes: 10}); err != nil {
					t.Fatal(err)
				}
				segs, _ := st.SegmentsBetween(ctx, cam, s, s.Add(time.Second))
				if err := st.SetSegmentProtected(ctx, segs[0].ID, true); err != nil {
					t.Fatal(err)
				}
			}
		}
		ret := NewRetention(root, st, cfg, quiet)
		ret.Disk = func(string) (sysinfo.Disk, error) { return sysinfo.Disk{Total: 100, Pct: 10}, nil }
		if _, err := ret.Enforce(ctx); err != nil {
			t.Fatal(err)
		}
		gone, _ := st.SegmentsBetween(ctx, "gone", time.Now().Add(-30*24*time.Hour), time.Now())
		kept, _ := st.SegmentsBetween(ctx, "cam1", time.Now().Add(-30*24*time.Hour), time.Now())
		if len(gone) != tc.goneLeft || len(kept) != 2 {
			t.Errorf("alert %d, default %d: removed camera kept %d (want %d), configured kept %d (want 2)", tc.alert, tc.def, len(gone), tc.goneLeft, len(kept))
		}
		st.Close()
	}
}

// A removed camera's id configured again: the indexer walks the same
// folder, and the old camera's archived recordings stay archived (not
// handed to the new camera, nor probed again on every scan).
func TestIndexerKeepsArchivedSegments(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	root := filepath.Join(dir, "recordings")
	st, err := store.Open(ctx, filepath.Join(dir, "db", "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	old := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Second)
	rel := "cam2/" + old.Format("2006/01/02/15-04-05") + ".mp4"
	writeSeg(t, root, rel, 4096, old.Add(5*time.Minute))
	ix := NewIndexer(root, st, quiet)
	probes := 0
	ix.Probe = func(context.Context, string) (time.Duration, error) { probes++; return 5 * time.Minute, nil }
	if err := ix.Scan(ctx); err != nil || probes != 1 {
		t.Fatalf("first scan: probes %d err %v", probes, err)
	}
	for _, ids := range [][]string{{"cam2"}, {}, {"cam2"}} { // added, removed, configured again
		if _, _, _, err := st.SyncCameraIDs(ctx, ids, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := ix.Scan(ctx); err != nil || probes != 1 {
		t.Fatalf("scan after revival: probes %d err %v", probes, err)
	}
	if segs, _ := st.SegmentsBetween(ctx, "cam2", old.Add(-time.Hour), time.Now()); len(segs) != 0 {
		t.Fatalf("the old camera's recording is on the new camera: %+v", segs)
	}
	ids, _ := st.SegmentCameraIDs(ctx)
	if len(ids) != 1 || !strings.HasPrefix(ids[0], "cam2"+store.ArchiveSep) {
		t.Fatalf("segment camera ids %v", ids)
	}
	// Its file deleted by hand: the archived row goes with it.
	_ = os.Remove(filepath.Join(root, filepath.FromSlash(rel)))
	if err := ix.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if ids, _ := st.SegmentCameraIDs(ctx); len(ids) != 0 {
		t.Fatalf("row of a deleted archived file kept: %v", ids)
	}
}
