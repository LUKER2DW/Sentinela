package api

import (
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
