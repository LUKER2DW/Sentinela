package recording

import (
	"context"
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
recording: {segment_seconds: 300, max_disk_percent: 90}
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
