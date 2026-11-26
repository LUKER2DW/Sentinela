package onvif

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LUKER2DW/sentinela/internal/store"
)

// hostPort splits an httptest URL for NewClient/NewPTZ.
func hostPort(t *testing.T, raw string) (string, int) {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	h, p, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatal(err)
	}
	n, _ := strconv.Atoi(p)
	return h, n
}

func TestOnBase(t *testing.T) {
	c := NewClient("192.168.1.20", 8899, "u", "p")
	tests := []struct {
		in   string
		want bool
	}{
		{"http://192.168.1.20:8899/event_service/7", true},
		{"HTTP://192.168.1.20:8899/event_service/7?x=1", true},
		{"http://192.168.1.21:8899/event_service/7", false}, // another host
		{"http://192.168.1.20:80/event_service/7", false},   // another port
		{"http://192.168.1.20/event_service/7", false},      // implicit port
		{"https://192.168.1.20:8899/event_service/7", false},
		{"http://x@192.168.1.20:8899/event_service/7", false},
		{"/event_service/7", false},
		{"", false},
		{"::", false},
	}
	for _, tt := range tests {
		if got := c.onBase(tt.in); got != tt.want {
			t.Errorf("onBase(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

// A pull point remembered for a previous camera address is never pulled
// (it would get the current camera's digest and could speak for it): the
// watcher subscribes on the configured address and remembers that one.
func TestWatcherForgetsPullPointOfAnotherAddress(t *testing.T) {
	const env = `<?xml version="1.0"?><s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:tev="http://www.onvif.org/ver10/events/wsdl" xmlns:wsa="http://www.w3.org/2005/08/addressing"><s:Body>%s</s:Body></s:Envelope>`
	var old atomic.Int32
	oldSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		old.Add(1)
		fmt.Fprintf(w, env, `<tev:PullMessagesResponse/>`)
	}))
	defer oldSrv.Close()
	var creates atomic.Int32
	var newSrv *httptest.Server
	newSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch {
		case strings.Contains(string(body), "CreatePullPointSubscription"):
			creates.Add(1)
			fmt.Fprintf(w, env, `<tev:CreatePullPointSubscriptionResponse><tev:SubscriptionReference><wsa:Address>`+newSrv.URL+`/event_service/1</wsa:Address></tev:SubscriptionReference></tev:CreatePullPointSubscriptionResponse>`)
		case strings.Contains(string(body), "PullMessages"):
			fmt.Fprintf(w, env, `<tev:PullMessagesResponse/>`)
		default:
			fmt.Fprintf(w, env, "")
		}
	}))
	defer newSrv.Close()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_ = st.SetSetting(context.Background(), "onvif.pullpoint.cam1", oldSrv.URL+"/event_service/5")
	w := &Watcher{CameraID: "cam1", Store: st, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Client: &Client{Base: newSrv.URL, User: "u2", Password: "p2", HTTP: newSrv.Client()}, MinPull: 50 * time.Millisecond}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	w.Run(ctx)
	if n := old.Load(); n != 0 {
		t.Fatalf("the previous address received %d requests", n)
	}
	if creates.Load() != 1 {
		t.Fatalf("subscriptions on the configured address = %d, want 1", creates.Load())
	}
	if got := st.Setting(context.Background(), "onvif.pullpoint.cam1", ""); got != newSrv.URL+"/event_service/1" {
		t.Fatalf("remembered %q", got)
	}
}

// The camera chooses its topics: inactive ones are not kept at all and
// active ones are capped per type, so the watcher's state stays small and
// each message costs the same whatever was sent before.
func TestWatcherTopicStateBounded(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	w := &Watcher{CameraID: "cam1", Store: st}
	now := time.Now()
	for i := range 5000 {
		w.Handle(ctx, Notification{Topic: fmt.Sprintf("tns1:RuleEngine/CellMotionDetector/Motion/%d", i), Items: map[string]string{"IsMotion": "false"}}, now)
	}
	if n := len(w.topics); n != 0 {
		t.Fatalf("inactive topics kept: %d types", n)
	}
	for i := range 500 {
		w.Handle(ctx, Notification{Topic: fmt.Sprintf("tns1:RuleEngine/CellMotionDetector/Motion/%d", i), Items: map[string]string{"IsMotion": "true"}}, now)
	}
	if n := len(w.topics["motion"]); n != maxActiveTopics {
		t.Fatalf("active motion topics kept = %d, want %d", n, maxActiveTopics)
	}
	if len(w.open) != 1 {
		t.Fatalf("open = %+v", w.open)
	}
}

