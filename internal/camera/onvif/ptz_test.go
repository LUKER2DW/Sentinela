package onvif

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestVelocity(t *testing.T) {
	for _, tc := range []struct {
		op, dir string
		speed   int
		inv     Invert
		x, y    float64
		zoom    bool
		err     error
	}{
		{"move", "up", 8, Invert{}, 0, 1, false, nil},
		{"move", "down_left", 4, Invert{}, -0.5, -0.5, false, nil},
		{"move", "up_right", 4, Invert{Pan: true, Tilt: true}, -0.5, -0.5, false, nil},
		{"zoom", "in", 2, Invert{}, 0.25, 0, true, nil},
		{"zoom", "out", 2, Invert{}, -0.25, 0, true, nil},
		{"move", "sideways", 4, Invert{}, 0, 0, false, ErrInvalidCommand},
		{"move", "up_up_up", 4, Invert{}, 0, 0, false, ErrInvalidCommand},
		{"move", "up", 9, Invert{}, 0, 0, false, ErrInvalidCommand},
		{"focus", "near", 4, Invert{}, 0, 0, false, ErrUnsupported},
	} {
		x, y, zoom, err := Velocity(tc.op, tc.dir, tc.speed, tc.inv)
		if err != tc.err || x != tc.x || y != tc.y || zoom != tc.zoom {
			t.Errorf("%s %s %d: got %v,%v,%v,%v", tc.op, tc.dir, tc.speed, x, y, zoom, err)
		}
	}
}

func TestParsePresets(t *testing.T) {
	data := []byte(`<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Body><GetPresetsResponse>` +
		`<Preset token="1"><Name>Gate</Name></Preset>` +
		`<Preset token="abc"><Name>Preset 3</Name></Preset>` +
		`<Preset token="Preset_7"><Name>Yard</Name></Preset>` +
		`<Preset token="2"><Name>2</Name></Preset>` +
		`<Preset token=""><Name>broken</Name></Preset>` +
		`</GetPresetsResponse></s:Body></s:Envelope>`)
	got, err := ParsePresets(data)
	if err != nil {
		t.Fatal(err)
	}
	want := map[int]string{1: "1", 2: "2", 3: "abc", 4: "Preset_7"}
	if len(got) != len(want) {
		t.Fatalf("got %d presets: %+v", len(got), got)
	}
	for _, p := range got {
		if want[p.ID] != p.token {
			t.Errorf("preset %d has token %q, want %q", p.ID, p.token, want[p.ID])
		}
	}
}

func TestPTZProfile(t *testing.T) {
	data := []byte(`<Envelope><Body><GetProfilesResponse>` +
		`<Profiles token="main"><Name>main</Name></Profiles>` +
		`<Profiles token="ptzprof"><PTZConfiguration token="p1"/></Profiles>` +
		`</GetProfilesResponse></Body></Envelope>`)
	if tok, err := PTZProfile(data); err != nil || tok != "ptzprof" {
		t.Errorf("got %q, %v", tok, err)
	}
	if _, err := PTZProfile([]byte(`<Envelope><Body><GetProfilesResponse/></Body></Envelope>`)); err != ErrNoPTZ {
		t.Errorf("no profiles: %v", err)
	}
}

// fakePTZ is a camera whose services live on vendor paths, reported by
// GetCapabilities with the wrong host (as some firmware does).
type fakePTZ struct {
	mu        sync.Mutex
	calls     []string
	noTimeout bool
}

