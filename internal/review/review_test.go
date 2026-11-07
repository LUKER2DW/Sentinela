package review

import (
	"testing"
	"time"

	"github.com/LUKER2DW/sentinela/internal/store"
)

func TestGroup(t *testing.T) {
	t0 := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	ev := func(id int64, cam, typ string, from, to time.Duration, closed bool) store.Event {
		e := store.Event{ID: id, CameraID: cam, Type: typ, Start: t0.Add(from)}
		if closed {
			end := t0.Add(to)
			e.End = &end
		}
		return e
	}
	events := []store.Event{
		ev(1, "cam1", "motion", 0, 10*time.Second, true),
		ev(2, "cam1", "motion", 30*time.Second, 50*time.Second, true), // 20 s after: same item
		ev(3, "cam1", "line", 70*time.Second, 75*time.Second, true),   // 20 s after: same item, now an alert
		ev(4, "cam1", "motion", 10*time.Minute, 0, false),             // later, still open
		ev(5, "cam2", "motion", 5*time.Second, 6*time.Second, true),   // other camera
	}
	items := Group(events, Gap, func(e store.Event) bool { return e.ID == 2 })
	if len(items) != 3 {
		t.Fatalf("items = %+v", items)
	}
	open, cam2, first := items[0], items[1], items[2]
	if open.ID != 4 || open.End != nil || open.Severity != Detection {
		t.Errorf("open item = %+v", open)
	}
	if cam2.ID != 5 || cam2.CameraID != "cam2" {
		t.Errorf("cam2 item = %+v", cam2)
	}
	if first.ID != 1 || first.Events != 3 || first.Severity != Alert || first.SnapshotEvent != 2 ||
		!first.End.Equal(t0.Add(75*time.Second)) || len(first.Types) != 2 {
		t.Errorf("first item = %+v", first)
	}
	if got := Group(nil, Gap, nil); got == nil || len(got) != 0 {
		t.Errorf("empty = %#v", got)
	}
}
