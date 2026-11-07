package api

import (
	"bufio"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestNotifyPrefsWants(t *testing.T) {
	p := defaultPrefs()
	for _, mode := range []string{ModeHome, ModeNight, ModeAway} {
		for _, typ := range EventTypes {
			if p.Wants(mode, typ) {
				t.Fatalf("default prefs notified for %s in %s", typ, mode)
			}
		}
	}
	p = NotifyPrefs{FollowMode: true, Types: []string{"human", "tamper"}}
	if p.Wants(ModeHome, "human") || !p.Wants(ModeHome, "tamper") {
		t.Fatal("selected types must follow the house mode")
	}
	p.FollowMode = false
	if !p.Wants(ModeHome, "human") || p.Wants(ModeAway, "motion") {
		t.Fatal("own list without the house mode")
	}
	if p.Enabled("login") {
		t.Fatal("login notice must require its own selection")
	}
	p.Types = append(p.Types, "login")
	if !p.Enabled("login") {
		t.Fatal("selected login notice was not enabled")
	}
}

func TestNotifyStreamAndPrefs(t *testing.T) {
	priv, _ := newTestServer(t)
	c := newClient(t, priv.URL)
	c.do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`)
	if code, m, _ := c.do("GET", "/api/me/notify", ""); code != 200 || len(m["prefs"].(map[string]any)["types"].([]any)) != 0 {
		t.Fatalf("new user notification defaults = %d %v", code, m)
	}
	code, m, body := c.do("PUT", "/api/me/notify", `{"follow_mode":false,"types":["human","bogus","tamper"]}`)
	if code != 200 || len(m["prefs"].(map[string]any)["types"].([]any)) != 2 {
		t.Fatalf("put prefs = %d %s", code, body)
	}
	if _, m, _ := c.do("GET", "/api/me/notify", ""); m["prefs"].(map[string]any)["follow_mode"] != false {
		t.Fatalf("get prefs = %v", m)
	}

	req, _ := http.NewRequest("GET", priv.URL+"/api/notify/stream", nil)
	resp, err := c.hc.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("stream: %v %v", err, resp)
	}
	defer resp.Body.Close()
	rd := bufio.NewReader(resp.Body)
	for { // wait until subscribed
		line, err := rd.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(line, ": ready") {
			break
		}
	}
	go func() {
		r, _ := http.NewRequest("POST", priv.URL+"/api/push/test", nil)
		r.Header.Set("X-CSRF-Token", c.csrf)
		if resp, err := c.hc.Do(r); err == nil {
			resp.Body.Close()
		}
	}()
	deadline := time.After(5 * time.Second)
	got := make(chan string, 1)
	go func() {
		for {
			line, err := rd.ReadString('\n')
			if err != nil {
				return
			}
			if strings.HasPrefix(line, "data: ") {
				got <- line
				return
			}
		}
	}()
	select {
	case line := <-got:
		if !strings.Contains(line, "Notificações funcionando") {
			t.Fatalf("stream data %q", line)
		}
	case <-deadline:
		t.Fatal("no notification on the stream")
	}
}
