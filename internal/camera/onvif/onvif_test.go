package onvif

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/LUKER2DW/sentinela/internal/store"
)

const pullResp = `<?xml version="1.0"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:tev="http://www.onvif.org/ver10/events/wsdl"
 xmlns:wsnt="http://docs.oasis-open.org/wsn/b-2" xmlns:tt="http://www.onvif.org/ver10/schema">
<s:Body><tev:PullMessagesResponse>
<tev:CurrentTime>2026-09-28T17:00:00Z</tev:CurrentTime>
<wsnt:NotificationMessage>
 <wsnt:Topic Dialect="http://www.onvif.org/ver10/tev/topicExpression/ConcreteSet">tns1:RuleEngine/CellMotionDetector/Motion</wsnt:Topic>
 <wsnt:Message><tt:Message UtcTime="2026-09-28T17:00:00Z" PropertyOperation="Changed">
  <tt:Source><tt:SimpleItem Name="VideoSourceConfigurationToken" Value="000"/></tt:Source>
  <tt:Data><tt:SimpleItem Name="IsMotion" Value="true"/></tt:Data>
 </tt:Message></wsnt:Message>
</wsnt:NotificationMessage>
<wsnt:NotificationMessage>
 <wsnt:Topic>tns1:VideoSource/MotionAlarm</wsnt:Topic>
 <wsnt:Message><tt:Message><tt:Data><tt:SimpleItem Name="State" Value="false"/></tt:Data></tt:Message></wsnt:Message>
</wsnt:NotificationMessage>
<wsnt:NotificationMessage>
 <wsnt:Topic>tns1:Media/ProfileChanged</wsnt:Topic>
 <wsnt:Message><tt:Message><tt:Data><tt:SimpleItem Name="Token" Value="000"/></tt:Data></tt:Message></wsnt:Message>
</wsnt:NotificationMessage>
</tev:PullMessagesResponse></s:Body></s:Envelope>`

func TestParseAndClassify(t *testing.T) {
	ns, err := ParseNotifications([]byte(pullResp))
	if err != nil || len(ns) != 3 {
		t.Fatalf("n=%d err=%v", len(ns), err)
	}
	cases := []struct {
		typ    string
		active bool
		ok     bool
	}{{"motion", true, true}, {"motion", false, true}, {"", false, false}}
	for i, c := range cases {
		typ, active, ok := Classify(ns[i])
		if typ != c.typ || active != c.active || ok != c.ok {
			t.Errorf("msg %d: got (%s,%v,%v) want %+v", i, typ, active, ok, c)
		}
	}
}

func TestWatcherMergesMotionTopics(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var got []bool
	w := &Watcher{CameraID: "cam1", Store: st, Sink: func(_, typ string, active bool, _ time.Time) { got = append(got, active) }}
	now := time.Now()
	cell := Notification{Topic: "tns1:RuleEngine/CellMotionDetector/Motion", Items: map[string]string{"IsMotion": "true"}}
	alarm := Notification{Topic: "tns1:VideoSource/MotionAlarm", Items: map[string]string{"State": "true"}}
	w.Handle(ctx, cell, now)
	w.Handle(ctx, alarm, now)
	cell.Items["IsMotion"] = "false"
	w.Handle(ctx, cell, now.Add(time.Second)) // alarm still active: stays open
	alarm.Items["State"] = "false"
	w.Handle(ctx, alarm, now.Add(2*time.Second))
	if len(got) != 2 || !got[0] || got[1] {
		t.Fatalf("transitions = %v, want [true false]", got)
	}
	ev, _ := st.EventsBetween(ctx, "cam1", now.Add(-time.Minute), now.Add(time.Minute), 10)
	if len(ev) != 1 || ev[0].End == nil || ev[0].End.Sub(ev[0].Start) < 2*time.Second-time.Millisecond {
		t.Fatalf("events = %+v", ev)
	}
}
