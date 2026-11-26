//go:build !windows

package apps

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A FIFO in a local (Cloud) folder neither blocks a snapshot nor is copied;
// the first-deploy import refuses it.
func TestLocalFolderFIFODoesNotBlock(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe"), 0o600); err != nil {
		t.Skip("mkfifo unavailable:", err)
	}
	out := filepath.Join(t.TempDir(), "snap")
	done := make(chan error, 2)
	go func() {
		done <- LocalSource{Root: base}.Snapshot(context.Background(), &App{Path: "proj"}, Version{}, out)
	}()
	go func() {
		src, err := os.OpenRoot(dir)
		if err != nil {
			done <- err
			return
		}
		defer src.Close()
		if err := copyProject(context.Background(), src, filepath.Join(t.TempDir(), "copy")); err == nil {
			done <- errors.New("copyProject accepted a FIFO")
			return
		}
		done <- nil
	}()
	for i := 0; i < 2; i++ {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("a FIFO in a local folder blocks")
		}
	}
	if _, err := os.Lstat(filepath.Join(out, "pipe")); err == nil {
		t.Fatal("FIFO copied into the snapshot")
	}
}
