package api

import (
	"bufio"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/LUKER2DW/sentinela/internal/auth"
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
		if !strings.Contains(line, "Notifications are working") {
			t.Fatalf("stream data %q", line)
		}
	case <-deadline:
		t.Fatal("no notification on the stream")
	}
}

// An open notification stream from outside closes once the account turns
// two-step login on, when its session was made without a code (as every
// other long-lived stream does); a session made with the code stays.
func TestNotifyStreamClosesOnTwoStepEnrollment(t *testing.T) {
	shortRecheck(t)
	priv, srv := newTestServer(t)
	pub := newPublicServer(t, srv, ZonePublic)
	home := newClient(t, priv.URL)
	home.do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`)
	out := newClient(t, pub.URL)
	if code, _, body := out.do("POST", "/api/login", `{"username":"admin","password":"long enough pw"}`); code != 200 {
		t.Fatalf("login = %d %s", code, body)
	}
	open := func(c *client) <-chan string {
		req, _ := http.NewRequest("GET", c.base+"/api/notify/stream", nil)
		resp, err := c.hc.Do(req)
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("stream: %v %v", err, resp)
		}
		t.Cleanup(func() { resp.Body.Close() })
		lines := sseLines(bufio.NewReader(resp.Body))
		for l := range lines {
			if strings.HasPrefix(l, ": ready") {
				return lines
			}
		}
		t.Fatal("stream ended before it was ready")
		return nil
	}
	lines := open(out)
	// Still open over a few rechecks.
	select {
	case _, ok := <-lines:
		if !ok {
			t.Fatal("stream closed before enrollment")
		}
	case <-time.After(200 * time.Millisecond):
	}

	_, m, _ := home.do("POST", "/api/me/totp/setup", `{"password":"long enough pw"}`)
	secret, _ := m["secret"].(string)
	now := auth.TOTPStep(time.Now())
	c0, _ := auth.TOTPCode(secret, now)
	if code, _, body := home.do("POST", "/api/me/totp/enable", `{"code":"`+c0+`"}`); code != 200 {
		t.Fatalf("totp enable = %d %s", code, body)
	}
	deadline := time.After(3 * time.Second)
	for closed := false; !closed; {
		select {
		case _, ok := <-lines:
			closed = !ok
		case <-deadline:
			t.Fatal("password-only stream outlived two-step enrollment")
		}
	}

	// A session made with the code keeps its stream.
	withCode := newClient(t, pub.URL)
	c1, _ := auth.TOTPCode(secret, now+1)
	if code, _, body := withCode.do("POST", "/api/login", `{"username":"admin","password":"long enough pw","code":"`+c1+`"}`); code != 200 {
		t.Fatalf("login with code = %d %s", code, body)
	}
	lines = open(withCode)
	select {
	case _, ok := <-lines:
		if !ok {
			t.Fatal("two-step session's stream was closed")
		}
	case <-time.After(300 * time.Millisecond):
	}
}
