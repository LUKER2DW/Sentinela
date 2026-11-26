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

// The firewall marker is never read through a link or from a FIFO.
func TestFirewallMarkerRefusesLinksAndFIFOs(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	if err := os.WriteFile(real, []byte("boot b-1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := readFirewallMarker(filepath.Join(dir, "link")); err == nil {
		t.Fatal("marker read through a link")
	}
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe"), 0o600); err != nil {
		t.Skip("mkfifo unavailable:", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := readFirewallMarker(filepath.Join(dir, "pipe"))
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, errNotRegular) {
			t.Fatalf("FIFO marker: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reading a FIFO marker blocks")
	}
}
