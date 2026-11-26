//go:build !windows

package apps

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// The disk walk neither blocks on a FIFO nor follows a link a service
// planted in its folder, also when one stands where a folder was listed.
func TestDirSizeIgnoresFIFOsAndLinks(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "big"), make([]byte, 1<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "f"), []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(root, "pipe"), 0o600); err != nil {
		t.Skip("mkfifo unavailable:", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	done := make(chan int64, 1)
	go func() {
		n, _ := dirSizeBounded(root, time.Minute, 1000)
		done <- n
	}()
	select {
	case n := <-done:
		if n != 10 {
			t.Fatalf("size %d: a link was followed", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the disk walk blocks on a FIFO")
	}
	// What a folder listed earlier was swapped for: opened relative to its
	// parent, as a folder only, without following or blocking.
	d, err := openWalkDir(root)
	if err != nil {
		t.Fatal(err)
	}
	defer d.close()
	for _, name := range []string{"pipe", "link", "f"} {
		opened := make(chan error, 1)
		go func() {
			c, err := d.openDir(name)
			if err == nil {
				c.close()
			}
			opened <- err
		}()
		select {
		case err := <-opened:
			if err == nil {
				t.Errorf("%s opened as a folder", name)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("opening %s blocks", name)
		}
	}
}
