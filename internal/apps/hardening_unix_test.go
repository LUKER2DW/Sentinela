//go:build !windows

package apps

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A FIFO a service planted in its data folder must not block the agent (the
// editor holds fileMu while it reads).
func TestFIFOInDataDoesNotBlock(t *testing.T) {
	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe"), 0o600); err != nil {
		t.Skip("mkfifo unavailable:", err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	done := make(chan error, 1)
	go func() {
		_, err := readProjectText(root, "pipe")
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a FIFO was read as a text file")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reading a FIFO blocks")
	}
	f, _, err := func() (*os.File, os.FileInfo, error) { return openRegular(root, "pipe") }()
	if f != nil {
		f.Close()
	}
	if !errors.Is(err, errNotRegular) {
		t.Fatalf("openRegular: %v", err)
	}
}
