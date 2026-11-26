package api

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A deleted account's Cloud folder and recycle bin never pass to the next
// account with that name, typed or derived from an e-mail; a folder left
// under a name is given to a new account only when the admin says so.
func TestDeletedAccountCloudIsNotReissued(t *testing.T) {
	share := t.TempDir()
	ts, _ := newTestServerWith(t, "volumes:\n  - {id: nuvem, name: Nuvem, path: "+filepath.ToSlash(share)+", kind: nas}\n")
	admin := newClient(t, ts.URL)
	admin.do("POST", "/api/setup", `{"username":"dono","password":"long enough pw"}`)
	ana, anaID := signedInAccount(t, ts, admin, "ana", "operator", "")
	ana.do("GET", "/api/nas/list?path=", "")
	if code, _, body := ana.do("PUT", "/api/nas/upload?path=ana/private.txt&offset=0&total=5", "hello"); code != 200 {
		t.Fatalf("upload = %d %s", code, body)
	}
	ana.do("PUT", "/api/nas/upload?path=ana/old.txt&offset=0&total=3", "old")
	if code, _, body := ana.do("DELETE", "/api/nas/file?path=ana/old.txt", ""); code != 200 {
		t.Fatalf("delete = %d %s", code, body)
	}
	if code, _, body := admin.do("DELETE", fmt.Sprintf("/api/users/%d", anaID), ""); code != 200 {
		t.Fatalf("delete account = %d %s", code, body)
	}

	// Same name again, derived from an e-mail: it starts empty.
	code, m, body := admin.do("POST", "/api/users", `{"email":"ana@example.org","password":"another long pw","role":"operator"}`)
	if code != 201 || m["username"] != "ana" {
		t.Fatalf("recreate = %d %s", code, body)
	}
	again := newClient(t, ts.URL)
	if code, _, body := again.do("POST", "/api/login", `{"username":"ana","password":"another long pw"}`); code != 200 {
		t.Fatalf("login = %d %s", code, body)
	}
	again.do("GET", "/api/nas/list?path=", "") // makes the own folder
	if code, _, body := again.do("GET", "/api/nas/list?path=ana", ""); code != 200 || strings.Contains(body, "private.txt") {
		t.Fatalf("new ana's folder = %d %s", code, body)
	}
	if code, _, body := again.do("GET", "/api/nas/list?path=.lixeira/ana", ""); strings.Contains(body, "old.txt") {
		t.Fatalf("new ana's bin = %d %s", code, body)
	}
	// The admins keep the old files.
	code, _, body = admin.do("GET", "/api/nas/list?path=Contas%20removidas", "")
	if code != 200 || !strings.Contains(body, `"ana `) {
		t.Fatalf("kept folder = %d %s", code, body)
	}
	if code, _, body := again.do("GET", "/api/nas/list?path=Contas%20removidas", ""); code != 403 {
		t.Fatalf("new ana lists the kept folders = %d %s", code, body)
	}

	// A folder left under a name (before this fix, or made over SMB).
	for _, d := range []string{"bia", ".lixeira/bia", "carol"} {
		if err := os.MkdirAll(filepath.Join(share, filepath.FromSlash(d)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(share, "bia", "x.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, body := admin.do("POST", "/api/users", `{"username":"bia","password":"another long pw","role":"operator"}`); code != 409 || !strings.Contains(body, "Cloud folder") {
		t.Fatalf("leftover without a choice = %d %s", code, body)
	}
	if code, _, body := admin.do("POST", "/api/users", `{"username":"bia","password":"another long pw","role":"operator","cloud":"keep"}`); code != 400 {
		t.Fatalf("unknown choice = %d %s", code, body)
	}
	// Refused for another reason (e-mail taken): nothing is moved.
	if code, _, body := admin.do("POST", "/api/users", `{"username":"bia","email":"ana@example.org","password":"another long pw","role":"operator","cloud":"archive"}`); code != 409 {
		t.Fatalf("taken e-mail = %d %s", code, body)
	}
	if _, err := os.Stat(filepath.Join(share, "bia", "x.txt")); err != nil {
		t.Fatalf("a refused account moved the folder: %v", err)
	}
	if code, _, body := admin.do("POST", "/api/users", `{"username":"bia","password":"another long pw","role":"operator","cloud":"archive"}`); code != 201 {
		t.Fatalf("archive = %d %s", code, body)
	}
	if _, err := os.Stat(filepath.Join(share, "bia", "x.txt")); !os.IsNotExist(err) {
		t.Fatalf("archived folder still under the name: %v", err)
	}
	if code, _, body := admin.do("POST", "/api/users", `{"username":"carol","password":"another long pw","role":"operator","cloud":"adopt"}`); code != 201 {
		t.Fatalf("adopt = %d %s", code, body)
	}
	if _, err := os.Stat(filepath.Join(share, "carol")); err != nil {
		t.Fatalf("adopted folder moved: %v", err)
	}
	// An e-mail never derives a name with a folder left under it.
	if err := os.MkdirAll(filepath.Join(share, "dani"), 0o755); err != nil {
		t.Fatal(err)
	}
	if code, m, body := admin.do("POST", "/api/users", `{"email":"dani@example.org","role":"viewer"}`); code != 201 || m["username"] != "dani2" {
		t.Fatalf("derived name = %d %s", code, body)
	}
	// A live account's name is refused before anything is moved.
	if code, _, _ := admin.do("POST", "/api/users", `{"username":"ANA","password":"another long pw","role":"viewer","cloud":"archive"}`); code != 409 {
		t.Fatalf("live name = %d", code)
	}
	if _, err := os.Stat(filepath.Join(share, "ana")); err != nil {
		t.Fatalf("a live account's folder was moved: %v", err)
	}
}
