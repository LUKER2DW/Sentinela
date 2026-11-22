package onvif

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ONVIF PTZ for cameras of any brand (PTZ service, ver20): held moves and
// zoom with ContinuousMove/Stop, and presets with GetPresets, GotoPreset,
// SetPreset and RemovePreset. These are the only PTZ operations Sentinela
// sends (docs/CAMERAS.md); focus and iris (Imaging service) are not used.
// A held move stops by itself after Deadman unless renewed, and asks the
// camera to stop on its own too (ContinuousMove Timeout) in case Sentinela
// goes away mid-move.

const ptzNS = "http://www.onvif.org/ver20/ptz/wsdl"

var (
	// ErrInvalidCommand is a move or preset request Sentinela refuses.
	ErrInvalidCommand = errors.New("invalid PTZ command")
	// ErrUnsupported is an operation ONVIF PTZ cannot do here (focus, iris).
	ErrUnsupported = errors.New("not supported over ONVIF PTZ")
	// ErrNoPTZ: the camera reports no PTZ service or profile.
	ErrNoPTZ = errors.New("the camera reports no ONVIF PTZ")
)

// Invert says which axes a mount reverses.
type Invert struct{ Pan, Tilt bool }

// PTZPreset is one saved position as the API shows it.
type PTZPreset struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// PTZ moves one camera over ONVIF. Calls are serialized per camera.
type PTZ struct {
	c       *Client
	invert  Invert
	Deadman time.Duration

	mu      sync.Mutex
	profile string
	moving  bool
	timer   *time.Timer
	// noTimeout: the camera faulted on ContinuousMove's Timeout, send none.
	noTimeout bool
}

// NewPTZ returns a PTZ driver for host:port. Its requests time out
// quickly: a move must not hang behind a slow camera.
func NewPTZ(host string, port int, devicePath, user, pw string, inv Invert) *PTZ {
	c := NewClient(host, port, user, pw).WithDevicePath(devicePath)
	c.HTTP = &http.Client{Timeout: 5 * time.Second}
	return &PTZ{c: c, invert: inv, Deadman: time.Second}
}

func (p *PTZ) ptzURL(ctx context.Context) string {
	return p.c.service(ctx, "ptz", "/onvif/ptz_service")
}

// profileLocked finds (once) the media profile that carries the PTZ
// configuration; the first profile when none says.
func (p *PTZ) profileLocked(ctx context.Context) (string, error) {
	if p.profile != "" {
		return p.profile, nil
	}
	data, err := p.c.call(ctx, p.c.mediaService(ctx), "", `<GetProfiles xmlns="`+mediaNS+`"/>`)
	if err != nil {
		return "", err
	}
	tok, err := PTZProfile(data)
	if err != nil {
		return "", err
	}
	p.profile = tok
	return tok, nil
}

// PTZProfile picks the profile token PTZ commands use from a
// GetProfilesResponse.
func PTZProfile(data []byte) (string, error) {
	var r struct {
		Profiles []struct {
			Token string `xml:"token,attr"`
			PTZ   *struct {
				Token string `xml:"token,attr"`
			} `xml:"PTZConfiguration"`
		} `xml:"Body>GetProfilesResponse>Profiles"`
	}
	if err := xml.Unmarshal(data, &r); err != nil {
		return "", fmt.Errorf("onvif: parse profiles: %w", err)
	}
	first := ""
	for _, pr := range r.Profiles {
		if pr.Token == "" {
			continue
		}
		if pr.PTZ != nil {
			return pr.Token, nil
		}
		if first == "" {
			first = pr.Token
		}
	}
	if first == "" {
		return "", ErrNoPTZ
	}
	return first, nil
}

