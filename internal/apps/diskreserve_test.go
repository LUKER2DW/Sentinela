package apps

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
)

// roomyDisk is a system disk with plenty of room (tests).
func roomyDisk(string) (uint64, uint64, error) { return 60 << 30, 100 << 30, nil }

// Below the system disk's reserve every running service stops, none starts
// or deploys, and they come back once the disk has its margin again.
func TestDiskReserveStopsAndHolds(t *testing.T) {
	a, eng, _ := newTestAgent(t)
	var free atomic.Uint64
	free.Store(60 << 30)
	a.diskSpace = func(string) (uint64, uint64, error) { return free.Load(), 100 << 30, nil }
	v, err := a.Create(nodeApp())
	if err != nil {
		t.Fatal(err)
	}
	a.Enqueue(v.ID, "deploy")
	runQueued(t, a)
	if got, _ := a.viewOf(v.ID); got.Status.State != "running" {
		t.Fatalf("deploy: %+v", got.Status)
	}
	name := containerName(v.ID)
	stops := eng.called("stop " + name)

	// 10 % of 100 GiB is kept: 9 GiB free is below it.
	free.Store(9 << 30)
	a.Reconcile(context.Background())
	if eng.called("stop "+name) != stops+1 {
		t.Fatalf("service not stopped below the reserve: %v", eng.calls)
	}
	got, _ := a.viewOf(v.ID)
	if got.Desired != Running || !strings.Contains(got.Status.Error, "reserve") {
		t.Fatalf("held service: desired %s error %q", got.Desired, got.Status.Error)
	}
	starts := eng.called("start " + name)
	a.Reconcile(context.Background())
	if eng.called("start "+name) != starts {
		t.Fatal("started while the disk is below its reserve")
	}
	if err := a.canStart(a.snapshot(v.ID)); !errors.Is(err, errDiskReserve) {
		t.Fatalf("canStart below the reserve: %v", err)
	}
	a.Enqueue(v.ID, "deploy")
	runQueued(t, a)
	if got, _ := a.viewOf(v.ID); got.Status.Job.Phase != "failed" || !strings.Contains(got.Status.Job.Error, "reserve") {
		t.Fatalf("deploy below the reserve: %+v", got.Status.Job)
	}

	// Just above the reserve is not enough to come back (the margin).
	free.Store(10<<30 + 100<<20)
	a.Reconcile(context.Background())
	if eng.called("start "+name) != starts {
		t.Fatal("started without the margin")
	}
	free.Store(20 << 30)
	a.Reconcile(context.Background())
	if eng.called("start "+name) != starts+1 {
		t.Fatalf("not started once the disk had room: %v", eng.calls)
	}
}

// The reserve is a percentage of the disk, never under 1 GiB; an unreadable
// disk applies none.
func TestDiskReserveSize(t *testing.T) {
	a, _, _ := newTestAgent(t)
	a.Cfg.DiskReservePercent = 10
	a.diskSpace = func(string) (uint64, uint64, error) { return 1 << 30, 4 << 30, nil }
	if _, reserve, _, ok := a.diskState(); !ok || reserve != diskReserveFloor {
		t.Fatalf("small disk reserve %d", reserve)
	}
	if err := a.diskGate(false); err != nil {
		t.Fatalf("1 GiB free on a 4 GiB disk: %v", err)
	}
	a.diskSpace = func(string) (uint64, uint64, error) { return 0, 0, errors.New("no statfs") }
	if err := a.diskGate(false); err != nil {
		t.Fatalf("unknown disk: %v", err)
	}
}
