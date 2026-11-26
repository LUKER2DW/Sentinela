package store

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"
)

// An install from before camera_ids: ids left in recordings, events and
// grants are known, and the ones not configured are retired at once.
func TestSyncCameraIDsSeedsAndRetires(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	at := time.Now().UTC().Add(-time.Hour)
	if err := s.UpsertSegment(ctx, Segment{CameraID: "old", Start: at, End: at.Add(time.Minute), Path: "old/a.mp4", Bytes: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.OpenEvent(ctx, "gone", "motion", at, ""); err != nil {
		t.Fatal(err)
	}
	users := map[string][]string{"a": {"cam1", "old"}, "b": {"granted"}, "c": {"cam1"}, "d": nil, "e": {"old"}}
	ids := map[string]int64{}
	for name, cams := range users {
		id, err := s.CreateUser(ctx, name, "x", "viewer")
		if err != nil {
			t.Fatal(err)
		}
		ids[name] = id
		if err := s.UpdateUserCameras(ctx, id, cams); err != nil {
			t.Fatal(err)
		}
	}
	keys := func(id string) []string { return []string{"k." + id} }
	for _, id := range []string{"old", "cam1"} {
		_ = s.SetSetting(ctx, "k."+id, "v")
	}

	retired, now, _, err := s.SyncCameraIDs(ctx, []string{"cam1"}, keys)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"gone", "granted", "old"}
	if !slices.Equal(retired, want) || !slices.Equal(now, want) {
		t.Fatalf("retired %v, now %v; want %v", retired, now, want)
	}
	for name, cams := range map[string][]string{"a": {"cam1"}, "b": {"granted"}, "c": {"cam1"}, "d": {}, "e": {"old"}} {
		u, _ := s.UserByID(ctx, ids[name])
		if !slices.Equal(u.Cameras, cams) {
			t.Errorf("%s: cameras %v, want %v", name, u.Cameras, cams)
		}
	}
	if s.Setting(ctx, "k.old", "gone") != "gone" || s.Setting(ctx, "k.cam1", "") != "v" {
		t.Error("settings: the removed camera's should go, the configured one's stay")
	}
	if got, _ := s.RetiredCameraIDs(ctx); !slices.Equal(got, want) {
		t.Fatalf("RetiredCameraIDs = %v", got)
	}

	if known, err := s.CameraIDKnown(ctx, "old"); err != nil || !known {
		t.Fatalf("a retired id must be known: %v %v", known, err)
	}
	if known, _ := s.CameraIDKnown(ctx, "cam9"); known {
		t.Fatal("an id never used is free")
	}

	// Next start: nothing newly retired.
	if _, now, revived, err := s.SyncCameraIDs(ctx, []string{"cam1"}, keys); err != nil || len(now)+len(revived) != 0 {
		t.Fatalf("second sync: now %v revived %v err %v", now, revived, err)
	}
}

// A retired id configured again by hand is a new camera: the old one's
// recordings, events and grants move to an archive id, its settings go,
// and what the new camera recorded already stays with it.
func TestSyncCameraIDsRevivedIsNewCamera(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	keys := func(id string) []string { return []string{"k." + id} }
	old := time.Now().UTC().Add(-time.Hour)
	if err := s.UpsertSegment(ctx, Segment{CameraID: "cam2", Start: old, End: old.Add(time.Minute), Path: "cam2/a.mp4", Bytes: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.OpenEvent(ctx, "cam2", "motion", old, ""); err != nil {
		t.Fatal(err)
	}
	only, _ := s.CreateUser(ctx, "only", "x", "viewer")
	both, _ := s.CreateUser(ctx, "both", "x", "viewer")
	_ = s.UpdateUserCameras(ctx, only, []string{"cam2"})
	_ = s.UpdateUserCameras(ctx, both, []string{"cam1", "cam2"})
	if _, _, _, err := s.SyncCameraIDs(ctx, []string{"cam1", "cam2"}, keys); err != nil {
		t.Fatal(err)
	}
	if _, now, _, err := s.SyncCameraIDs(ctx, []string{"cam1"}, keys); err != nil || !slices.Equal(now, []string{"cam2"}) {
		t.Fatalf("remove cam2: now %v err %v", now, err)
	}
	var removedAt string
	if err := s.db.QueryRowContext(ctx, `SELECT removed_at FROM camera_ids WHERE id='cam2'`).Scan(&removedAt); err != nil {
		t.Fatal(err)
	}
	archive := ArchiveID("cam2", removedAt)
	// The recorder of the new cam2 may write before the API syncs; the
	// grant left behind and a setting written meanwhile must not apply.
	fresh := time.Now().UTC().Add(time.Minute)
	if err := s.UpsertSegment(ctx, Segment{CameraID: "cam2", Start: fresh, End: fresh.Add(time.Minute), Path: "cam2/b.mp4", Bytes: 1}); err != nil {
		t.Fatal(err)
	}
	_ = s.SetSetting(ctx, "k.cam2", "old camera's")

	retired, now, revived, err := s.SyncCameraIDs(ctx, []string{"cam1", "cam2"}, keys)
	if err != nil || len(now) != 0 || !slices.Equal(revived, []string{"cam2"}) || !slices.Equal(retired, []string{archive}) {
		t.Fatalf("revive: retired %v now %v revived %v err %v", retired, now, revived, err)
	}
	if !strings.HasPrefix(archive, "cam2"+ArchiveSep) {
		t.Fatalf("archive id %q", archive)
	}
	segs, _ := s.SegmentsBetween(ctx, "cam2", old.Add(-time.Hour), fresh.Add(time.Hour))
	if len(segs) != 1 || segs[0].Path != "cam2/b.mp4" {
		t.Fatalf("new cam2 timeline: %+v", segs)
	}
	if segs, _ := s.SegmentsBetween(ctx, archive, old.Add(-time.Hour), fresh.Add(time.Hour)); len(segs) != 1 {
		t.Fatalf("archived recordings: %+v", segs)
	}
	if evs, _ := s.EventsBetween(ctx, "cam2", old.Add(-time.Hour), fresh.Add(time.Hour), 10); len(evs) != 0 {
		t.Fatalf("old events on the new camera: %+v", evs)
	}
	for id, want := range map[int64][]string{only: {archive}, both: {"cam1"}} {
		u, _ := s.UserByID(ctx, id)
		if !slices.Equal(u.Cameras, want) {
			t.Errorf("user %d cameras %v, want %v", id, u.Cameras, want)
		}
	}
	if s.Setting(ctx, "k.cam2", "none") != "none" {
		t.Error("the old camera's settings must go")
	}
	// A second start changes nothing; removing it again retires it again.
	if _, now, revived, err := s.SyncCameraIDs(ctx, []string{"cam1", "cam2"}, keys); err != nil || len(now)+len(revived) != 0 {
		t.Fatalf("after revival: now %v revived %v err %v", now, revived, err)
	}
	if _, now, _, _ := s.SyncCameraIDs(ctx, []string{"cam1"}, keys); !slices.Equal(now, []string{"cam2"}) {
		t.Fatalf("removed again: now %v", now)
	}
}
