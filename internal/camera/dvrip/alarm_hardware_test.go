//go:build hardware

package dvrip

// Read-only probe of the DVRIP alarm channel (PLAN Phase 9, Waves 3–4).
// It logs in, subscribes to alarm pushes (message 1500, as python-dvr does)
// and prints every packet the camera sends for SENTINELA_PROBE_SECONDS.
// Nothing is written to the camera. Run it on the server so the password
// never leaves it:
//
//	go test -c -tags hardware -o dvrip-probe ./internal/camera/dvrip
//	sudo env $(cat /etc/sentinela/secrets.env) ./dvrip-probe -test.run TestAlarmProbe -test.v -test.timeout 0

import (
	"context"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	msgKeepAlive = 1006
	msgAlarmSet  = 1500
)

func TestAlarmProbe(t *testing.T) {
	addr := os.Getenv("SENTINELA_PROBE_ADDR")
	if addr == "" {
		addr = "192.168.1.20:34567"
	}
	user, pw := os.Getenv("CAM1_USER"), os.Getenv("CAM1_PASS")
	if user == "" || pw == "" {
		t.Skip("CAM1_USER/CAM1_PASS not set")
	}
	secs, _ := strconv.Atoi(os.Getenv("SENTINELA_PROBE_SECONDS"))
	if secs <= 0 {
		secs = 120
	}
	ctx := context.Background()
	c, err := Dial(ctx, addr, user, pw)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer c.Close()

	resp, err := c.Request(ctx, msgAlarmSet, map[string]any{"Name": "", "SessionID": c.SessionHex()})
	if err != nil {
		t.Fatalf("alarm subscribe: %v", err)
	}
	t.Logf("subscribe reply: %v", resp)

	var wmu sync.Mutex
	send := func(msg uint16, payload string) {
		wmu.Lock()
		defer wmu.Unlock()
		body := append([]byte(payload), '\n', 0)
		_ = c.c.SetWriteDeadline(time.Now().Add(DefaultTimeout))
		_, _ = c.c.Write(append(EncodeHeader(c.session, c.seq, msg, uint32(len(body))), body...))
		c.seq++
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		tk := time.NewTicker(10 * time.Second)
		defer tk.Stop()
		for {
			select {
			case <-done:
				return
			case <-tk.C:
				send(msgKeepAlive, `{"Name":"KeepAlive","SessionID":"`+c.SessionHex()+`"}`)
			}
		}
	}()

	end := time.Now().Add(time.Duration(secs) * time.Second)
	_ = c.c.SetReadDeadline(end)
	for {
		h, data, err := ReadPacket(c.c)
		if err != nil {
			if time.Now().After(end) {
				return
			}
			t.Fatalf("read: %v", err)
		}
		if h.Msg == msgKeepAlive+1 {
			continue
		}
		t.Logf("%s msg=%d %s", time.Now().UTC().Format(time.RFC3339), h.Msg, strings.TrimRight(string(data), "\x00\n "))
	}
}
