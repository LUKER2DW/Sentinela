package apps

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestWalkBounded(t *testing.T) {
	dir := t.TempDir()
	for _, p := range []string{"a/x", "a/b/y", "skip/z", "c"} {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("1"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, c := range []struct {
		name string
		ctx  context.Context
		max  int
		want []string
		err  error
	}{
		{"all, folders first", context.Background(), 100, []string{"a", "a/b", "a/b/y", "a/x", "c", "skip"}, nil},
		{"entry cap", context.Background(), 3, nil, errTooManyEntries},
		{"canceled", canceled, 100, nil, context.Canceled},
	} {
		t.Run(c.name, func(t *testing.T) {
			seen := map[string]bool{}
			var order []string
			err := walkBounded(c.ctx, root, c.max, false, func(p string, d fs.DirEntry) error {
				if !seen[path.Dir(p)] && path.Dir(p) != "." {
					t.Errorf("%s visited before its folder", p)
				}
				seen[p] = true
				order = append(order, p)
				if d.Name() == "skip" {
					return fs.SkipDir
				}
				return nil
			})
			if c.err != nil {
				if !errors.Is(err, c.err) {
					t.Fatalf("err %v, want %v", err, c.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			sorted := slices.Clone(order)
			slices.Sort(sorted)
			got := strings.Join(sorted, ",")
			if got != strings.Join(c.want, ",") {
				t.Fatalf("visited %s", got)
			}
		})
	}
	deep := dir
	for i := 0; i < walkMaxDepth+1; i++ {
		deep = filepath.Join(deep, "d")
	}
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Skip("deep folders unavailable:", err)
	}
	if err := walkBounded(context.Background(), root, 1000, false, func(string, fs.DirEntry) error { return nil }); !errors.Is(err, errTooDeep) {
		t.Fatalf("deep tree: %v", err)
	}
}

// A local folder snapshot copies no more than its budget, even if a file
// grows after it was listed.
func TestCopyRegularBudget(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "f"), []byte("0123456789"), 0o755); err != nil {
		t.Fatal(err)
	}
	sr, _ := os.OpenRoot(src)
	defer sr.Close()
	dr, _ := os.OpenRoot(dst)
	defer dr.Close()
	for _, c := range []struct {
		name string
		max  int64
		err  error
	}{
		{"fits", 10, nil},
		{"over", 9, errTooLarge},
	} {
		t.Run(c.name, func(t *testing.T) {
			n, err := copyRegular(sr, dr, "f", 0o755, c.max)
			if !errors.Is(err, c.err) || c.err == nil && n != 10 {
				t.Fatalf("%d, %v", n, err)
			}
		})
	}
}

// Resolve of a local folder stops when its context ends.
func TestLocalResolveHonoursContext(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (LocalSource{Root: filepath.Dir(dir)}).Resolve(ctx, &App{Path: filepath.Base(dir)}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled resolve: %v", err)
	}
	if v, err := (LocalSource{Root: filepath.Dir(dir)}).Resolve(context.Background(), &App{Path: filepath.Base(dir)}); err != nil || !strings.HasPrefix(v.Label, "local-") {
		t.Fatalf("%+v %v", v, err)
	}
}
