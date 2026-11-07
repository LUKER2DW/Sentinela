package api

import "testing"

func TestOnboardingGuide(t *testing.T) {
	ts, srv := newTestServer(t)
	admin := newClient(t, ts.URL)
	if code, _, body := admin.do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`); code != 200 {
		t.Fatalf("setup: %d %s", code, body)
	}
	_, me, _ := admin.do("GET", "/api/me", "")
	if f, _ := me["features"].(map[string]any); f["setup_guide"] != true {
		t.Fatalf("new install: guide not pending: %v", me["features"])
	}
	code, m, _ := admin.do("GET", "/api/onboarding", "")
	if code != 200 || m["pending"] != true {
		t.Fatalf("get: %d %v", code, m)
	}
	if cams, _ := m["cameras"].([]any); len(cams) != 1 {
		t.Fatalf("cameras: %v", m["cameras"])
	}
	if code, _, _ := admin.do("PUT", "/api/onboarding", `{"pending":false}`); code != 200 {
		t.Fatalf("finish: %d", code)
	}
	_, me, _ = admin.do("GET", "/api/me", "")
	if f, _ := me["features"].(map[string]any); f["setup_guide"] != false {
		t.Fatalf("after finish: %v", me["features"])
	}
	// Installs that existed before the guide never get it by themselves.
	if err := srv.Store.SetSetting(t.Context(), onboardingKey, "done"); err != nil {
		t.Fatal(err)
	}
	viewer := newClient(t, ts.URL)
	if code, _, _ := viewer.do("GET", "/api/onboarding", ""); code != 401 {
		t.Fatalf("anonymous: %d", code)
	}
}
