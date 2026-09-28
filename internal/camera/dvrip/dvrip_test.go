package dvrip

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeCamera emulates the DVRIP subset used by the driver.
type fakeCamera struct {
	t        *testing.T
	ln       net.Listener
	password string
	model    string

	mu        sync.Mutex
	param     map[string]any
	ptz       []string // "Command:Preset:Step"
	failSet   bool     // reject every set-config
	ignoreSet bool     // accept set-config but do not apply it
	sets      int
}

func newFake(t *testing.T) *fakeCamera {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeCamera{t: t, ln: ln, password: "secret", model: "IPC_NT98566_IPG-N4C-WQ2_S38",
		param: map[string]any{"WhiteLight": float64(0), "DayNightColor": "0x00000000", "PictureFlip": "0x00000001"}}
	go f.serve()
	t.Cleanup(func() { ln.Close() })
	return f
}

func (f *fakeCamera) addr() string { return f.ln.Addr().String() }

func (f *fakeCamera) serve() {
	for {
		c, err := f.ln.Accept()
		if err != nil {
			return
		}
		go f.handle(c)
	}
}

func (f *fakeCamera) reply(c net.Conn, h Header, v map[string]any) {
	b, _ := json.Marshal(v)
	b = append(b, '\n', 0)
	c.Write(EncodeHeader(h.Session, h.Seq, h.Msg+1, uint32(len(b))))
	c.Write(b)
}

func (f *fakeCamera) handle(c net.Conn) {
	defer c.Close()
	for {
		h, data, err := ReadPacket(c)
		if err != nil {
			return
		}
		var req map[string]any
		json.Unmarshal(bytes.TrimRight(data, "\x00\n"), &req)
		f.mu.Lock()
		switch h.Msg {
		case MsgLogin:
			if req["PassWord"] != SofiaHash(f.password) {
				f.reply(c, h, map[string]any{"Ret": 205})
			} else {
				f.reply(c, h, map[string]any{"Ret": 100, "SessionID": "0x0000002A"})
			}
		case MsgSysInfo:
			f.reply(c, h, map[string]any{"Ret": 100, "SystemInfo": map[string]any{"DeviceModel": f.model, "SoftWareVersion": "V5"}})
		case MsgGetConfig:
			name := req["Name"].(string)
			switch name {
			case cameraParam:
				cp := map[string]any{}
				for k, v := range f.param {
					cp[k] = v
				}
				f.reply(c, h, map[string]any{"Ret": 100, name: cp})
			case "Uart.PTZPreset":
				f.reply(c, h, map[string]any{"Ret": 100, name: []any{[]any{map[string]any{"Id": float64(1), "Name": "Portao"}}}})
			default:
				f.reply(c, h, map[string]any{"Ret": 607})
			}
		case MsgSetConfig:
			f.sets++
			if f.failSet {
				f.reply(c, h, map[string]any{"Ret": 103})
			} else {
				if !f.ignoreSet || f.sets > 1 {
					f.param = req[cameraParam].(map[string]any)
				}
				f.reply(c, h, map[string]any{"Ret": 100})
			}
		case MsgPTZ:
			op := req["OPPTZControl"].(map[string]any)
			p := op["Parameter"].(map[string]any)
			f.ptz = append(f.ptz, op["Command"].(string)+":"+jsonNum(p["Preset"])+":"+jsonNum(p["Step"]))
			f.reply(c, h, map[string]any{"Ret": 100})
		}
		f.mu.Unlock()
	}
}

func jsonNum(v any) string { b, _ := json.Marshal(v); return string(b) }

func (f *fakeCamera) ptzLog() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.ptz...)
}

func TestSofiaHashKnownVector(t *testing.T) {
	// Empty password hash is a widely published XMEye constant.
	if got := SofiaHash(""); got != "tlJwpbo6" {
		t.Fatalf("SofiaHash(\"\") = %q", got)
	}
}

func TestHeaderRoundTrip(t *testing.T) {
	b := EncodeHeader(0x2A, 7, MsgPTZ, 3)
	h, data, err := ReadPacket(bytes.NewReader(append(b, 'a', 'b', 'c')))
	if err != nil || h.Session != 0x2A || h.Seq != 7 || h.Msg != MsgPTZ || string(data) != "abc" {
		t.Fatalf("h=%+v data=%q err=%v", h, data, err)
	}
}

