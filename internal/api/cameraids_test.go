package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/LUKER2DW/sentinela/internal/camera/onvif"
	"github.com/LUKER2DW/sentinela/internal/store"
)

// A removed camera's id is never given to another camera: its grants and
// settings go, the wizard and the editor skip it, and a viewer who had it
// gets no new camera (nor "every camera") through it.
func TestRemovedCameraIDIsNeverReissued(t *testing.T) {
	c, srv := settingsServer(t)
	ctx := context.Background()
	ts := &httptest.Server{URL: c.base} // account() needs only the address
	twoCameras(srv)
	start := func() { // serve starts with srv.Cfg
		t.Helper()
		srv.idsSynced = false
		if err := srv.SyncCameraIDs(ctx); err != nil {
			t.Fatal(err)
		}
	}
	start()
	only2, only2ID := signedInAccount(t, ts, c, "only2", "viewer", `"cam2"`)
	_, bothID := signedInAccount(t, ts, c, "both", "viewer", `"cam1","cam2"`)
	at := time.Now().UTC().Add(-time.Hour)
	if err := srv.Store.UpsertSegment(ctx, store.Segment{CameraID: "cam2", Start: at, End: at.Add(time.Minute), Path: "cam2/old.mp4", Bytes: 10}); err != nil {
		t.Fatal(err)
	}
	for _, k := range cameraSettingKeys("cam2") {
		if err := srv.Store.SetSetting(ctx, k, "x"); err != nil {
			t.Fatal(err)
		}
	}
	_ = srv.Store.SetSetting(ctx, detectionKey("cam1"), "kept")

	// cam2 is removed (the server restarts with the new config).
	srv.Cfg.Cameras = srv.Cfg.Cameras[:1]
	start()
	cams := func(id int64) []string {
		u, err := srv.Store.UserByID(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return u.Cameras
	}
	if got := cams(bothID); !slices.Equal(got, []string{"cam1"}) {
		t.Errorf("grant naming cam1 and the removed cam2 = %v, want [cam1]", got)
	}
	if got := cams(only2ID); !slices.Equal(got, []string{"cam2"}) {
		t.Errorf("grant naming only the removed cam2 = %v, want it kept (never every camera)", got)
	}
	for _, k := range []string{detectionKey("cam2"), onvif.PullPointSettingKey("cam2")} {
		if v := srv.Store.Setting(ctx, k, "gone"); v != "gone" {
			t.Errorf("setting %s of the removed camera kept: %q", k, v)
		}
	}
	if v := srv.Store.Setting(ctx, detectionKey("cam1"), ""); v != "kept" {
		t.Errorf("setting of a configured camera dropped: %q", v)
	}
	// The viewer limited to the removed camera sees none of the others.
	if code, m, body := only2.do("GET", "/api/cameras", ""); code != 200 || strings.Contains(body, `"cam1"`) {
		t.Fatalf("cameras of a viewer whose camera was removed: %d %v %s", code, m, body)
	}

	// The wizard and the editor never offer cam2 again.
	if id, err := srv.nextCameraID(ctx); err != nil || id != "cam3" {
		t.Fatalf("next camera id = %q %v, want cam3", id, err)
	}
	code, m, body := c.do("GET", "/api/settings", "")
	if code != 200 {
		t.Fatalf("settings: %d %s", code, body)
	}
	if fmt.Sprint(m["retired_ids"]) != "[cam2]" {
		t.Fatalf("retired_ids = %v, want [cam2]", m["retired_ids"])
	}
	var doc SettingsDoc
	b, _ := json.Marshal(m["settings"])
	_ = json.Unmarshal(b, &doc)
	doc.Cameras = append(doc.Cameras, SettingsCamera{ID: "cam2", Name: "Nova", Host: "192.168.1.31", Vendor: "hikvision", Record: true, User: "admin", Password: "Garage55pass"})
	req, _ := json.Marshal(doc)
	if code, _, body := c.do("PUT", "/api/settings", string(req)); code != 400 || !strings.Contains(body, "removed camera") {
		t.Fatalf("new camera with the removed id: %d %s", code, body)
	}
	doc.Cameras[1].ID = "cam3"
	req, _ = json.Marshal(doc)
	if code, _, body := c.do("PUT", "/api/settings", string(req)); code != 202 {
		t.Fatalf("new camera with a fresh id: %d %s", code, body)
	}

	// Restarting with the same config changes nothing more.
	start()
	if got := cams(only2ID); !slices.Equal(got, []string{"cam2"}) {
		t.Errorf("second start changed the grant: %v", got)
	}

	// A hand edit brings cam2 back: a new camera, with nothing of the old.
	twoCameras(srv)
	start()
	if code, _, body := only2.do("GET", "/api/cameras", ""); code != 200 || strings.Contains(body, `"cam2"`) || strings.Contains(body, `"cam1"`) {
		t.Fatalf("the old camera's viewer after cam2 came back: %d %s", code, body)
	}
	if segs, _ := srv.Store.SegmentsBetween(ctx, "cam2", at.Add(-time.Hour), time.Now()); len(segs) != 0 {
		t.Fatalf("the old camera's recordings on the new cam2: %+v", segs)
	}
	rows, _ := srv.Store.UserAudit(ctx, "", 20)
	revived := false
	for _, r := range rows {
		revived = revived || r.Action == "camera.revive" && r.Target == "cam2"
	}
	if !revived {
		t.Fatal("bringing a removed id back was not audited")
	}
}

// Grants keep only configured cameras: a stale id from a form is dropped,
// and a list with nothing configured left is refused, never "every camera".
func TestCameraGrantDropsUnknownIDs(t *testing.T) {
	ts, srv := newTestServer(t)
	twoCameras(srv)
	admin := newClient(t, ts.URL)
	admin.do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`)
	_, id := signedInAccount(t, ts, admin, "kid", "viewer", `"cam1"`)
	cases := []struct {
		body string
		code int
		want []string
	}{
		{`["cam9","cam2"]`, 200, []string{"cam2"}},
		{`["cam2","cam2","cam1"]`, 200, []string{"cam2", "cam1"}},
		{`["cam9"]`, 400, []string{"cam2", "cam1"}},
		{`[]`, 200, []string{}},
	}
	for _, tc := range cases {
		code, _, body := admin.do("PATCH", fmt.Sprintf("/api/users/%d", id), `{"cameras":`+tc.body+`}`)
		u, _ := srv.Store.UserByID(context.Background(), id)
		if code != tc.code || !slices.Equal(u.Cameras, tc.want) {
			t.Errorf("PATCH %s = %d %s, stored %v; want %d and %v", tc.body, code, body, u.Cameras, tc.code, tc.want)
		}
	}
	if code, _, _ := admin.do("POST", "/api/users", `{"username":"kid2","password":"long enough pw","role":"viewer","cameras":["cam9","cam2"]}`); code != 201 {
		t.Fatalf("create with a stale id = %d", code)
	}
	u, _ := srv.Store.UserByName(context.Background(), "kid2")
	if !slices.Equal(u.Cameras, []string{"cam2"}) {
		t.Fatalf("created grant = %v, want [cam2]", u.Cameras)
	}
}
