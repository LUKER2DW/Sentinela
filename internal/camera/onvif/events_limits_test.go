package onvif

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LUKER2DW/sentinela/internal/store"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// A topic is kept between pulls while it is active, so an oversized one
// (a reply is up to 4 MiB) is dropped by the parser and by Handle: what
// the watcher keeps does not grow with the camera's strings.
func TestOversizedTopicsAreNotKept(t *testing.T) {
	ctx := context.Background()
	huge := "tns1:RuleEngine/CellMotionDetector/Motion/" + strings.Repeat("x", 1<<20)
	long := strings.Repeat("v", maxItemValueLen+1)
	reply := soap + `<PullMessagesResponse><NotificationMessage><Topic>` + huge + `</Topic><Message><Message><Data>` +
		`<SimpleItem Name="IsMotion" Value="true"/></Data></Message></Message></NotificationMessage>` +
		`<NotificationMessage><Topic>tns1:VideoSource/MotionAlarm</Topic><Message><Message>` +
		`<Source><SimpleItem Name="State" Value="false"/><SimpleItem Name="` + strings.Repeat("n", maxItemNameLen+1) + `" Value="v"/></Source>` +
		`<Data><SimpleItem Name="State" Value="true"/><SimpleItem Name="Long" Value="` + long + `"/></Data>` +
		`</Message></Message></NotificationMessage></PullMessagesResponse></s:Body></s:Envelope>`
	ns, err := parseNotifications([]byte(reply), pullLimit)
	if err != nil || len(ns) != 1 || ns[0].Topic != "tns1:VideoSource/MotionAlarm" {
		t.Fatalf("parsed %+v %v", ns, err)
	}
	if len(ns[0].Items) != 1 || ns[0].Items["State"] != "true" {
		t.Fatalf("items %v, want only State=true (Data wins, long items dropped)", ns[0].Items)
	}

	var items strings.Builder
	for i := range 3 * maxItems {
		fmt.Fprintf(&items, `<SimpleItem Name="k%d" Value="v"/>`, i)
	}
	many := soap + `<PullMessagesResponse><NotificationMessage><Topic>tns1:VideoSource/MotionAlarm</Topic><Message><Message><Data>` +
		items.String() + `</Data></Message></Message></NotificationMessage></PullMessagesResponse></s:Body></s:Envelope>`
	if ns, err := ParseNotifications([]byte(many)); err != nil || len(ns) != 1 || len(ns[0].Items) != maxItems {
		t.Fatalf("items kept from a long list: %v", ns)
	}

	w := &Watcher{CameraID: "cam1", Store: openStore(t)}
	for _, typ := range []string{"CellMotionDetector/Motion", "TamperDetector", "LineDetector", "FieldDetector"} {
		for i := range 20 {
			topic := fmt.Sprintf("tns1:RuleEngine/%s/%d/%s", typ, i, strings.Repeat("x", maxTopicLen))
			w.Handle(ctx, Notification{Topic: topic, Items: map[string]string{"State": "true"}}, time.Now())
		}
	}
	if len(w.topics) != 0 || len(w.open) != 0 {
		t.Fatalf("kept %d types, %d open events", len(w.topics), len(w.open))
	}
	edge := "tns1:" + strings.Repeat("a", maxTopicLen-len("tns1:")-len("/TamperDetector")) + "/TamperDetector"
	w.Handle(ctx, Notification{Topic: edge, Items: map[string]string{"IsTamper": "true"}}, time.Now())
	if len(w.topics["tamper"]) != 1 {
		t.Fatalf("a topic of exactly %d bytes was not kept", maxTopicLen)
	}
}

// Each event type has its own allowance: a camera that used up motion's
// still stores tamper, line and intrusion events.
func TestWatcherAllowancePerType(t *testing.T) {
	ctx := context.Background()
	ids := map[string][]int64{}
	w := &Watcher{CameraID: "cam1", Store: openStore(t), OpenBurst: 2, OpenEvery: time.Hour,
		Sink: func(_, typ string, id int64, active bool, _ time.Time) {
			if active {
				ids[typ] = append(ids[typ], id)
			}
		}}
	t0 := time.Now()
	flip := func(topic, key string, at time.Time) {
		w.Handle(ctx, Notification{Topic: topic, Items: map[string]string{key: "true"}}, at)
		w.Handle(ctx, Notification{Topic: topic, Items: map[string]string{key: "false"}}, at)
	}
	for i := range 10 {
		flip("tns1:RuleEngine/CellMotionDetector/Motion", "IsMotion", t0.Add(time.Duration(i)*time.Second))
	}
	flip("tns1:RuleEngine/TamperDetector/Tamper", "IsTamper", t0.Add(20*time.Second))
	flip("tns1:RuleEngine/LineDetector/Crossed", "State", t0.Add(21*time.Second))
	flip("tns1:RuleEngine/FieldDetector/ObjectsInside", "IsInside", t0.Add(22*time.Second))
	if m := ids["motion"]; len(m) != 10 || m[1] == 0 || m[2] != 0 {
		t.Fatalf("motion ids %v, want two stored then zeros", m)
	}
	for _, typ := range []string{"tamper", "line", "intrusion"} {
		if got := ids[typ]; len(got) != 1 || got[0] == 0 {
			t.Errorf("%s ids %v: starved by motion", typ, got)
		}
	}
}

