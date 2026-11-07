// Package dvrip is a clean-room client for the XMEye/Xiongmai "Sofia" DVRIP
// protocol (TCP 34567). Only the operations allow-listed in docs/CAMERA.md
// §1.3 are exposed.
package dvrip

import (
	"context"
	"crypto/md5"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

// Message ids (docs/CAMERA.md §1.3).
const (
	MsgLogin     = 1000
	MsgSysInfo   = 1020
	MsgSetConfig = 1040
	MsgGetConfig = 1042
	MsgPTZ       = 1400
)

const (
	headerLen  = 20
	maxPayload = 1 << 20
	// DefaultTimeout bounds every request (AGENTS.md §6).
	DefaultTimeout = 5 * time.Second
)

// ErrRejected is returned when the camera answers with a non-OK Ret code.
var ErrRejected = errors.New("dvrip: request rejected")

// Conn is one authenticated DVRIP session. It is not safe for concurrent use;
// the Controller serializes access.
type Conn struct {
	c       net.Conn
	session uint32
	seq     uint32
	timeout time.Duration
}

// Dial connects and logs in.
func Dial(ctx context.Context, addr, user, password string) (*Conn, error) {
	d := net.Dialer{Timeout: DefaultTimeout}
	nc, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dvrip dial: %w", err)
	}
	c := &Conn{c: nc, timeout: DefaultTimeout}
	resp, err := c.Request(ctx, MsgLogin, map[string]any{
		"EncryptType": "MD5", "LoginType": "DVRIP-Web", "UserName": user, "PassWord": SofiaHash(password),
	})
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("dvrip login: %w", err)
	}
	code := Ret(resp)
	if code != 100 && code != 515 {
		nc.Close()
		return nil, fmt.Errorf("dvrip login: %w (code %d)", ErrRejected, code)
	}
	sid, _ := resp["SessionID"].(string)
	v, err := strconv.ParseUint(strings.TrimPrefix(strings.ToLower(sid), "0x"), 16, 32)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("dvrip login: bad session id")
	}
	c.session = uint32(v)
	return c, nil
}

// Close closes the TCP connection.
func (c *Conn) Close() error { return c.c.Close() }

// SessionHex is the session id in the "0x%08X" form the camera expects in payloads.
func (c *Conn) SessionHex() string { return fmt.Sprintf("0x%08X", c.session) }