func TestLoginRejected(t *testing.T) {
	f := newFake(t)
	if _, err := Dial(context.Background(), f.addr(), "admin", "wrong"); err == nil {
		t.Fatal("expected login failure")
	}
}

func TestStateReadsLightDayNightPresets(t *testing.T) {
	f := newFake(t)
	c := NewController(f.addr(), "admin", "secret", true, nil)
	defer c.Close()
	st, err := c.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !st.Approved || st.WhiteLight || st.DayNight != "auto" || len(st.Presets) != 1 || st.Presets[0].Name != "Portao" {
		t.Fatalf("state=%+v", st)
	}
}

func TestCommandInversion(t *testing.T) {
	cases := []struct {
		op, dir string
		inv     bool
		want    string
	}{
		{"move", "left", true, "DirectionRight"},
		{"move", "up_left", true, "DirectionRightDown"},
		{"move", "up_left", false, "DirectionLeftUp"},
		{"zoom", "in", true, "ZoomTile"},
	}
	for _, c := range cases {
		got, err := Command(c.op, c.dir, c.inv)
		if err != nil || got != c.want {
			t.Errorf("Command(%s,%s,%v)=%s,%v want %s", c.op, c.dir, c.inv, got, err, c.want)
		}
	}
	if _, err := Command("move", "sideways", false); err == nil {
		t.Error("invalid dir accepted")
	}
	if _, err := Command("reboot", "", false); err == nil {
		t.Error("unknown op accepted")
	}
}

func TestHeldMoveDeadmanStops(t *testing.T) {
	f := newFake(t)
	c := NewController(f.addr(), "admin", "secret", true, nil)
	c.Deadman = 150 * time.Millisecond
	defer c.Close()
	if err := c.Start(context.Background(), "move", "left", 4); err != nil {
		t.Fatal(err)
	}
	time.Sleep(400 * time.Millisecond)
	got := strings.Join(f.ptzLog(), ",")
	if got != "DirectionRight:65535:4,DirectionRight:-1:4" {
		t.Fatalf("ptz log = %s", got)
	}
}

func TestNudgeStartsAndStops(t *testing.T) {
	f := newFake(t)
	c := NewController(f.addr(), "admin", "secret", false, nil)
	defer c.Close()
	if err := c.Nudge(context.Background(), "zoom", "in", 3, 120*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(f.ptzLog(), ","); got != "ZoomTile:65535:3,ZoomTile:-1:3" {
		t.Fatalf("ptz log = %s", got)
	}
	if err := c.Nudge(context.Background(), "zoom", "in", 3, 5*time.Second); err == nil {
		t.Fatal("over-long nudge accepted")
	}
}

func TestUnapprovedModelBlocked(t *testing.T) {
	f := newFake(t)
	f.model = "SOMETHING_ELSE"
	c := NewController(f.addr(), "admin", "secret", false, nil)
	defer c.Close()
	if err := c.Start(context.Background(), "move", "up", 2); err != ErrUnsupportedModel {
		t.Fatalf("err = %v", err)
	}
	if len(f.ptzLog()) != 0 {
		t.Fatal("command sent to unapproved model")
	}
}

func TestSetWhiteLightVerified(t *testing.T) {
	f := newFake(t)
	c := NewController(f.addr(), "admin", "secret", false, nil)
	defer c.Close()
	if err := c.SetWhiteLight(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if f.param["WhiteLight"] != float64(1) || f.param["PictureFlip"] != "0x00000001" {
		t.Fatalf("param=%v", f.param)
	}
}

func TestSetParamRestoresOnMismatch(t *testing.T) {
	f := newFake(t)
	f.ignoreSet = true // first write "succeeds" but is not applied → read-back mismatch
	c := NewController(f.addr(), "admin", "secret", false, nil)
	defer c.Close()
	err := c.SetDayNight(context.Background(), "color")
	if err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("err = %v", err)
	}
	if f.sets != 2 || f.param["DayNightColor"] != "0x00000000" {
		t.Fatalf("restore not performed: sets=%d param=%v", f.sets, f.param)
	}
}