func pullReply(n int, value func(i int) string) string {
	var b strings.Builder
	b.WriteString(soap + `<PullMessagesResponse>`)
	for i := range n {
		fmt.Fprintf(&b, `<NotificationMessage><Topic>tns1:RuleEngine/CellMotionDetector/Motion</Topic><Message><Message><Data><SimpleItem Name="IsMotion" Value="%s"/></Data></Message></Message></NotificationMessage>`, value(i))
	}
	b.WriteString(`</PullMessagesResponse></s:Body></s:Envelope>`)
	return b.String()
}

// MessageLimit is only a request: a longer reply is cut to its last
// pullLimit messages (the newest state).
func TestParseNotificationsLimit(t *testing.T) {
	data := []byte(pullReply(1000, func(i int) string { return strconv.Itoa(i) }))
	all, err := ParseNotifications(data)
	if err != nil || len(all) != 1000 {
		t.Fatalf("all: %d %v", len(all), err)
	}
	got, err := parseNotifications(data, pullLimit)
	if err != nil || len(got) != pullLimit {
		t.Fatalf("limited: %d %v", len(got), err)
	}
	if got[0].Items["IsMotion"] != "968" || got[pullLimit-1].Items["IsMotion"] != "999" {
		t.Fatalf("kept %s..%s, want the last %d", got[0].Items["IsMotion"], got[pullLimit-1].Items["IsMotion"], pullLimit)
	}
	for _, bad := range []string{"", "<", soap + `<PullMessagesResponse>`} {
		if _, err := parseNotifications([]byte(bad), pullLimit); err == nil {
			t.Errorf("%q parsed without error", bad)
		}
	}
}

// A camera answering every pull with a full batch of flips on one topic
// is still pulled at the pace, and its flips do not each store an event.
func TestWatcherFloodOfTransitions(t *testing.T) {
	for _, n := range []int{pullLimit - 1, pullLimit, 500} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			const env = `<?xml version="1.0"?><s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:wsa="http://www.w3.org/2005/08/addressing"><s:Body>%s</s:Body></s:Envelope>`
			reply := pullReply(n, func(i int) string { return strconv.FormatBool(i%2 == 0) })
			var pulls atomic.Int32
			var srv *httptest.Server
			srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				switch {
				case strings.Contains(string(body), "CreatePullPointSubscription"):
					fmt.Fprintf(w, env, `<CreatePullPointSubscriptionResponse><SubscriptionReference><wsa:Address>`+srv.URL+`/event_service/0</wsa:Address></SubscriptionReference></CreatePullPointSubscriptionResponse>`)
				case strings.Contains(string(body), "PullMessages"):
					pulls.Add(1)
					fmt.Fprint(w, reply)
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
			var opened atomic.Int32
			w := &Watcher{CameraID: "cam1", Store: st, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
				Client: &Client{Base: srv.URL, HTTP: srv.Client()}, MinPull: 100 * time.Millisecond,
				Sink: func(_, _ string, _ int64, active bool, _ time.Time) {
					if active {
						opened.Add(1)
					}
				}}
			start := time.Now()
			ctx, cancel := context.WithTimeout(context.Background(), 650*time.Millisecond)
			defer cancel()
			w.Run(ctx)
			if p := pulls.Load(); p > 8 {
				t.Fatalf("%d pulls in 650 ms at a 100 ms pace", p)
			}
			ev, _ := st.EventsBetween(context.Background(), "cam1", start.Add(-time.Minute), time.Now().Add(time.Minute), 100)
			if len(ev) != 1 || opened.Load() != 1 {
				t.Fatalf("stored %d events and opened %d for one flapping topic, want 1", len(ev), opened.Load())
			}
		})
	}
}

