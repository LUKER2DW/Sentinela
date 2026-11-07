package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LUKER2DW/sentinela/internal/store"
)

func TestTimelapseList(t *testing.T) {
	t0 := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	seg := func(path string, startMin, endMin int) store.Segment {
		return store.Segment{Path: path, Start: t0.Add(time.Duration(startMin) * time.Minute), End: t0.Add(time.Duration(endMin) * time.Minute)}
	}
	// 10:00–10:05, gap, 10:10–10:15.
	segs := []store.Segment{seg("a.mp4", 0, 5), seg("b's.mp4", 10, 15)}
	path := func(rel string) string { return "/rec/" + rel }

	tests := []struct {
		name     string
		from, to time.Time
		every    time.Duration
		want     int
		contains []string
	}{
		{"every 2 min skips the gap", t0, t0.Add(15 * time.Minute), 2 * time.Minute, 6,
			[]string{"file '/rec/a.mp4'\ninpoint 0.000\noutpoint 0.050", "inpoint 240.000", `file '/rec/b'\''s.mp4'`, "inpoint 240.000"}},
		{"range starting inside a segment", t0.Add(90 * time.Second), t0.Add(4 * time.Minute), time.Minute, 3,
			[]string{"inpoint 90.000", "inpoint 150.000", "inpoint 210.000"}},
		{"only gap", t0.Add(6 * time.Minute), t0.Add(9 * time.Minute), time.Minute, 0, nil},
		{"after the last segment", t0.Add(20 * time.Minute), t0.Add(30 * time.Minute), time.Minute, 0, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			list, n := timelapseList(segs, tt.from, tt.to, tt.every, path)
			if n != tt.want {
				t.Fatalf("samples = %d, want %d\n%s", n, tt.want, list)
			}
			for _, c := range tt.contains {
				if !strings.Contains(list, c) {
					t.Errorf("list lacks %q\n%s", c, list)
				}
			}
		})
	}
}

func TestPruneTimelapses(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	touch := func(name string, age time.Duration) {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		_ = os.Chtimes(p, now.Add(-age), now.Add(-age))
	}
	touch("old.mp4", 7*time.Hour)
	touch("stale.mp4.part", time.Hour)
	for i := range 7 {
		touch(fmt.Sprintf("n%d.mp4", i), time.Duration(i)*time.Minute)
	}
	pruneTimelapses(dir, now)
	left, _ := os.ReadDir(dir)
	var names []string
	for _, e := range left {
		names = append(names, e.Name())
	}
	// The newest five stay: room for the one about to be written.
	if want := "n0.mp4 n1.mp4 n2.mp4 n3.mp4 n4.mp4"; strings.Join(names, " ") != want {
		t.Fatalf("left %v, want %s", names, want)
	}
}

// Prepared timelapse end to end with a real ffmpeg: prepare until ready,
// then the file is served with byte ranges.
func TestTimelapsePrepare(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	ts, srv := newTestServer(t)
	srv.FFmpeg = ff
	rel := "cam1/2026/09/29/10-00-00.mp4"
	abs := srv.segmentPath(rel)
	_ = os.MkdirAll(filepath.Dir(abs), 0o750)
	out, err := exec.Command(ff, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc=size=320x180:rate=10",
		"-t", "60", "-c:v", "libx264", "-g", "10", "-pix_fmt", "yuv420p", "-y", abs).CombinedOutput()
	if err != nil {
		t.Skipf("ffmpeg cannot make a test clip: %v %s", err, out)
	}
	t0 := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	if err := srv.Store.UpsertSegment(context.Background(), store.Segment{CameraID: "cam1", Start: t0, End: t0.Add(time.Minute), Path: rel, Bytes: 1}); err != nil {
		t.Fatal(err)
	}
	c := newClient(t, ts.URL)
	c.do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`)
	q := "from=" + t0.Format(time.RFC3339) + "&to=" + t0.Add(time.Minute).Format(time.RFC3339) + "&every=10&fps=10"
	var state string
	for i := 0; i < 100 && state != "ready"; i++ {
		code, m, body := c.do("GET", "/api/cameras/cam1/timelapse/prepare?"+q, "")
		if code != 200 {
			t.Fatalf("prepare = %d %s", code, body)
		}
		state, _ = m["state"].(string)
		if state == "failed" || state == "empty" {
			t.Fatalf("prepare state %s", body)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if state != "ready" {
		t.Fatal("timelapse never became ready")
	}
	req, _ := http.NewRequest("GET", ts.URL+"/api/cameras/cam1/timelapse?"+q, nil)
	req.Header.Set("Range", "bytes=0-99")
	resp, err := c.hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent || resp.Header.Get("Content-Type") != "video/mp4" {
		t.Fatalf("prepared file = %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
}