// Request sends one message and reads one response.
func (c *Conn) Request(ctx context.Context, msg uint16, payload map[string]any) (map[string]any, error) {
	deadline := time.Now().Add(c.timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = c.c.SetDeadline(deadline)
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	body = append(body, '\n', 0)
	if _, err := c.c.Write(EncodeHeader(c.session, c.seq, msg, uint32(len(body)))); err != nil {
		return nil, err
	}
	if _, err := c.c.Write(body); err != nil {
		return nil, err
	}
	c.seq++
	for {
		h, data, err := ReadPacket(c.c)
		if err != nil {
			return nil, err
		}
		// Skip unsolicited packets (e.g. alarm pushes) that are not our reply.
		if h.Msg != msg+1 && h.Msg != msg {
			continue
		}
		out := map[string]any{}
		trimmed := strings.TrimRight(string(data), "\x00\n ")
		if trimmed == "" {
			return out, nil
		}
		if err := json.Unmarshal([]byte(trimmed), &out); err != nil {
			return nil, fmt.Errorf("dvrip: decode reply: %w", err)
		}
		return out, nil
	}
}

// Header is a decoded DVRIP header.
type Header struct {
	Session uint32
	Seq     uint32
	Msg     uint16
	Len     uint32
}

// EncodeHeader builds the 20-byte header.
func EncodeHeader(session, seq uint32, msg uint16, n uint32) []byte {
	b := make([]byte, headerLen)
	b[0] = 0xFF
	binary.LittleEndian.PutUint32(b[4:], session)
	binary.LittleEndian.PutUint32(b[8:], seq)
	binary.LittleEndian.PutUint16(b[14:], msg)
	binary.LittleEndian.PutUint32(b[16:], n)
	return b
}

// ReadPacket reads one header + payload.
func ReadPacket(r io.Reader) (Header, []byte, error) {
	hb := make([]byte, headerLen)
	if _, err := io.ReadFull(r, hb); err != nil {
		return Header{}, nil, err
	}
	if hb[0] != 0xFF {
		return Header{}, nil, errors.New("dvrip: bad magic")
	}
	h := Header{
		Session: binary.LittleEndian.Uint32(hb[4:]),
		Seq:     binary.LittleEndian.Uint32(hb[8:]),
		Msg:     binary.LittleEndian.Uint16(hb[14:]),
		Len:     binary.LittleEndian.Uint32(hb[16:]),
	}
	if h.Len > maxPayload {
		return Header{}, nil, errors.New("dvrip: payload too large")
	}
	data := make([]byte, h.Len)
	if _, err := io.ReadFull(r, data); err != nil {
		return Header{}, nil, err
	}
	return h, data, nil
}

// SofiaHash is the XMEye 8-character folded MD5 password hash.
func SofiaHash(pw string) string {
	const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	d := md5.Sum([]byte(pw))
	out := make([]byte, 8)
	for i := range out {
		out[i] = alphabet[(int(d[2*i])+int(d[2*i+1]))%len(alphabet)]
	}
	return string(out)
}

// Ret extracts the numeric Ret code (0 if missing).
func Ret(m map[string]any) int {
	if v, ok := m["Ret"].(float64); ok {
		return int(v)
	}
	return 0
}

// SystemInfo reads the SystemInfo object.
func (c *Conn) SystemInfo(ctx context.Context) (map[string]any, error) {
	resp, err := c.Request(ctx, MsgSysInfo, map[string]any{"Name": "SystemInfo", "SessionID": c.SessionHex()})
	if err != nil {
		return nil, err
	}
	if Ret(resp) != 100 {
		return nil, fmt.Errorf("SystemInfo: %w (code %d)", ErrRejected, Ret(resp))
	}
	info, _ := resp["SystemInfo"].(map[string]any)
	return info, nil
}

// GetConfig reads one named config and returns the raw value under that name.
func (c *Conn) GetConfig(ctx context.Context, name string) (any, error) {
	resp, err := c.Request(ctx, MsgGetConfig, map[string]any{"Name": name, "SessionID": c.SessionHex()})
	if err != nil {
		return nil, err
	}
	if Ret(resp) != 100 {
		return nil, fmt.Errorf("get %s: %w (code %d)", name, ErrRejected, Ret(resp))
	}
	v, ok := resp[name]
	if !ok {
		return nil, fmt.Errorf("get %s: missing object", name)
	}
	return v, nil
}

// setConfig writes one named config object. Callers must follow the
// read-modify-verify-restore rule (AGENTS.md §2.6); see Controller.setParam.
func (c *Conn) setConfig(ctx context.Context, name string, value any) error {
	resp, err := c.Request(ctx, MsgSetConfig, map[string]any{"Name": name, "SessionID": c.SessionHex(), name: value})
	if err != nil {
		return err
	}
	if Ret(resp) != 100 {
		return fmt.Errorf("set %s: %w (code %d)", name, ErrRejected, Ret(resp))
	}
	return nil
}

// ptz sends one OPPTZControl command with the envelope from docs/CAMERA.md.
func (c *Conn) ptz(ctx context.Context, command string, step, preset int) error {
	resp, err := c.Request(ctx, MsgPTZ, map[string]any{
		"Name":      "OPPTZControl",
		"SessionID": c.SessionHex(),
		"OPPTZControl": map[string]any{
			"Command": command,
			"Parameter": map[string]any{
				"AUX":      map[string]any{"Number": 0, "Status": "On"},
				"Channel":  0,
				"MenuOpts": "Enter",
				"POINT":    map[string]any{"bottom": 0, "left": 0, "right": 0, "top": 0},
				"Pattern":  "SetBegin",
				"Preset":   preset,
				"Step":     step,
				"Tour":     0,
			},
		},
	})
	if err != nil {
		return err
	}
	if Ret(resp) != 100 {
		return fmt.Errorf("PTZ %s: %w (code %d)", command, ErrRejected, Ret(resp))
	}
	return nil
}