// Velocity turns (op, dir, speed 1..8) into ONVIF velocity vectors, with
// the mount's inversion applied. zoom is only meaningful when isZoom.
func Velocity(op, dir string, speed int, inv Invert) (x, y float64, isZoom bool, err error) {
	if speed < 1 || speed > 8 {
		return 0, 0, false, ErrInvalidCommand
	}
	v := float64(speed) / 8
	switch op {
	case "move":
		parts := strings.Split(dir, "_")
		if len(parts) > 2 {
			return 0, 0, false, ErrInvalidCommand
		}
		for _, d := range parts {
			switch d {
			case "up":
				y = v
			case "down":
				y = -v
			case "left":
				x = -v
			case "right":
				x = v
			default:
				return 0, 0, false, ErrInvalidCommand
			}
		}
		if inv.Pan {
			x = -x
		}
		if inv.Tilt {
			y = -y
		}
		return x, y, false, nil
	case "zoom":
		switch dir {
		case "in":
			return v, 0, true, nil
		case "out":
			return -v, 0, true, nil
		}
		return 0, 0, false, ErrInvalidCommand
	case "focus", "iris":
		return 0, 0, false, ErrUnsupported
	}
	return 0, 0, false, ErrInvalidCommand
}

func num(f float64) string { return strconv.FormatFloat(f, 'f', 3, 64) }

// Start begins (or renews) a held movement; it stops after Deadman unless
// Start is called again or Stop is called.
func (p *PTZ) Start(ctx context.Context, op, dir string, speed int) error {
	x, y, zoom, err := Velocity(op, dir, speed, p.invert)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	prof, err := p.profileLocked(ctx)
	if err != nil {
		return err
	}
	vel := `<PanTilt xmlns="` + schemaN + `" x="` + num(x) + `" y="` + num(y) + `"/>`
	if zoom {
		vel = `<Zoom xmlns="` + schemaN + `" x="` + num(x) + `"/>`
	}
	move := func(timeout string) error {
		body := `<ContinuousMove xmlns="` + ptzNS + `"><ProfileToken>` + xmlEscape(prof) + `</ProfileToken>` +
			`<Velocity>` + vel + `</Velocity>` + timeout + `</ContinuousMove>`
		_, err := p.c.call(ctx, p.ptzURL(ctx), "", body)
		return err
	}
	if p.noTimeout {
		err = move("")
	} else if err = move(`<Timeout>PT2S</Timeout>`); err != nil && ctx.Err() == nil && strings.Contains(err.Error(), "HTTP") {
		// Some firmware refuses the optional Timeout: retry once without it
		// and remember (the deadman still stops the move).
		if err2 := move(""); err2 == nil {
			p.noTimeout, err = true, nil
		}
	}
	if err != nil {
		return err
	}
	p.moving = true
	if p.timer == nil {
		p.timer = time.AfterFunc(p.Deadman, p.deadman)
	} else {
		p.timer.Reset(p.Deadman)
	}
	return nil
}

func (p *PTZ) deadman() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.moving {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = p.stopLocked(ctx)
}

// Stop ends the current movement, if any.
func (p *PTZ) Stop(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stopLocked(ctx)
}

func (p *PTZ) stopLocked(ctx context.Context) error {
	if !p.moving {
		return nil
	}
	if p.timer != nil {
		p.timer.Stop()
	}
	prof, err := p.profileLocked(ctx)
	if err != nil {
		return err
	}
	body := `<Stop xmlns="` + ptzNS + `"><ProfileToken>` + xmlEscape(prof) + `</ProfileToken>` +
		`<PanTilt>true</PanTilt><Zoom>true</Zoom></Stop>`
	if _, err := p.c.call(ctx, p.ptzURL(ctx), "", body); err != nil {
		return err
	}
	p.moving = false
	return nil
}

