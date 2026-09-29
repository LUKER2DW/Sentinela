package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestMigrateIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	for i := 0; i < 2; i++ {
		s, err := Open(context.Background(), path)
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		s.Close()
	}
}

func TestFirstUserOnlyOnce(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if _, err := s.CreateFirstUser(ctx, "admin", "h"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateFirstUser(ctx, "evil", "h"); err == nil {
		t.Fatal("second setup must fail")
	}
	u, err := s.UserByName(ctx, "ADMIN")
	if err != nil || u.Role != "admin" {
		t.Fatalf("user=%v err=%v", u, err)
	}
}

func TestSegmentsOverlap(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	base := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		st := base.Add(time.Duration(i) * 5 * time.Minute)
		if err := s.UpsertSegment(ctx, Segment{CameraID: "cam1", Start: st, End: st.Add(5 * time.Minute), Path: st.Format("150405") + ".mp4", Bytes: 100}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.SegmentsBetween(ctx, "cam1", base.Add(4*time.Minute), base.Add(6*time.Minute))
	if err != nil || len(got) != 2 {
		t.Fatalf("got %d segments, err=%v", len(got), err)
	}
	// Upsert updates, not duplicates.
	if err := s.UpsertSegment(ctx, Segment{CameraID: "cam1", Start: base, End: base.Add(6 * time.Minute), Path: base.Format("150405") + ".mp4", Bytes: 200}); err != nil {
		t.Fatal(err)
	}
	st, err := s.Stats(ctx)
	if err != nil || len(st) != 1 || st[0].Count != 3 || st[0].Bytes != 400 {
		t.Fatalf("stats=%+v err=%v", st, err)
	}
}

func TestGapsSince(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	t0 := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	add := func(path string, from, to time.Duration) {
		t.Helper()
		if err := s.UpsertSegment(ctx, Segment{CameraID: "cam1", Start: t0.Add(from), End: t0.Add(to), Path: path, Bytes: 1}); err != nil {
			t.Fatal(err)
		}
	}
	add("a", 0, 300*time.Second)
	add("b", 301*time.Second, 600*time.Second)  // 1 s: segment boundary, not a gap
	add("c", 550*time.Second, 900*time.Second)  // overlaps b
	add("d", 960*time.Second, 1200*time.Second) // 60 s hole after c
	gaps, err := s.GapsSince(ctx, "cam1", t0.Add(-time.Hour), 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(gaps) != 1 || !gaps[0].From.Equal(t0.Add(900*time.Second)) || !gaps[0].To.Equal(t0.Add(960*time.Second)) {
		t.Fatalf("gaps=%+v", gaps)
	}
	st := RecorderStatus{CameraID: "cam1", State: "recording", Since: t0, Heartbeat: t0, Restarts: 7, Restarts1h: 3}
	if err := s.PutRecorderStatus(ctx, st); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.ListRecorderStatus(ctx); len(got) != 1 || got[0].Restarts1h != 3 {
		t.Fatalf("status=%+v", got)
	}
}

func TestEventsLifecycle(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	now := time.Now().UTC()
	id, err := s.OpenEvent(ctx, "cam1", "motion", now.Add(-time.Hour), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CloseDanglingEvents(ctx, "cam1", time.Minute); err != nil {
		t.Fatal(err)
	}
	ev, err := s.EventsBetween(ctx, "", now.Add(-2*time.Hour), now, 10)
	if err != nil || len(ev) != 1 || ev[0].ID != id || ev[0].End == nil {
		t.Fatalf("events=%+v err=%v", ev, err)
	}
	if got := ev[0].End.Sub(ev[0].Start); got != time.Minute {
		t.Fatalf("dangling close length %v", got)
	}
}

func TestConcurrentOpenOfFreshDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "race.db")
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		go func() {
			s, err := Open(context.Background(), path)
			if err == nil {
				s.Close()
			}
			errs <- err
		}()
	}
	for i := 0; i < 4; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent open: %v", err)
		}
	}
}
