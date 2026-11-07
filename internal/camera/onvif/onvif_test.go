package onvif

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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
	w := &Watcher{CameraID: "cam1", Store: st, Sink: func(_, typ string, _ int64, active bool, _ time.Time) { got = append(got, active) }}
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

// The camera answers an empty PullMessages at once (it ignores the timeout):
// the watcher must not turn that into a request flood.
func TestWatcherPacesEmptyPulls(t *testing.T) {
	const env = `<?xml version="1.0"?><s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:tev="http://www.onvif.org/ver10/events/wsdl" xmlns:wsa="http://www.w3.org/2005/08/addressing"><s:Body>%s</s:Body></s:Envelope>`
	var pulls atomic.Int32
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch {
		case strings.Contains(string(body), "CreatePullPointSubscription"):
			fmt.Fprintf(w, env, `<tev:CreatePullPointSubscriptionResponse><tev:SubscriptionReference><wsa:Address>`+srv.URL+`/event_service/0</wsa:Address></tev:SubscriptionReference></tev:CreatePullPointSubscriptionResponse>`)
		case strings.Contains(string(body), "PullMessages"):
			pulls.Add(1)
			fmt.Fprintf(w, env, `<tev:PullMessagesResponse><tev:CurrentTime>2026-09-30T17:08:20Z</tev:CurrentTime></tev:PullMessagesResponse>`)
		default:
			fmt.Fprintf(w, env, "")
		}
	}))
	defer srv.Close()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	w := &Watcher{CameraID: "cam1", Store: st, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Client: &Client{Base: srv.URL, HTTP: srv.Client()}, MinPull: 100 * time.Millisecond}
	ctx, cancel := context.WithTimeout(context.Background(), 450*time.Millisecond)
	defer cancel()
	w.Run(ctx)
	if n := pulls.Load(); n < 2 || n > 6 {
		t.Fatalf("pulls in 450 ms at a 100 ms pace = %d, want 2..6", n)
	}
}

// The camera never frees a subscription and refuses new ones once its 32
// slots are used: the watcher must reuse the remembered pull point, and
// adopt a kept one when nothing is remembered.
func TestWatcherReusesPullPoints(t *testing.T) {
	const env = `<?xml version="1.0"?><s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:tev="http://www.onvif.org/ver10/events/wsdl" xmlns:wsa="http://www.w3.org/2005/08/addressing"><s:Body>%s</s:Body></s:Envelope>`
	var creates atomic.Int32
	var mu sync.Mutex
	pulled := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch {
		case strings.Contains(string(body), "CreatePullPointSubscription"):
			creates.Add(1)
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, env, `<s:Fault><s:Reason><s:Text>ResourceUnknownFault</s:Text></s:Reason></s:Fault>`)
		case strings.Contains(string(body), "PullMessages") && (r.URL.Path == "/event_service/7" || r.URL.Path == "/event_service/3"):
			mu.Lock()
			pulled[r.URL.Path]++
			mu.Unlock()
			fmt.Fprintf(w, env, `<tev:PullMessagesResponse/>`)
		default:
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, env, `<s:Fault><s:Reason><s:Text>ResourceUnknownFault</s:Text></s:Reason></s:Fault>`)
		}
	}))
	defer srv.Close()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	run := func() {
		w := &Watcher{CameraID: "cam1", Store: st, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
			Client: &Client{Base: srv.URL, HTTP: srv.Client()}, MinPull: 50 * time.Millisecond}
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		w.Run(ctx)
	}
	run() // nothing remembered: Create refused, the highest live slot is adopted
	mu.Lock()
	n7 := pulled["/event_service/7"]
	mu.Unlock()
	if n7 < 2 || creates.Load() != 1 {
		t.Fatalf("first run: pulls %v, creates %d", pulled, creates.Load())
	}
	if got := st.Setting(context.Background(), "onvif.pullpoint.cam1", ""); got != srv.URL+"/event_service/7" {
		t.Fatalf("remembered %q", got)
	}
	run() // remembered: reused without asking for a new subscription
	if creates.Load() != 1 {
		t.Fatalf("second run created a subscription (creates %d)", creates.Load())
	}
}

func TestParseSystemTime(t *testing.T) {
	// Trimmed response from the XMEye camera (docs/CAMERA.md §5.1).
	data := []byte(`<?xml version="1.0"?><s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:tds="http://www.onvif.org/ver10/device/wsdl" xmlns:tt="http://www.onvif.org/ver10/schema"><s:Body><tds:GetSystemDateAndTimeResponse><tds:SystemDateAndTime><tt:DateTimeType>Manual</tt:DateTimeType><tt:UTCDateTime><tt:Time><tt:Hour>0</tt:Hour><tt:Minute>3</tt:Minute><tt:Second>37</tt:Second></tt:Time><tt:Date><tt:Year>2026</tt:Year><tt:Month>9</tt:Month><tt:Day>29</tt:Day></tt:Date></tt:UTCDateTime><tt:LocalDateTime><tt:Time><tt:Hour>21</tt:Hour><tt:Minute>3</tt:Minute><tt:Second>37</tt:Second></tt:Time><tt:Date><tt:Year>2026</tt:Year><tt:Month>9</tt:Month><tt:Day>28</tt:Day></tt:Date></tt:LocalDateTime></tds:SystemDateAndTime></tds:GetSystemDateAndTimeResponse></s:Body></s:Envelope>`)
	got, err := ParseSystemTime(data)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 9, 29, 0, 3, 37, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if _, err := ParseSystemTime([]byte(`<s:Envelope xmlns:s="x"><s:Body/></s:Envelope>`)); err == nil {
		t.Fatal("want error for a response without UTCDateTime")
	}
}

func TestRebaseSubscriptionAddress(t *testing.T) {
	c := NewClient("192.168.1.20", 8899, "u", "p")
	tests := []struct{ in, want string }{
		// The camera reports its unused wired static address.
		{"http://192.168.1.10:8899/event_service/61", "http://192.168.1.20:8899/event_service/61"},
		{"http://192.168.1.10/event_service/7?x=1", "http://192.168.1.20:8899/event_service/7?x=1"},
		{"/event_service/3", "http://192.168.1.20:8899/event_service/3"},
		{"http://192.168.1.20:8899/event_service/2", "http://192.168.1.20:8899/event_service/2"},
	}
	for _, tt := range tests {
		if got := c.rebase(tt.in); got != tt.want {
			t.Errorf("rebase(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// While the camera moves under PTZ its own motion opens no event; tamper
// and other types still do.
func TestWatcherQuietDuringPTZ(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	quiet := true
	var got []string
	w := &Watcher{CameraID: "cam1", Store: st, Quiet: func(string) bool { return quiet },
		Sink: func(_, typ string, _ int64, active bool, _ time.Time) {
			if active {
				got = append(got, typ)
			}
		}}
	now := time.Now()
	cell := Notification{Topic: "tns1:RuleEngine/CellMotionDetector/Motion", Items: map[string]string{"IsMotion": "true"}}
	w.Handle(ctx, cell, now)
	w.Handle(ctx, Notification{Topic: "tns1:VideoSource/TamperDetector", Items: map[string]string{"IsTamper": "true"}}, now)
	cell.Items["IsMotion"] = "false"
	w.Handle(ctx, cell, now.Add(time.Second))
	quiet = false
	cell.Items["IsMotion"] = "true"
	w.Handle(ctx, cell, now.Add(5*time.Second))
	if len(got) != 2 || got[0] != "tamper" || got[1] != "motion" {
		t.Fatalf("opened = %v, want [tamper motion]", got)
	}
}