// A re-trigger within the grace continues the event; the end stored is
// when the type last went inactive.
func TestWatcherCloseGrace(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var got []bool
	w := &Watcher{CameraID: "cam1", Store: st, CloseGrace: 5 * time.Second,
		Sink: func(_, _ string, _ int64, active bool, _ time.Time) { got = append(got, active) }}
	m := func(v string) Notification {
		return Notification{Topic: "tns1:VideoSource/MotionAlarm", Items: map[string]string{"State": v}}
	}
	t0 := time.Now().Truncate(time.Second)
	w.Handle(ctx, m("true"), t0)
	w.Handle(ctx, m("false"), t0.Add(2*time.Second))
	w.Handle(ctx, m("true"), t0.Add(4*time.Second)) // within the grace: same event
	w.Handle(ctx, m("false"), t0.Add(6*time.Second))
	w.settle(ctx, t0.Add(10*time.Second))
	if len(got) != 1 {
		t.Fatalf("transitions before the grace ended: %v", got)
	}
	w.settle(ctx, t0.Add(11*time.Second))
	if len(got) != 2 || !got[0] || got[1] {
		t.Fatalf("transitions = %v, want [true false]", got)
	}
	ev, _ := st.EventsBetween(ctx, "cam1", t0.Add(-time.Minute), t0.Add(time.Minute), 10)
	if len(ev) != 1 || ev[0].End == nil || !ev[0].End.Equal(t0.Add(6*time.Second)) {
		t.Fatalf("events = %+v", ev)
	}
}

// Past the allowance, transitions reach the Sink with id 0 (live state,
// the detector's motion) and nothing is stored.
func TestWatcherOpenAllowance(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var ids []int64
	w := &Watcher{CameraID: "cam1", Store: st, OpenBurst: 3, OpenEvery: time.Minute,
		Sink: func(_, _ string, id int64, active bool, _ time.Time) {
			if active {
				ids = append(ids, id)
			}
		}}
	t0 := time.Now()
	tamper := func(v string) Notification {
		return Notification{Topic: "tns1:VideoSource/TamperDetector", Items: map[string]string{"IsTamper": v}}
	}
	for i := range 10 {
		at := t0.Add(time.Duration(i) * time.Second)
		w.Handle(ctx, tamper("true"), at)
		w.Handle(ctx, tamper("false"), at.Add(time.Millisecond))
	}
	w.Handle(ctx, tamper("true"), t0.Add(70*time.Second)) // one refilled
	ev, _ := st.EventsBetween(ctx, "cam1", t0.Add(-time.Minute), t0.Add(2*time.Minute), 100)
	if len(ev) != 4 || len(ids) != 11 {
		t.Fatalf("stored %d, transitions %d (%v), want 4 and 11", len(ev), len(ids), ids)
	}
	zero := 0
	for _, id := range ids {
		if id == 0 {
			zero++
		}
	}
	if zero != 7 || ids[10] == 0 {
		t.Fatalf("ids = %v", ids)
	}
}

// Whatever answers at the camera address must not be able to send our
// request (SOAP body, digest) to another host with a redirect.
func TestClientRefusesRedirects(t *testing.T) {
	for _, code := range []int{http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			var reached atomic.Int32
			other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reached.Add(1)
			}))
			defer other.Close()
			cam := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, other.URL+"/api/streams?src=cam2&dst=rtsp://127.0.0.1:9/x", code)
			}))
			defer cam.Close()
			host, port := hostPort(t, cam.URL)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			c := NewClient(host, port, "u", "p")
			if _, err := c.SystemTime(ctx); err == nil || !strings.Contains(err.Error(), "redirect") {
				t.Errorf("SystemTime: %v, want a refused redirect", err)
			}
			sub := &Subscription{c: c, URL: cam.URL + "/event_service/0"}
			if _, err := sub.Pull(ctx, time.Second); err == nil {
				t.Error("Pull followed a redirect")
			}
			// A client set up by the caller (as the tests and the wizard do)
			// refuses them too.
			c2 := &Client{Base: cam.URL, HTTP: http.DefaultClient}
			if _, err := c2.DeviceInformation(ctx); err == nil {
				t.Error("DeviceInformation followed a redirect")
			}
			p := NewPTZ(host, port, "", "u", "p", Invert{})
			if _, err := p.Presets(ctx); err == nil {
				t.Error("Presets followed a redirect")
			}
			if n := reached.Load(); n != 0 {
				t.Fatalf("the redirect target received %d requests", n)
			}
		})
	}
}
