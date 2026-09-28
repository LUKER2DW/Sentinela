package dvrip

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"
)

// Controller owns the single DVRIP session of one camera and serializes all
// requests (AGENTS.md §6). The session is opened lazily and closed after idle.
type Controller struct {
	Addr          string
	User          string
	Password      string
	InvertPanTilt bool
	Log           *slog.Logger

	// IdleClose closes the session after this long without requests.
	IdleClose time.Duration
	// Deadman stops a held movement when no keepalive arrives in time.
	Deadman time.Duration

	dial func(ctx context.Context, addr, user, pw string) (*Conn, error)

	mu        sync.Mutex
	conn      *Conn
	lastUse   time.Time
	idleTimer *time.Timer
	model     string

	moving    *activeMove
	moveTimer *time.Timer
}

type activeMove struct {
	command string
	speed   int
}

// Errors returned to API callers (safe to show).
var (
	ErrUnsupportedModel = errors.New("camera model is not approved for DVRIP control")
	ErrInvalidCommand   = errors.New("invalid PTZ command")
)

// approvedModels lists models whose control surface is verified (docs/CAMERA.md).
var approvedModels = []string{"IPC_NT98566"}

// NewController returns a controller with default timings.
func NewController(addr, user, password string, invert bool, log *slog.Logger) *Controller {
	return &Controller{Addr: addr, User: user, Password: password, InvertPanTilt: invert, Log: log,
		IdleClose: 30 * time.Second, Deadman: time.Second, dial: Dial}
}

// with runs fn with a live, logged-in session, reconnecting once on I/O errors.
func (c *Controller) with(ctx context.Context, fn func(*Conn) error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.withLocked(ctx, fn)
}

func (c *Controller) withLocked(ctx context.Context, fn func(*Conn) error) error {
	for attempt := 0; attempt < 2; attempt++ {
		if c.conn == nil {
			conn, err := c.dial(ctx, c.Addr, c.User, c.Password)
			if err != nil {
				return err
			}
			c.conn = conn
			if c.model == "" {
				if info, err := conn.SystemInfo(ctx); err == nil {
					c.model, _ = info["DeviceModel"].(string)
				}
			}
		}
		err := fn(c.conn)
		c.touch()
		if err == nil || !isNetErr(err) || ctx.Err() != nil {
			return err
		}
		// Network error: drop the session and retry once with a fresh login.
		c.conn.Close()
		c.conn = nil
		if attempt == 1 {
			return err
		}
	}
	return nil
}

// isNetErr reports errors that mean the session is dead (worth one re-login).
func isNetErr(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed)
}

func (c *Controller) touch() {
	c.lastUse = time.Now()
	if c.idleTimer == nil {
		c.idleTimer = time.AfterFunc(c.IdleClose, c.idleCheck)
		return
	}
	c.idleTimer.Reset(c.IdleClose)
}

func (c *Controller) idleCheck() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil || c.moving != nil {
		if c.moving != nil && c.idleTimer != nil {
			c.idleTimer.Reset(c.IdleClose)
		}
		return
	}
	if time.Since(c.lastUse) >= c.IdleClose {
		c.conn.Close()
		c.conn = nil
	}
}

// Close stops any movement and closes the session.
func (c *Controller) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.moving != nil {
		ctx, cancel := context.WithTimeout(context.Background(), DefaultTimeout)
		_ = c.stopLocked(ctx)
		cancel()
	}
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
}

func (c *Controller) requireApproved() error {
	for _, m := range approvedModels {
		if strings.Contains(strings.ToUpper(c.model), m) {
			return nil
		}
	}
	return ErrUnsupportedModel
}

// State is the readable control state of a camera.
type State struct {
	Model      string   `json:"model"`
	Firmware   string   `json:"firmware"`
	WhiteLight bool     `json:"white_light"`
	DayNight   string   `json:"day_night"`
	Presets    []Preset `json:"presets"`
	Approved   bool     `json:"approved"`
}

