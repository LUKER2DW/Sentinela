package apps

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDirSizeDepthCap(t *testing.T) {
	for _, c := range []struct {
		name     string
		depth    int
		complete bool
		total    int64
	}{
		{"within", diskMaxDepth - 2, true, 20},
		{"below the cap", diskMaxDepth + 3, false, 10},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "top"), []byte("0123456789"), 0o644); err != nil {
				t.Fatal(err)
			}
			// Short names keep the deepest path within Windows' limits.
			deep := root
			for i := 0; i < c.depth; i++ {
				deep = filepath.Join(deep, "d")
			}
			if err := os.MkdirAll(deep, 0o755); err != nil {
				t.Skip("deep folders unavailable:", err)
			}
			if err := os.WriteFile(filepath.Join(deep, "f"), []byte("0123456789"), 0o644); err != nil {
				t.Skip("deep folders unavailable:", err)
			}
			got, complete := dirSizeBounded(root, time.Minute, 1_000_000)
			if got != c.total || complete != c.complete {
				t.Fatalf("%d bytes, complete %v", got, complete)
			}
		})
	}
}

// Wide folders of folders are walked whole (one batch held per level).
func TestDirSizeWideFolders(t *testing.T) {
	root := t.TempDir()
	n := 0
	for i := 0; i < 2*diskBatch+3; i++ {
		d := filepath.Join(root, fmt.Sprintf("dir-%03d", i))
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "f"), []byte("0123"), 0o644); err != nil {
			t.Fatal(err)
		}
		n++
	}
	if got, complete := dirSizeBounded(root, time.Minute, 1_000_000); got != int64(n)*4 || !complete {
		t.Fatalf("%d bytes, complete %v", got, complete)
	}
}
