package api

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/LUKER2DW/sentinela/internal/auth"
)

// liveCutAudit returns the details of the live.cut rows of username.
func liveCutAudit(t *testing.T, srv *Server, username string) []string {
	t.Helper()
	rows, err := srv.Store.UserAudit(context.Background(), username, 50)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, r := range rows {
		if r.Action == "live.cut" {
			out = append(out, r.Detail)
		}
	}
	return out
}

// A viewer who keeps WebRTC media, signs out and signs in again, over and
// over, gets one free cut; the next one cools the account down, and while
// it is cooled down it can open no stream, so it causes no further cut.
func TestLiveCutLoopIsRationed(t *testing.T) {
	ts, srv := newTestServer(t)
	f, u := newFakeGo2RTC(t, true)
	srv.go2rtc = u
	liveTimings(t, 150*time.Millisecond, 0, time.Hour)       // no slot wait: count charges, not gaps
	srv.homeLim.attempts = auth.NewLimiter(100, time.Minute) // it signs in again and again
	runLiveGuard(t, srv)
	admin := newClient(t, ts.URL)
	admin.do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`)
	signedInAccount(t, ts, admin, "kid", "viewer", "")

	refused := 0
	for i := 0; i < 5; i++ {
		kid := newClient(t, ts.URL)
		if code, _, body := kid.do("POST", "/api/login", `{"username":"kid","password":"long enough pw"}`); code != 200 {
			t.Fatalf("login %d = %d %s", i, code, body)
		}
		code, path := ticket(t, kid, "cam1", "main")
		if code == 429 {
			refused++
			continue
		}
		if code != 200 {
			t.Fatalf("ticket %d = %d %s", i, code, path)
		}
		conn, status := openStream(t, ts, kid, path)
		if conn == nil {
			t.Fatalf("upgrade %d = %s", i, status)
		}
		<-f.opened
		conn.Close() // the WebSocket goes, the PeerConnection stays
		_, before, _ := f.counts()
		if code, _, _ := kid.do("POST", "/api/logout", ""); code != 200 {
			t.Fatalf("logout %d = %d", i, code)
		}
		waitFor(t, "a signed-out stream kept its media", func() bool {
			n, restarts, _ := f.counts()
			return n == 0 && restarts == before+1
		})
	}
	_, restarts, exits := f.counts()
	if restarts != 2 || exits != 0 || refused != 3 {
		t.Fatalf("restarts %d, exits %d, refused tickets %d; want 2 (the free one and the one that cools the account down), 0, 3", restarts, exits, refused)
	}
	if wait := srv.live.coolDown(mustUserID(t, srv, "kid")); wait < 9*time.Minute || wait > cutCoolFirst {
		t.Fatalf("cool-down %s, want about %s", wait, cutCoolFirst)
	}
	got := liveCutAudit(t, srv, "kid")
	if len(got) != 2 || !strings.Contains(got[0], "refused for 10m0s") || !strings.HasPrefix(got[1], "free") {
		t.Fatalf("audit of kid's cuts: %q", got)
	}
	// Other accounts are not affected.
	if code, _ := ticket(t, admin, "cam1", "main"); code != 200 {
		t.Fatalf("admin ticket during kid's cool-down = %d", code)
	}
}

func mustUserID(t *testing.T, srv *Server, name string) int64 {
	t.Helper()
	u, err := srv.Store.UserByName(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	return u.ID
}

// An admin's change is charged to the account it changed, never the admin.
func TestAdminChangeChargesTheAccount(t *testing.T) {
	ts, srv := newTestServer(t)
	f, u := newFakeGo2RTC(t, true)
	srv.go2rtc = u
	fastLiveGuard(t, srv)
	admin, _, kidID, conn, _ := liveViewer(t, ts, f)
	conn.Close()
	if code, _, body := admin.do("PATCH", fmt.Sprintf("/api/users/%d", kidID), `{"disabled":true}`); code != 200 {
		t.Fatalf("disable = %d %s", code, body)
	}
	waitFor(t, "media of a disabled account kept flowing", func() bool { _, restarts, _ := f.counts(); return restarts == 1 })
	waitFor(t, "the cut was not written to the audit log", func() bool { return len(liveCutAudit(t, srv, "kid")) == 1 })
	if got := liveCutAudit(t, srv, "admin"); len(got) != 0 {
		t.Fatalf("the admin was charged: %q", got)
	}
}

// Every stale consumer found goes in one cut; one found after it waits for
// the next slot, and the cut that worked is not escalated to an exit.
func TestLiveCutsCoalesceAndWaitForSlot(t *testing.T) {
	_, srv := newTestServer(t)
	f, u := newFakeGo2RTC(t, true)
	srv.go2rtc = u
	f.add("0123456789abcdef01234567")
	f.add("89abcdef0123456789abcdef")
	liveTimings(t, 50*time.Millisecond, time.Second, 100*time.Millisecond)
	runLiveGuard(t, srv)
	waitFor(t, "unknown consumers were left running", func() bool { n, restarts, _ := f.counts(); return n == 0 && restarts == 1 })
	f.add("fedcba9876543210fedcba98")
	time.Sleep(400 * time.Millisecond)
	if _, restarts, exits := f.counts(); restarts != 1 || exits != 0 {
		t.Fatalf("inside the cut gap: %d restarts, %d exits", restarts, exits)
	}
	waitFor(t, "the waiting consumer was not cut at the next slot", func() bool { n, restarts, _ := f.counts(); return n == 0 && restarts == 2 })
	if _, _, exits := f.counts(); exits != 0 {
		t.Fatalf("a restart that worked was followed by %d exits", exits)
	}
}

// A restart that did not take (its own consumers are still there) is
// followed by an exit.
func TestLiveCutEscalatesWhenItFailed(t *testing.T) {
	_, srv := newTestServer(t)
	f, u := newFakeGo2RTC(t, true)
	srv.go2rtc = u
	f.stuck = true
	f.add("0123456789abcdef01234567")
	liveTimings(t, 50*time.Millisecond, time.Hour, 200*time.Millisecond)
	runLiveGuard(t, srv)
	waitFor(t, "a restart that did not take was not escalated", func() bool {
		n, restarts, exits := f.counts()
		return n == 0 && restarts == 1 && exits == 1
	})
}

// The charge ladder: one free cut an hour, then cool-downs that double up
// to a day, cleared by a quiet day after the cool-down.
func TestLiveCutCharges(t *testing.T) {
	l := newLiveMedia()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	steps := []struct {
		at   time.Duration
		free bool
		cool time.Duration
	}{
		{0, true, 0},
		{61 * time.Minute, true, 0}, // an hour later, still no strike: free again
		{63 * time.Minute, false, 10 * time.Minute},
		{74 * time.Minute, false, 20 * time.Minute},
		{95 * time.Minute, false, 40 * time.Minute},
		{136 * time.Minute, false, 80 * time.Minute},
		{217 * time.Minute, false, 160 * time.Minute},
		{378 * time.Minute, false, 320 * time.Minute},
		{699 * time.Minute, false, 640 * time.Minute},
		{1340 * time.Minute, false, 1280 * time.Minute},
		{2621 * time.Minute, false, 24 * time.Hour},
		{4062 * time.Minute, false, 24 * time.Hour},
	}
	for _, s := range steps {
		free, cool := l.chargeLocked(7, t0.Add(s.at))
		if free != s.free || cool != s.cool {
			t.Fatalf("at %s: free %v cool %s; want %v %s", s.at, free, cool, s.free, s.cool)
		}
	}
	last := t0.Add(4062*time.Minute + 24*time.Hour)
	l.now = func() time.Time { return last.Add(-time.Minute) }
	if l.coolDown(7) != time.Minute || l.coolDown(8) != 0 {
		t.Fatal("only the charged account is cooled down")
	}
	if free, _ := l.chargeLocked(7, last.Add(cutQuiet)); !free {
		t.Fatal("a quiet day after the cool-down must clear the strikes")
	}
}

// Closed streams stop counting once go2rtc shows their consumer gone; one
// whose consumer stays counts; while go2rtc cannot be read, closed ones are
// forgotten after liveForget.
func TestLiveStreamsCappedPerAccount(t *testing.T) {
	_, srv := newTestServer(t)
	f, u := newFakeGo2RTC(t, false)
	srv.go2rtc = u
	l := srv.live
	now := time.Now()
	l.now = func() time.Time { return now }
	allow := func(context.Context) bool { return true }
	ctx := context.Background()
	fill := func() []string {
		var tags []string
		for i := 0; i < maxUserLiveStreams; i++ {
			tag, closed, err := l.open(1, "kid", allow, func() {})
			if err != nil {
				t.Fatalf("stream %d: %v", i, err)
			}
			closed()
			tags = append(tags, tag)
		}
		return tags
	}
	fill()
	if _, _, err := l.open(1, "kid", allow, func() {}); !errors.Is(err, errTooManyStreams) {
		t.Fatalf("closed streams go2rtc was not asked about yet must count: %v", err)
	}
	if _, _, err := l.open(2, "other", allow, func() {}); err != nil {
		t.Fatalf("another account: %v", err)
	}
	now = now.Add(time.Second)
	if _, err := srv.readLive(ctx); err != nil {
		t.Fatal(err)
	}
	tags := fill() // the first 64 are gone from go2rtc: they no longer count
	for _, tag := range tags {
		f.add(tag) // these kept their WebRTC media
	}
	now = now.Add(time.Second)
	if _, err := srv.readLive(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := l.open(1, "kid", allow, func() {}); !errors.Is(err, errTooManyStreams) {
		t.Fatalf("streams whose consumer is still in go2rtc must count: %v", err)
	}

	// go2rtc cannot be read: nothing is learnt, and the closed streams are
	// forgotten after liveForget, so memory stays bounded.
	srv.go2rtc = &url.URL{Scheme: "http", Host: "127.0.0.1:1"}
	now = now.Add(liveForget + time.Minute)
	if _, err := srv.readLive(ctx); err == nil {
		t.Fatal("go2rtc should be unreachable")
	}
	l.mu.Lock()
	left := len(l.streams)
	l.mu.Unlock()
	if left != 1 { // the other account's open stream
		t.Fatalf("%d streams remembered, want 1", left)
	}
	if _, _, err := l.open(1, "kid", allow, func() {}); err != nil {
		t.Fatalf("after forgetting: %v", err)
	}
}
