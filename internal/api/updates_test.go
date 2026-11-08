package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUntilDaily(t *testing.T) {
	loc := time.FixedZone("BRT", -3*3600)
	now := time.Date(2026, 11, 8, 2, 30, 0, 0, loc)
	if d := untilDaily(now, "04:00", loc); d != 90*time.Minute {
		t.Errorf("later today: %v", d)
	}
	if d := untilDaily(now, "02:00", loc); d != 23*time.Hour+30*time.Minute {
		t.Errorf("already past: tomorrow, got %v", d)
	}
	if d := untilDaily(now, "02:30", loc); d != 24*time.Hour {
		t.Errorf("this very minute: tomorrow, got %v", d)
	}
	if d := untilDaily(now, "bad", loc); d != 90*time.Minute {
		t.Errorf("invalid time falls back to 04:00, got %v", d)
	}
}

func TestUpdateCheckAndInstallRequests(t *testing.T) {
	ts, srv := newTestServer(t)
	admin := newClient(t, ts.URL)
	admin.do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`)
	req := filepath.Join(srv.updatesDir(), "request")

	if code, m, body := admin.do("POST", "/api/updates/check", ""); code != 202 || m["requested"] != "check" {
		t.Fatalf("check: %d %s", code, body)
	}
	if b, _ := os.ReadFile(req); !strings.HasPrefix(string(b), "check ") {
		t.Fatalf("request file: %q", b)
	}
	select {
	case <-srv.mirrorKick: // the app mirror checks right away too
	default:
		t.Fatal("check did not ask the app mirror")
	}
	if _, m, _ := admin.do("GET", "/api/updates", ""); m["pending"] != true {
		t.Fatalf("pending not shown: %v", m)
	}

	if code, m, body := admin.do("POST", "/api/updates/check", `{"install":true}`); code != 202 || m["requested"] != "install" {
		t.Fatalf("install: %d %s", code, body)
	}
	if b, _ := os.ReadFile(req); !strings.HasPrefix(string(b), "install ") {
		t.Fatalf("request file: %q", b)
	}
	// A few requests a minute per kind, then wait.
	limited := false
	for i := 0; i < 10 && !limited; i++ {
		code, _, _ := admin.do("POST", "/api/updates/check", `{"install":true}`)
		limited = code == 429
	}
	if !limited {
		t.Fatal("install requests are not rate limited")
	}
}