func (f *fakePTZ) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body := string(b)
		op := body[strings.Index(body, "<s:Body><")+len("<s:Body><"):]
		op = op[:strings.IndexAny(op, " />")]
		f.mu.Lock()
		f.calls = append(f.calls, r.URL.Path+" "+op)
		f.mu.Unlock()
		if op != "GetSystemDateAndTime" && !strings.Contains(body, "UsernameToken") {
			http.Error(w, "auth", http.StatusUnauthorized)
			return
		}
		reply := func(s string) {
			_, _ = io.WriteString(w, `<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Body>`+s+`</s:Body></s:Envelope>`)
		}
		switch op {
		case "GetSystemDateAndTime":
			now := time.Now().UTC()
			reply(`<GetSystemDateAndTimeResponse><SystemDateAndTime><UTCDateTime><Date><Year>` + strconv.Itoa(now.Year()) + `</Year><Month>` +
				strconv.Itoa(int(now.Month())) + `</Month><Day>` + strconv.Itoa(now.Day()) + `</Day></Date><Time><Hour>` + strconv.Itoa(now.Hour()) +
				`</Hour><Minute>` + strconv.Itoa(now.Minute()) + `</Minute><Second>` + strconv.Itoa(now.Second()) + `</Second></Time></UTCDateTime></SystemDateAndTime></GetSystemDateAndTimeResponse>`)
		case "GetCapabilities":
			reply(`<GetCapabilitiesResponse><Capabilities><Media><XAddr>http://10.9.9.9/onvif/Media</XAddr></Media>` +
				`<PTZ><XAddr>http://10.9.9.9/onvif/PTZ</XAddr></PTZ></Capabilities></GetCapabilitiesResponse>`)
		case "GetProfiles":
			reply(`<GetProfilesResponse><Profiles token="prof1"><PTZConfiguration token="c"/></Profiles></GetProfilesResponse>`)
		case "ContinuousMove":
			if strings.Contains(body, "<Timeout>") && f.noTimeout {
				w.WriteHeader(http.StatusInternalServerError)
				reply(`<s:Fault><s:Reason><s:Text>Timeout not supported</s:Text></s:Reason></s:Fault>`)
				return
			}
			reply(`<ContinuousMoveResponse/>`)
		case "GetPresets":
			reply(`<GetPresetsResponse><Preset token="tok5"><Name>Preset 5</Name></Preset></GetPresetsResponse>`)
		default:
			reply(`<` + op + `Response/>`)
		}
	}
}

func TestPTZFlow(t *testing.T) {
	f := &fakePTZ{noTimeout: true}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()
	p := &PTZ{c: &Client{Base: srv.URL, User: "u", Password: "p", HTTP: srv.Client()}, Deadman: 50 * time.Millisecond}
	defer p.Close()
	ctx := context.Background()
	if err := p.Start(ctx, "move", "left", 4); err != nil {
		t.Fatal(err)
	}
	if !p.noTimeout {
		t.Error("the Timeout refusal should be remembered")
	}
	time.Sleep(200 * time.Millisecond) // the deadman stops the move
	if err := p.Preset(ctx, "goto", 5); err != nil {
		t.Fatal(err)
	}
	if err := p.Preset(ctx, "goto", 6); err != ErrInvalidCommand {
		t.Errorf("goto an empty preset: %v", err)
	}
	if err := p.Preset(ctx, "set", 6); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	joined := strings.Join(f.calls, "\n")
	for _, want := range []string{"/onvif/PTZ ContinuousMove", "/onvif/PTZ Stop", "/onvif/Media GetProfiles", "/onvif/PTZ GotoPreset", "/onvif/PTZ SetPreset"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in calls:\n%s", want, joined)
		}
	}
	if strings.Count(joined, "GetCapabilities") != 1 || strings.Count(joined, "GetProfiles") != 1 {
		t.Errorf("capabilities and profile should be read once:\n%s", joined)
	}
}

// A configured device path only ever names a path on the camera: a
// leading "//" cannot turn it into another host.
func TestDevicePathStaysOnHost(t *testing.T) {
	for path, want := range map[string]string{
		"":                      "http://10.0.0.5:80/onvif/device_service",
		"/onvif/Device":         "http://10.0.0.5:80/onvif/Device",
		"//evil.example/x":      "http://10.0.0.5:80/evil.example/x",
		"/onvif/device_service": "http://10.0.0.5:80/onvif/device_service",
	} {
		if got := NewClient("10.0.0.5", 80, "u", "p").WithDevicePath(path).device(); got != want {
			t.Errorf("%q: %s, want %s", path, got, want)
		}
	}
}
