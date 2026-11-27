package api

import (
	"path/filepath"
	"testing"

	"github.com/LUKER2DW/sentinela/internal/config"
)

// A snapshot path always stays one camera folder under data_dir/snapshots,
// whatever id reaches it ("..", which filepath.Base kept, included).
func TestSnapshotPathRefusesIDsOutsideTheRule(t *testing.T) {
	s := &Server{Cfg: &config.Config{DataDir: "/data"}}
	for _, id := range []string{"..", ".", "", "a/b", "../x", `..\x`, "Cam", "x\x00y"} {
		if p, ok := s.snapshotPath(id, 1); ok {
			t.Errorf("snapshotPath(%q) = %q, want refused", id, p)
		}
	}
	want := filepath.Join("/data", "snapshots", "cam1", "7.jpg")
	if p, ok := s.snapshotPath("cam1", 7); !ok || p != want {
		t.Errorf("snapshotPath(cam1, 7) = %q, %v; want %q", p, ok, want)
	}
}
