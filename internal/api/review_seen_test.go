package api

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Lead 4: a viewer could store any number of made-up review ids. Only
// existing items of cameras the user may see are stored now, a call is
// bounded, and marks go with their events.
func TestMarkSeenStoresOnlyVisibleItems(t *testing.T) {
	ts, srv := newTestServer(t)
	srv.Cfg.Cameras = append(srv.Cfg.Cameras, srv.Cfg.Cameras[0])
	srv.Cfg.Cameras[1].ID, srv.Cfg.Cameras[1].Name = "cam2", "Fundo"
	ctx := context.Background()
	at := time.Now().UTC().Add(-time.Hour)
	mine, err := srv.Store.OpenEvent(ctx, "cam1", "motion", at, "")
	if err != nil {
		t.Fatal(err)
	}
	hidden, err := srv.Store.OpenEvent(ctx, "cam2", "motion", at, "")
	if err != nil {
		t.Fatal(err)
	}
	admin := newClient(t, ts.URL)
	admin.do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`)
	if code, _, body := admin.do("POST", "/api/users", `{"username":"kid","password":"viewer password","role":"viewer","cameras":["cam1"]}`); code != 201 {
		t.Fatalf("create = %d %s", code, body)
	}
	kid := newClient(t, ts.URL)
	kid.do("POST", "/api/login", `{"username":"kid","password":"viewer password"}`)
	rows := func() int {
		var n int
		if err := srv.Store.PrivateDB().QueryRowContext(ctx, `SELECT count(*) FROM review_seen r JOIN users u ON u.id=r.user_id WHERE u.username='kid'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	many := make([]string, maxSeenIDs+1)
	for i := range many {
		many[i] = fmt.Sprint(1_000_000 + i)
	}
	cases := []struct {
		name     string
		body     string
		wantCode int
		marked   float64
		rows     int
	}{
		{"made-up, hidden and repeated ids", fmt.Sprintf(`{"ids":[%d,%d,999999,%d]}`, mine, hidden, mine), 200, 1, 1},
		{"too many ids in one call", `{"ids":[` + strings.Join(many, ",") + `]}`, 400, 0, 1},
		{"only made-up ids", `{"ids":[123456789,987654321]}`, 200, 0, 1},
	}
	for _, c := range cases {
		code, m, body := kid.do("POST", "/api/review/seen", c.body)
		if code != c.wantCode || code == 200 && m["marked"] != c.marked || rows() != c.rows {
			t.Fatalf("%s: %d %s, rows %d (want %d, marked %v, rows %d)", c.name, code, body, rows(), c.wantCode, c.marked, c.rows)
		}
	}
	if _, m, _ := kid.do("GET", "/api/review", ""); m["unseen"] != float64(0) {
		t.Fatalf("the visible item is not seen: %v", m)
	}

	// The mark goes once retention deletes its event.
	if err := srv.Store.PurgeEventsBefore(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := srv.Store.PurgeSeenBefore(ctx, time.Now().Add(-31*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if n := rows(); n != 0 {
		t.Fatalf("%d marks left for deleted events", n)
	}
}