// Nudge moves for d (100 ms..1 s) and stops.
func (p *PTZ) Nudge(ctx context.Context, op, dir string, speed int, d time.Duration) error {
	if d < 100*time.Millisecond || d > time.Second {
		return ErrInvalidCommand
	}
	if err := p.Start(ctx, op, dir, speed); err != nil {
		return err
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return p.Stop(stopCtx)
}

// onvifPreset is a camera preset with the number Sentinela shows for it.
type onvifPreset struct {
	PTZPreset
	token string
}

var presetNumber = regexp.MustCompile(`^(?i:preset)?\s*0*([1-9][0-9]{0,2})$`)

// ParsePresets reads a GetPresetsResponse and numbers the presets 1..255:
// a name that is a number ("3", "Preset 3": what SetPreset below writes)
// wins, then a numeric token, then the next free number in camera order.
func ParsePresets(data []byte) ([]onvifPreset, error) {
	var r struct {
		Presets []struct {
			Token string `xml:"token,attr"`
			Name  string `xml:"Name"`
		} `xml:"Body>GetPresetsResponse>Preset"`
	}
	if err := xml.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("onvif: parse presets: %w", err)
	}
	used := map[int]bool{}
	ids := make([]int, len(r.Presets))
	pick := func(s string) int {
		if m := presetNumber.FindStringSubmatch(strings.TrimSpace(s)); m != nil {
			if n, _ := strconv.Atoi(m[1]); n <= 255 && !used[n] {
				return n
			}
		}
		return 0
	}
	for pass := 0; pass < 2; pass++ {
		for i, pr := range r.Presets {
			if ids[i] != 0 || pr.Token == "" {
				continue
			}
			src := pr.Name
			if pass == 1 {
				src = pr.Token
			}
			if n := pick(src); n != 0 {
				ids[i], used[n] = n, true
			}
		}
	}
	next := 1
	var out []onvifPreset
	for i, pr := range r.Presets {
		if pr.Token == "" {
			continue
		}
		if ids[i] == 0 {
			for next <= 255 && used[next] {
				next++
			}
			if next > 255 {
				break
			}
			ids[i], used[next] = next, true
		}
		out = append(out, onvifPreset{PTZPreset: PTZPreset{ID: ids[i], Name: strings.TrimSpace(pr.Name)}, token: pr.Token})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (p *PTZ) presetsLocked(ctx context.Context) (string, []onvifPreset, error) {
	prof, err := p.profileLocked(ctx)
	if err != nil {
		return "", nil, err
	}
	data, err := p.c.call(ctx, p.ptzURL(ctx), "", `<GetPresets xmlns="`+ptzNS+`"><ProfileToken>`+xmlEscape(prof)+`</ProfileToken></GetPresets>`)
	if err != nil {
		return "", nil, err
	}
	list, err := ParsePresets(data)
	return prof, list, err
}

// Presets lists the camera's saved positions (read-only).
func (p *PTZ) Presets(ctx context.Context) ([]PTZPreset, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, list, err := p.presetsLocked(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]PTZPreset, len(list))
	for i, pr := range list {
		out[i] = pr.PTZPreset
	}
	return out, nil
}

// Preset runs goto, set or clear on preset id (1..255).
func (p *PTZ) Preset(ctx context.Context, action string, id int) error {
	if id < 1 || id > 255 || (action != "goto" && action != "set" && action != "clear") {
		return ErrInvalidCommand
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	prof, list, err := p.presetsLocked(ctx)
	if err != nil {
		return err
	}
	token := ""
	for _, pr := range list {
		if pr.ID == id {
			token = pr.token
		}
	}
	profTag := `<ProfileToken>` + xmlEscape(prof) + `</ProfileToken>`
	var body string
	switch action {
	case "goto":
		if token == "" {
			return ErrInvalidCommand
		}
		body = `<GotoPreset xmlns="` + ptzNS + `">` + profTag + `<PresetToken>` + xmlEscape(token) + `</PresetToken></GotoPreset>`
	case "set":
		// A new preset is named "Preset N" so it keeps number N; an
		// existing one is overwritten in place (same token).
		body = `<SetPreset xmlns="` + ptzNS + `">` + profTag + `<PresetName>Preset ` + strconv.Itoa(id) + `</PresetName>`
		if token != "" {
			body += `<PresetToken>` + xmlEscape(token) + `</PresetToken>`
		}
		body += `</SetPreset>`
	case "clear":
		if token == "" {
			return nil // nothing saved there
		}
		body = `<RemovePreset xmlns="` + ptzNS + `">` + profTag + `<PresetToken>` + xmlEscape(token) + `</PresetToken></RemovePreset>`
	}
	_, err = p.c.call(ctx, p.ptzURL(ctx), "", body)
	return err
}

// Close stops the deadman timer.
func (p *PTZ) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.timer != nil {
		p.timer.Stop()
	}
}