// A pull that returns long after the type went inactive (it may wait up
// to its 10 s timeout) does not continue the old event: its grace is over.
func TestWatcherGraceEndsBeforeLateMessage(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	var got []bool
	w := &Watcher{CameraID: "cam1", Store: st, CloseGrace: 5 * time.Second,
		Sink: func(_, _ string, _ int64, active bool, _ time.Time) { got = append(got, active) }}
	m := func(v string) Notification {
		return Notification{Topic: "tns1:VideoSource/MotionAlarm", Items: map[string]string{"State": v}}
	}
	t0 := time.Now().Truncate(time.Second)
	w.Handle(ctx, m("true"), t0)
	w.Handle(ctx, m("false"), t0.Add(time.Second))
	w.Handle(ctx, m("true"), t0.Add(11*time.Second)) // the next pull, 10 s later
	if len(got) != 3 || !got[0] || got[1] || !got[2] {
		t.Fatalf("transitions = %v, want [true false true]", got)
	}
	ev, _ := st.EventsBetween(ctx, "cam1", t0.Add(-time.Minute), t0.Add(time.Minute), 10)
	if len(ev) != 2 {
		t.Fatalf("stored %d events, want 2", len(ev))
	}
	for _, e := range ev {
		if e.Start.Equal(t0) && (e.End == nil || !e.End.Equal(t0.Add(time.Second))) {
			t.Fatalf("first event %+v, want it ended when it went inactive", e)
		}
	}
}

// An event not stored for want of allowance is stored, from its start, on
// a later active message once the allowance refilled; the Sink hears it
// start again with its id. The refusal is logged once until the allowance
// is full again, not after every refill.
func TestWatcherStoresLimitedEventLater(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	var logs strings.Builder
	type call struct {
		id     int64
		active bool
		at     time.Time
	}
	var calls []call
	w := &Watcher{CameraID: "cam1", Store: st, OpenBurst: 2, OpenEvery: time.Minute, CloseGrace: 5 * time.Second,
		Log:  slog.New(slog.NewTextHandler(&logs, nil)),
		Sink: func(_, _ string, id int64, active bool, at time.Time) { calls = append(calls, call{id, active, at}) }}
	tamper := func(v string) Notification {
		return Notification{Topic: "tns1:RuleEngine/TamperDetector/Tamper", Items: map[string]string{"IsTamper": v}}
	}
	t0 := time.Now().Truncate(time.Second)
	// Two stored, the third not (tokens: 2, then 0 plus a sixth).
	for i := range 3 {
		at := t0.Add(time.Duration(i) * 10 * time.Second)
		w.Handle(ctx, tamper("true"), at)
		w.Handle(ctx, tamper("false"), at.Add(time.Second))
		w.settle(ctx, at.Add(7*time.Second))
	}
	w.Handle(ctx, tamper("true"), t0.Add(30*time.Second)) // not stored either
	if n := len(calls); n != 7 || calls[6].id != 0 || !calls[6].active {
		t.Fatalf("calls %+v", calls)
	}
	// Still active over a minute later: a token is back, and the event is
	// stored from its start.
	w.Handle(ctx, tamper("true"), t0.Add(90*time.Second))
	last := calls[len(calls)-1]
	if len(calls) != 8 || last.id == 0 || !last.active || !last.at.Equal(t0.Add(30*time.Second)) {
		t.Fatalf("after the refill: %+v", calls)
	}
	e, err := st.EventByID(ctx, last.id)
	if err != nil || !e.Start.Equal(t0.Add(30*time.Second)) || e.End != nil {
		t.Fatalf("stored %+v %v", e, err)
	}
	w.Handle(ctx, tamper("false"), t0.Add(100*time.Second))
	w.settle(ctx, t0.Add(110*time.Second))
	if e, _ := st.EventByID(ctx, last.id); e.End == nil || !e.End.Equal(t0.Add(100*time.Second)) {
		t.Fatalf("the stored event did not end: %+v", e)
	}
	// Refused again and again as single tokens come back: logged once.
	for i := range 6 {
		at := t0.Add(120*time.Second + time.Duration(i)*40*time.Second)
		w.Handle(ctx, tamper("true"), at)
		w.Handle(ctx, tamper("false"), at.Add(time.Second))
		w.settle(ctx, at.Add(7*time.Second))
	}
	if n := strings.Count(logs.String(), "too fast"); n != 1 {
		t.Fatalf("logged %d times:\n%s", n, logs.String())
	}
	// Full again (OpenBurst x OpenEvery of calm), then refused: logged anew.
	at := t0.Add(time.Hour)
	for range 3 {
		w.Handle(ctx, tamper("true"), at)
		w.Handle(ctx, tamper("false"), at.Add(time.Second))
		w.settle(ctx, at.Add(7*time.Second))
		at = at.Add(10 * time.Second)
	}
	if n := strings.Count(logs.String(), "too fast"); n != 2 {
		t.Fatalf("logged %d times after a calm spell:\n%s", n, logs.String())
	}
}
