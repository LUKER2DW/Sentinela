// Package review groups camera events into review items (Frigate-style):
// events of one camera that follow each other within a short gap become one
// item, with a severity of Alert or Detection.
package review

import (
	"slices"
	"time"

	"github.com/LUKER2DW/sentinela/internal/store"
)

// Gap is how close two events must be to join the same item.
const Gap = 30 * time.Second

// Severities.
const (
	Alert     = "alert"
	Detection = "detection"
)

// alertTypes need attention even when someone is home: a person crossing a
// line or entering an area, or the camera being covered or moved.
var alertTypes = map[string]bool{"intrusion": true, "line": true, "tamper": true, "human": true}

// AlertTypes lists the event types that make an item an Alert.
func AlertTypes() []string {
	out := make([]string, 0, len(alertTypes))
	for t := range alertTypes {
		out = append(out, t)
	}
	slices.Sort(out)
	return out
}

// Severity of one event type.
func Severity(typ string) string {
	if alertTypes[typ] {
		return Alert
	}
	return Detection
}

// Item is a group of events. ID is the first event's id (stable while the
// item grows, used for the seen state).
type Item struct {
	ID       int64      `json:"id"`
	CameraID string     `json:"camera_id"`
	Start    time.Time  `json:"start"`
	End      *time.Time `json:"end,omitempty"` // nil while an event is still open
	Types    []string   `json:"types"`
	Severity string     `json:"severity"`
	Events   int        `json:"events"`
	// SnapshotEvent is the first event of the item with a picture (0 = none).
	SnapshotEvent int64 `json:"snapshot_event,omitempty"`
	Seen          bool  `json:"seen"`
}

// Group builds items from events of any cameras, newest item first.
// hasSnapshot reports whether an event has a picture (may be nil).
func Group(events []store.Event, gap time.Duration, hasSnapshot func(store.Event) bool) []Item {
	sorted := slices.Clone(events)
	slices.SortFunc(sorted, func(a, b store.Event) int {
		if a.CameraID != b.CameraID {
			if a.CameraID < b.CameraID {
				return -1
			}
			return 1
		}
		return a.Start.Compare(b.Start)
	})
	var out []Item
	var cur *Item
	var curEnd time.Time // latest end seen; far future while open
	open := false
	for _, e := range sorted {
		if cur != nil && e.CameraID == cur.CameraID && !e.Start.After(curEnd.Add(gap)) {
			cur.Events++
		} else {
			out = append(out, Item{ID: e.ID, CameraID: e.CameraID, Start: e.Start, Severity: Detection})
			cur = &out[len(out)-1]
			cur.Events = 1
			curEnd, open = e.Start, false
		}
		if !slices.Contains(cur.Types, e.Type) {
			cur.Types = append(cur.Types, e.Type)
		}
		if Severity(e.Type) == Alert {
			cur.Severity = Alert
		}
		if cur.SnapshotEvent == 0 && hasSnapshot != nil && hasSnapshot(e) {
			cur.SnapshotEvent = e.ID
		}
		if e.End == nil {
			open = true
			curEnd = e.Start
		} else if e.End.After(curEnd) {
			curEnd = *e.End
		}
		if open {
			cur.End = nil
		} else {
			end := curEnd
			cur.End = &end
		}
	}
	slices.SortFunc(out, func(a, b Item) int { return b.Start.Compare(a.Start) })
	if out == nil {
		out = []Item{}
	}
	return out
}