// Preset is a native camera preset.
type Preset struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// DayNightModes maps semantic names to firmware values (docs/CAMERA.md §1.3).
var DayNightModes = map[string]string{
	"auto":        "0x00000000",
	"color":       "0x00000001",
	"black_white": "0x00000002",
	"intelligent": "0x00000003",
}

const cameraParam = "Camera.Param.[0]"

// State reads model, light, day/night and presets (read-only).
func (c *Controller) State(ctx context.Context) (*State, error) {
	st := &State{Presets: []Preset{}}
	err := c.with(ctx, func(conn *Conn) error {
		info, err := conn.SystemInfo(ctx)
		if err != nil {
			return err
		}
		st.Model, _ = info["DeviceModel"].(string)
		st.Firmware, _ = info["SoftWareVersion"].(string)
		c.model = st.Model
		st.Approved = c.requireApproved() == nil
		if p, err := conn.GetConfig(ctx, cameraParam); err == nil {
			if obj, ok := p.(map[string]any); ok {
				st.WhiteLight = numeric(obj["WhiteLight"]) != 0
				raw, _ := obj["DayNightColor"].(string)
				for k, v := range DayNightModes {
					if strings.EqualFold(v, raw) {
						st.DayNight = k
					}
				}
			}
		}
		if p, err := conn.GetConfig(ctx, "Uart.PTZPreset"); err == nil {
			st.Presets = parsePresets(p)
		}
		return nil
	})
	return st, err
}

func parsePresets(v any) []Preset {
	out := []Preset{}
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case []any:
			for _, it := range x {
				walk(it)
			}
		case map[string]any:
			id := numeric(x["Id"])
			if id == 0 {
				id = numeric(x["Preset"])
			}
			if id >= 1 && id <= 255 {
				name, _ := x["Name"].(string)
				if name == "" {
					name = fmt.Sprintf("Preset %d", id)
				}
				out = append(out, Preset{ID: id, Name: name})
			}
		}
	}
	walk(v)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func numeric(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case string:
		var n int
		if strings.HasPrefix(strings.ToLower(x), "0x") {
			fmt.Sscanf(x[2:], "%x", &n)
		} else {
			fmt.Sscanf(x, "%d", &n)
		}
		return n
	case bool:
		if x {
			return 1
		}
	}
	return 0
}

// ---------- PTZ ----------

var verbs = map[string]map[string]string{
	"move": {"up": "DirectionUp", "down": "DirectionDown", "left": "DirectionLeft", "right": "DirectionRight",
		"up_left": "DirectionLeftUp", "up_right": "DirectionRightUp", "down_left": "DirectionLeftDown", "down_right": "DirectionRightDown"},
	"zoom":  {"in": "ZoomTile", "out": "ZoomWide"},
	"focus": {"near": "FocusNear", "far": "FocusFar"},
	"iris":  {"open": "IrisLarge", "close": "IrisSmall"},
}

// Command resolves an (op, dir) pair to a DVRIP verb, applying pan/tilt inversion.
func Command(op, dir string, invert bool) (string, error) {
	if op == "move" && invert {
		dir = invertDir(dir)
	}
	v, ok := verbs[op][dir]
	if !ok {
		return "", ErrInvalidCommand
	}
	return v, nil
}

func invertDir(d string) string {
	parts := strings.Split(d, "_")
	swap := map[string]string{"up": "down", "down": "up", "left": "right", "right": "left"}
	for i, p := range parts {
		if s, ok := swap[p]; ok {
			parts[i] = s
		}
	}
	return strings.Join(parts, "_")
}

// Start begins (or refreshes) a held movement. It auto-stops after Deadman
// unless Start is called again (keepalive) or Stop is called.
func (c *Controller) Start(ctx context.Context, op, dir string, speed int) error {
	cmd, err := Command(op, dir, c.InvertPanTilt)
	if err != nil {
		return err
	}
	if speed < 1 || speed > 8 {
		return ErrInvalidCommand
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.moving != nil && (c.moving.command != cmd || c.moving.speed != speed) {
		if err := c.stopLocked(ctx); err != nil {
			return err
		}
	}
	err = c.withLocked(ctx, func(conn *Conn) error {
		if err := c.requireApproved(); err != nil {
			return err
		}
		return conn.ptz(ctx, cmd, speed, 65535)
	})
	if err != nil {
		return err
	}
	c.moving = &activeMove{command: cmd, speed: speed}
	if c.moveTimer == nil {
		c.moveTimer = time.AfterFunc(c.Deadman, c.deadman)
	} else {
		c.moveTimer.Reset(c.Deadman)
	}
	return nil
}

func (c *Controller) deadman() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.moving == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), DefaultTimeout)
	defer cancel()
	if err := c.stopLocked(ctx); err != nil && c.Log != nil {
		c.Log.Warn("ptz deadman stop failed", "err", err)
	}
}

