package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func settingsServer(t *testing.T) (*client, *Server) {
	t.Helper()
	ts, srv := newTestServer(t)
	dir := t.TempDir()
	srv.ConfigPath = filepath.Join(dir, "config.yaml")
	raw := "data_dir: " + filepath.ToSlash(srv.Cfg.DataDir) + "\napps:\n  enabled: false\ncameras:\n  - {id: cam1, name: Frente, host: 127.0.0.1, dvrip_port: 1, user_env: U, password_env: P}\n"
	if err := os.WriteFile(srv.ConfigPath, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	srv.Cfg.ConfigUnitsMarker = filepath.Join(dir, "config-units")
	if err := os.WriteFile(srv.Cfg.ConfigUnitsMarker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	c := newClient(t, ts.URL)
	c.do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`)
	return c, srv
}

func TestSettingsEditor(t *testing.T) {
	c, srv := settingsServer(t)
	code, m, body := c.do("GET", "/api/settings", "")
	if code != 200 || m["host"] != true {
		t.Fatalf("get: %d %s", code, body)
	}
	var doc SettingsDoc
	b, _ := json.Marshal(m["settings"])
	if err := json.Unmarshal(b, &doc); err != nil || len(doc.Cameras) != 1 {
		t.Fatalf("settings: %v %s", err, b)
	}
	if strings.Contains(body, "Sup3r@Secret") {
		t.Fatal("the settings leak a camera password")
	}

	// A new camera without credentials is refused.
	doc.Cameras = append(doc.Cameras, SettingsCamera{ID: "garage", Name: "Garagem", Host: "192.168.1.30", Vendor: "hikvision", Record: true})
	req, _ := json.Marshal(doc)
	if code, _, body := c.do("PUT", "/api/settings", string(req)); code != 400 || !strings.Contains(body, "user and password") {
		t.Fatalf("no credentials: %d %s", code, body)
	}
	// Invalid values are refused by the server's own parser.
	bad := doc
	bad.Recording.MaxDiskPercent = 5
	bad.Cameras = bad.Cameras[:1]
	req, _ = json.Marshal(bad)
	if code, _, body := c.do("PUT", "/api/settings", string(req)); code != 400 || !strings.Contains(body, "max_disk_percent") {
		t.Fatalf("bad value: %d %s", code, body)
	}

	doc.Cameras[1].User, doc.Cameras[1].Password = "admin", "Garage55pass"
	doc.Timezone = "Europe/Lisbon"
	req, _ = json.Marshal(doc)
	if code, _, body := c.do("PUT", "/api/settings", string(req)); code != 202 {
		t.Fatalf("apply: %d %s", code, body)
	}
	dir := srv.settingsDir()
	yml, _ := os.ReadFile(filepath.Join(dir, "pending.yaml"))
	for _, want := range []string{"timezone: Europe/Lisbon", "id: garage", "user_env: GARAGE_USER", "user_env: U", "enabled: false"} {
		if !strings.Contains(string(yml), want) {
			t.Errorf("pending.yaml lacks %q:\n%s", want, yml)
		}
	}
	if strings.Contains(string(yml), "Garage55pass") {
		t.Fatal("a password went into config.yaml")
	}
	sec, _ := os.ReadFile(filepath.Join(dir, "pending-secrets.json"))
	if string(sec) != `{"GARAGE_PASS":"Garage55pass","GARAGE_USER":"admin"}` {
		t.Fatalf("secrets: %s", sec)
	}
	if st, err := os.Stat(filepath.Join(dir, "pending-secrets.json")); err != nil || (st.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/') {
		t.Fatalf("secrets file mode: %v %v", st, err)
	}
	// While one change waits for the host, another is refused.
	if code, _, _ := c.do("PUT", "/api/settings", string(req)); code != 409 {
		t.Fatalf("second apply: %d", code)
	}
}
