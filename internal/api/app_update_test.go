package api

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppUpdate(t *testing.T) {
	ts, srv := newTestServer(t)
	c := newClient(t, ts.URL)
	if code, _, _ := c.do("GET", "/api/app/latest", ""); code != 401 {
		t.Fatalf("unauthenticated latest = %d", code)
	}
	c.do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`)

	if code, m, _ := c.do("GET", "/api/app/latest", ""); code != 200 || m["available"] != false {
		t.Fatalf("no release: %d %v", code, m)
	}
	if code, _, _ := c.do("GET", "/api/app/sentinela.apk", ""); code != 404 {
		t.Fatalf("apk without release = %d", code)
	}

	dir := srv.appDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	apk := []byte("PK fake apk")
	sum := sha256.Sum256(apk)
	_ = os.WriteFile(filepath.Join(dir, "sentinela.apk"), apk, 0o644)
	manifest := func(size int) {
		_ = os.WriteFile(filepath.Join(dir, "app.json"), []byte(fmt.Sprintf(
			`{"version_code":10,"version_name":"1.9","size":%d,"sha256":%q}`, size, hex.EncodeToString(sum[:]))), 0o644)
	}

	manifest(len(apk) + 1) // size mismatch: half-copied APK is never offered
	if _, m, _ := c.do("GET", "/api/app/latest", ""); m["available"] != false {
		t.Fatalf("mismatched release offered: %v", m)
	}
	manifest(len(apk))
	if code, m, _ := c.do("GET", "/api/app/latest", ""); code != 200 || m["available"] != true || m["version_code"] != float64(10) {
		t.Fatalf("release: %d %v", code, m)
	}
	if code, _, body := c.do("GET", "/api/app/sentinela.apk", ""); code != 200 || body != string(apk) {
		t.Fatalf("apk = %d %q", code, body)
	}
}

func TestViewerHasNoNuvem(t *testing.T) {
	ts, _ := newTestServer(t)
	admin := newClient(t, ts.URL)
	admin.do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`)
	admin.do("POST", "/api/users", `{"username":"view","password":"viewer password","role":"viewer"}`)
	viewer := newClient(t, ts.URL)
	viewer.do("POST", "/api/login", `{"username":"view","password":"viewer password"}`)
	for _, p := range []string{"/api/nas/list?path=", "/api/nas/file?path=a", "/api/nas/thumb?path=a"} {
		if code, _, _ := viewer.do("GET", p, ""); code != 403 {
			t.Fatalf("viewer %s = %d", p, code)
		}
	}
	if code, _, _ := viewer.do("GET", "/api/cameras", ""); code != 200 {
		t.Fatalf("viewer cameras = %d", code)
	}
}

func TestAvatar(t *testing.T) {
	ts, _ := newTestServer(t)
	c := newClient(t, ts.URL)
	c.do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`)
	if code, _, _ := c.do("GET", "/api/avatars/admin", ""); code != 404 {
		t.Fatalf("no avatar yet = %d", code)
	}
	if code, _, _ := c.do("POST", "/api/me/avatar", `<svg onload="alert(1)"/>`); code != 415 {
		t.Fatalf("svg accepted: %d", code)
	}
	png := "\x89PNG\r\n\x1a\n" + strings.Repeat("\x00", 32)
	if code, _, body := c.do("POST", "/api/me/avatar", png); code != 200 {
		t.Fatalf("upload = %d %s", code, body)
	}
	if code, _, body := c.do("GET", "/api/avatars/admin", ""); code != 200 || body != png {
		t.Fatalf("get = %d", code)
	}
	c.do("DELETE", "/api/me/avatar", "")
	if code, _, _ := c.do("GET", "/api/avatars/admin", ""); code != 404 {
		t.Fatalf("after delete = %d", code)
	}
}