// Stop ends the current held movement, if any.
func (c *Controller) Stop(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stopLocked(ctx)
}

func (c *Controller) stopLocked(ctx context.Context) error {
	if c.moving == nil {
		return nil
	}
	m := c.moving
	if c.moveTimer != nil {
		c.moveTimer.Stop()
	}
	err := c.withLocked(ctx, func(conn *Conn) error { return conn.ptz(ctx, m.command, m.speed, -1) })
	if err == nil {
		c.moving = nil
	}
	return err
}

// Nudge moves for d (100 ms..1 s) and stops.
func (c *Controller) Nudge(ctx context.Context, op, dir string, speed int, d time.Duration) error {
	if d < 100*time.Millisecond || d > time.Second {
		return ErrInvalidCommand
	}
	if err := c.Start(ctx, op, dir, speed); err != nil {
		return err
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), DefaultTimeout)
	defer cancel()
	return c.Stop(stopCtx)
}

// Preset runs goto/set/clear on a native preset (1..255).
func (c *Controller) Preset(ctx context.Context, action string, id int) error {
	verb := map[string]string{"goto": "GotoPreset", "set": "SetPreset", "clear": "ClearPreset"}[action]
	if verb == "" || id < 1 || id > 255 {
		return ErrInvalidCommand
	}
	return c.with(ctx, func(conn *Conn) error {
		if err := c.requireApproved(); err != nil {
			return err
		}
		return conn.ptz(ctx, verb, 0, id)
	})
}

// ---------- persistent config writes ----------

// SetWhiteLight switches the white light on or off.
func (c *Controller) SetWhiteLight(ctx context.Context, on bool) error {
	v := float64(0)
	if on {
		v = 1
	}
	return c.setParam(ctx, "WhiteLight", v)
}

// SetDayNight applies a semantic day/night mode.
func (c *Controller) SetDayNight(ctx context.Context, mode string) error {
	v, ok := DayNightModes[mode]
	if !ok {
		return ErrInvalidCommand
	}
	return c.setParam(ctx, "DayNightColor", v)
}

// setParam implements read full object → change one field → write → read
// back → restore the exact original on any failure (AGENTS.md §2.6).
func (c *Controller) setParam(ctx context.Context, field string, value any) error {
	return c.with(ctx, func(conn *Conn) error {
		if err := c.requireApproved(); err != nil {
			return err
		}
		raw, err := conn.GetConfig(ctx, cameraParam)
		if err != nil {
			return err
		}
		baseline, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: unexpected shape", cameraParam)
		}
		if _, exists := baseline[field]; !exists {
			return fmt.Errorf("%s.%s not present on this camera", cameraParam, field)
		}
		desired := make(map[string]any, len(baseline))
		for k, v := range baseline {
			desired[k] = v
		}
		desired[field] = value
		restore := func(cause error) error {
			if rerr := conn.setConfig(ctx, cameraParam, baseline); rerr != nil {
				return fmt.Errorf("%w; restore failed: %v", cause, rerr)
			}
			return cause
		}
		if err := conn.setConfig(ctx, cameraParam, desired); err != nil {
			return restore(err)
		}
		back, err := conn.GetConfig(ctx, cameraParam)
		if err != nil {
			return restore(fmt.Errorf("read back: %w", err))
		}
		obj, _ := back.(map[string]any)
		if !reflect.DeepEqual(obj[field], value) {
			return restore(fmt.Errorf("%s read-back mismatch", field))
		}
		return nil
	})
}
