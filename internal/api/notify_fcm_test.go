package api

import (
	"net/http"
	"testing"
	"time"
)

func TestPendingNoticeBelongsToOneUserAndExpires(t *testing.T) {
	ts, s := newTestServer(t)
	admin := newClient(t, ts.URL)
	if code, _, _ := admin.do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`); code != http.StatusOK {
		t.Fatalf("setup: HTTP %d", code)
	}
	if code, m, _ := admin.do("GET", "/api/notify/fcm", ""); code != http.StatusOK || m["available"] != false {
		t.Fatalf("no Firebase key: HTTP %d %v", code, m)
	}
	if code, _, _ := admin.do("POST", "/api/users", `{"username":"viewer","password":"viewer password","role":"viewer"}`); code != http.StatusCreated {
		t.Fatalf("create viewer: HTTP %d", code)
	}
	u, err := s.Store.UserByName(t.Context(), "admin")
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.push.putPending(u.ID, []byte(`{"title":"Pessoa na frente"}`))
	if err != nil {
		t.Fatal(err)
	}
	if code, m, _ := admin.do("GET", "/api/notify/pending/"+id, ""); code != http.StatusOK || m["title"] != "Pessoa na frente" {
		t.Fatalf("owner fetch: HTTP %d %v", code, m)
	}
	viewer := newClient(t, ts.URL)
	viewer.do("POST", "/api/login", `{"username":"viewer","password":"viewer password"}`)
	if code, _, _ := viewer.do("GET", "/api/notify/pending/"+id, ""); code != http.StatusNotFound {
		t.Fatalf("other user's notice: HTTP %d", code)
	}
	s.push.mu.Lock()
	v := s.push.pending[id]
	v.expires = time.Now().Add(-time.Second)
	s.push.pending[id] = v
	s.push.mu.Unlock()
	if code, _, _ := admin.do("GET", "/api/notify/pending/"+id, ""); code != http.StatusNotFound {
		t.Fatalf("expired notice: HTTP %d", code)
	}
}
