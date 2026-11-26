package api

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type pushRT func(*http.Request) (*http.Response, error)

func (f pushRT) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// browserKeys returns a valid subscription key pair (p256dh, auth).
func browserKeys(t *testing.T) (string, string) {
	t.Helper()
	k, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)
	_, _ = rand.Read(auth)
	return base64.RawURLEncoding.EncodeToString(k.PublicKey().Bytes()), base64.RawURLEncoding.EncodeToString(auth)
}

// Lead 5: every user's deliveries shared one context, user after user, so a
// viewer early in name order with many slow subscriptions used up everyone
// else's time. Each user now has a budget of their own, whatever the event's
// snapshot left of the context, and users are served side by side.
func TestNotifyGivesEveryUserTheirOwnBudget(t *testing.T) {
	_, srv := newTestServer(t)
	ctx := context.Background()
	p256, auth := browserKeys(t)
	user := func(name string, subs int) {
		id, err := srv.Store.CreateUser(ctx, name, "x", "viewer")
		if err != nil {
			t.Fatal(err)
		}
		if err := srv.Store.SetUserNotify(ctx, id, `{"follow_mode":false,"types":["motion"]}`); err != nil {
			t.Fatal(err)
		}
		for i := range subs {
			if err := srv.Store.SavePushSub(ctx, id, nil, fmt.Sprintf("https://fcm.googleapis.com/fcm/send/%s-%d", name, i), p256, auth, "ua"); err != nil {
				t.Fatal(err)
			}
		}
	}
	user("aaa", 10) // sorts first; its push service never answers
	user("bbb", 10)
	user("zzz", 1)
	var mu sync.Mutex
	delivered := map[string]bool{}
	srv.push.sender.Client = &http.Client{Transport: pushRT(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/zzz-") {
			mu.Lock()
			delivered[r.URL.Path] = true
			mu.Unlock()
			return &http.Response{StatusCode: http.StatusCreated, Body: http.NoBody, Request: r}, nil
		}
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
	srv.push.userBudget, srv.push.sendTimeout = 600*time.Millisecond, 300*time.Millisecond
	ev, _ := srv.Store.OpenEvent(ctx, "cam1", "motion", time.Now().UTC(), "")
	// What a 12 s snapshot leaves of the event's 17 s: here, about enough to
	// read the users (the old code then spent it all on aaa's first device).
	short, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	start := time.Now()
	srv.notify(short, "cam1", "motion", ev, time.Now())
	mu.Lock()
	ok := delivered["/fcm/send/zzz-0"]
	mu.Unlock()
	if !ok {
		t.Fatal("the last user's notice was not delivered")
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("notify took %v", d)
	}
}

func TestPushSubscribeKeepsAFewDevicesPerUser(t *testing.T) {
	ts, _ := newTestServer(t)
	admin := newClient(t, ts.URL)
	admin.do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`)
	for i := range 15 {
		body := fmt.Sprintf(`{"endpoint":"https://fcm.googleapis.com/fcm/send/dev-%d","keys":{"p256dh":"a","auth":"b"}}`, i)
		if code, _, b := admin.do("POST", "/api/push/subscribe", body); code != 200 {
			t.Fatalf("subscribe %d = %d %s", i, code, b)
		}
	}
	if _, m, _ := admin.do("GET", "/api/push", ""); m["devices"] != float64(10) {
		t.Fatalf("devices = %v, want 10", m["devices"])
	}
}
