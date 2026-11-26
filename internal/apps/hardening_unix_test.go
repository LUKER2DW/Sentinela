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

// A FIFO a service planted in its release (which it may bind writable) where
// the agent once wrote the Compose input must not stall the agent: the input
// is the agent's own file now, so the next `up` goes on.
func TestComposeFIFOInReleaseDoesNotBlock(t *testing.T) {
	a, eng, src := newTestAgent(t)
	got := deployCompose(t, a, eng, src, App{Name: "Site"}, func(string) string { return `{"services":{"web":{"image":"nginx"}}}` })
	if got.Status.Job.Phase != "done" {
		t.Fatalf("deploy: %+v", got.Status.Job)
	}
	app := a.snapshot(got.ID)
	work := a.workDir(app, app.Deployed)
	for _, name := range []string{".sentinela-input.yaml", composeInputFile} {
		if err := syscall.Mkfifo(filepath.Join(work, name), 0o600); err != nil {
			t.Skip("mkfifo unavailable:", err)
		}
	}
	done := make(chan error, 1)
	go func() { done <- a.recreate(context.Background(), got.ID, func(string, ...any) {}) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a FIFO in the release stalls the agent")
	}
}
