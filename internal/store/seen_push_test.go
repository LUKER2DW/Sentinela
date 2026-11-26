package store

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"
)

func TestEventCamerasChunks(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()
	at := time.Now().UTC()
	a, _ := st.OpenEvent(ctx, "cam1", "motion", at, "")
	b, _ := st.OpenEvent(ctx, "cam2", "motion", at, "")
	ids := []int64{a}
	for i := int64(0); i < 1200; i++ { // more than one query's worth
		ids = append(ids, 1_000_000+i)
	}
	ids = append(ids, b)
	got, err := st.EventCameras(ctx, ids)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[a] != "cam1" || got[b] != "cam2" {
		t.Fatalf("got %v", got)
	}
}

func TestPushDevicesCappedPerUser(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()
	uid, err := st.CreateUser(ctx, "ana", "x", "viewer")
	if err != nil {
		t.Fatal(err)
	}
	other, _ := st.CreateUser(ctx, "bia", "x", "viewer")
	_ = st.SavePushSub(ctx, other, nil, "https://push/bia", "k", "a", "ua")
	hash := []byte("session-hash-ana")
	if err := st.CreateSession(ctx, hash, uid, "csrf", time.Now().Add(time.Hour), "ua", false, false, "h", "lan"); err != nil {
		t.Fatal(err)
	}
	for i := range MaxPushSubs + 2 {
		if err := st.SavePushSub(ctx, uid, nil, fmt.Sprintf("https://push/ana-%d", i), "k", "a", "ua"); err != nil {
			t.Fatal(err)
		}
		if err := st.SaveFCMToken(ctx, uid, hash, fmt.Sprintf("token-ana-%02d", i), "ua"); err != nil {
			t.Fatal(err)
		}
	}
	subs, _ := st.PushSubs(ctx, uid)
	if len(subs) != MaxPushSubs {
		t.Fatalf("%d subscriptions kept, want %d", len(subs), MaxPushSubs)
	}
	for _, p := range subs {
		if p.Endpoint == "https://push/ana-0" || p.Endpoint == "https://push/ana-1" {
			t.Fatalf("the oldest subscription %s was kept", p.Endpoint)
		}
	}
	tokens, _ := st.FCMTokens(ctx, uid)
	if len(tokens) != MaxFCMTokens || slices.Contains(tokens, "token-ana-00") {
		t.Fatalf("tokens kept: %v", tokens)
	}
	// Another user's devices are not touched.
	if b, _ := st.PushSubs(ctx, other); len(b) != 1 {
		t.Fatalf("other user's subscriptions: %d", len(b))
	}
}

func TestPushFailedDropsOnlyDeadEndpoints(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()
	uid, _ := st.CreateUser(ctx, "ana", "x", "viewer")
	_ = st.SavePushSub(ctx, uid, nil, "https://push/dead", "k", "a", "ua")
	_ = st.SavePushSub(ctx, uid, nil, "https://push/worked", "k", "a", "ua")
	ids := map[string]int64{}
	subs, _ := st.PushSubs(ctx, uid)
	for _, p := range subs {
		ids[p.Endpoint] = p.ID
	}
	st.MarkPushOK(ctx, ids["https://push/worked"])
	for i := 1; i <= 5; i++ {
		dropped, err := st.PushFailed(ctx, ids["https://push/dead"])
		if err != nil {
			t.Fatal(err)
		}
		if want := i == deadPushFails; dropped != want {
			t.Fatalf("failure %d: dropped=%v, want %v", i, dropped, want)
		}
		if dropped, _ := st.PushFailed(ctx, ids["https://push/worked"]); dropped {
			t.Fatal("a subscription that once delivered was dropped")
		}
	}
	subs, _ = st.PushSubs(ctx, uid)
	if len(subs) != 1 || subs[0].Endpoint != "https://push/worked" {
		t.Fatalf("left: %+v", subs)
	}
}
