package api

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/LUKER2DW/sentinela/internal/store"
)

// A segment row and a link the recorder planted under recordings/ must not
// make the API serve a file from the rest of its data folder.
func TestRecordingRefusesLinksOutOfTheFolder(t *testing.T) {
	ts, srv := newTestServer(t)
	data := srv.Cfg.DataDir
	rec := filepath.Join(data, "recordings")
	secretDir := filepath.Join(data, "apps-agent")
	for _, d := range []string{filepath.Join(rec, "cam1"), secretDir} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	const secret = "APPS-AGENT-TOKEN-MARKER"
	if err := os.WriteFile(filepath.Join(secretDir, "token"), []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rec, "cam1", "ok.mp4"), []byte("plain recording bytes"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(secretDir, "token"), filepath.Join(rec, "cam1", "file-link.mp4")); err != nil {
		t.Skipf("symbolic links unavailable here: %v", err)
	}
	if err := os.Symlink(secretDir, filepath.Join(rec, "cam1", "dir-link")); err != nil {
		t.Fatal(err)
	}
	// A relative link that stays inside the folder keeps working (ffmpeg never
	// writes links; os.Root refuses absolute ones).
	if err := os.Symlink("ok.mp4", filepath.Join(rec, "cam1", "inner-link.mp4")); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	t0 := time.Now().UTC().Add(-time.Hour)
	ids := map[string]int64{}
	for i, rel := range []string{"cam1/ok.mp4", "cam1/file-link.mp4", "cam1/dir-link/token", "cam1/inner-link.mp4"} {
		start := t0.Add(time.Duration(i) * time.Minute)
		if err := srv.Store.UpsertSegment(ctx, store.Segment{CameraID: "cam1", Start: start, End: start.Add(time.Minute), Path: rel, Bytes: 1}); err != nil {
			t.Fatal(err)
		}
	}
	segs, err := srv.Store.SegmentsBetween(ctx, "cam1", t0.Add(-time.Minute), t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range segs {
		ids[s.Path] = s.ID
	}

	admin := newClient(t, ts.URL)
	if code, _, body := admin.do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`); code != 200 {
		t.Fatalf("setup = %d %s", code, body)
	}
	for _, tc := range []struct {
		rel  string
		want int
		path bool // segmentPath gives a path (export, timelapse)
	}{
		{"cam1/ok.mp4", 200, true},
		{"cam1/inner-link.mp4", 200, true},
		{"cam1/file-link.mp4", 404, false},
		{"cam1/dir-link/token", 404, false},
	} {
		code, _, body := admin.do("GET", "/api/recordings/"+strconv.FormatInt(ids[tc.rel], 10), "")
		if code != tc.want || strings.Contains(body, secret) {
			t.Errorf("%s: %d, leaked=%v", tc.rel, code, strings.Contains(body, secret))
		}
		if got := srv.segmentPath(tc.rel) != ""; got != tc.path {
			t.Errorf("segmentPath(%s) given = %v, want %v", tc.rel, got, tc.path)
		}
	}
}
