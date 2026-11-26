package api

import (
	"bufio"
	"crypto/tls"
	"encoding/binary"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// HTTP/2 frame types and settings the tests use (RFC 9113).
const (
	h2Data         = 0x0
	h2Headers      = 0x1
	h2RSTStream    = 0x3
	h2Settings     = 0x4
	h2Ping         = 0x6
	h2GoAway       = 0x7
	h2WindowUpdate = 0x8

	h2FlagEndStream  = 0x1
	h2FlagAck        = 0x1
	h2FlagEndHeaders = 0x4

	h2SettingMaxConcurrentStreams = 0x3
	h2SettingInitialWindowSize    = 0x4
	h2SettingMaxFrameSize         = 0x5
	h2SettingMaxHeaderListSize    = 0x6
)

type h2frame struct {
	typ, flags byte
	stream     uint32
	payload    []byte
}

// h2peer is a minimal HTTP/2 client for misbehaving the way net/http's client
// never does: a zero flow-control window, request bodies nobody reads.
type h2peer struct {
	t      *testing.T
	c      *tls.Conn
	host   string
	frames chan h2frame
	wmu    sync.Mutex
}

// dialH2 opens an HTTP/2 connection to ts (a TLS test server) and sends the
// preface with settings (id → value).
func dialH2(t *testing.T, ts *httptest.Server, settings map[uint16]uint32) *h2peer {
	t.Helper()
	cfg := ts.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	cfg.NextProtos = []string{"h2"}
	c, err := tls.Dial("tcp", ts.Listener.Addr().String(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if p := c.ConnectionState().NegotiatedProtocol; p != "h2" {
		t.Fatalf("negotiated %q, want h2", p)
	}
	p := &h2peer{t: t, c: c, host: ts.Listener.Addr().String(), frames: make(chan h2frame, 512)}
	t.Cleanup(func() { c.Close() })
	p.write([]byte("PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"))
	var s []byte
	for id, v := range settings {
		s = binary.BigEndian.AppendUint16(s, id)
		s = binary.BigEndian.AppendUint32(s, v)
	}
	p.frame(h2Settings, 0, 0, s)
	go p.read()
	return p
}

func (p *h2peer) write(b []byte) {
	p.wmu.Lock()
	defer p.wmu.Unlock()
	_, _ = p.c.Write(b)
}

func (p *h2peer) frame(typ, flags byte, stream uint32, payload []byte) {
	h := []byte{byte(len(payload) >> 16), byte(len(payload) >> 8), byte(len(payload)), typ, flags, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(h[5:], stream)
	p.write(append(h, payload...))
}

// read hands frames to p.frames, answering SETTINGS and PING itself.
func (p *h2peer) read() {
	defer close(p.frames)
	br := bufio.NewReader(p.c)
	for {
		h := make([]byte, 9)
		if _, err := io.ReadFull(br, h); err != nil {
			return
		}
		f := h2frame{typ: h[3], flags: h[4], stream: binary.BigEndian.Uint32(h[5:]) & 0x7fffffff,
			payload: make([]byte, int(h[0])<<16|int(h[1])<<8|int(h[2]))}
		if _, err := io.ReadFull(br, f.payload); err != nil {
			return
		}
		switch {
		case f.typ == h2Settings && f.flags&h2FlagAck == 0:
			p.frame(h2Settings, h2FlagAck, 0, nil)
		case f.typ == h2Ping && f.flags&h2FlagAck == 0:
			p.frame(h2Ping, h2FlagAck, 0, f.payload)
		}
		p.frames <- f
	}
}

// get opens stream id with a GET of path; extra are more header pairs.
func (p *h2peer) get(id uint32, path string, endStream bool, extra ...string) {
	fields := append([]string{":method", "GET", ":scheme", "https", ":authority", p.host, ":path", path}, extra...)
	var b []byte
	for i := 0; i+1 < len(fields); i += 2 {
		b = append(b, 0) // literal without indexing, new name
		b = hpackString(b, fields[i])
		b = hpackString(b, fields[i+1])
	}
	flags := byte(h2FlagEndHeaders)
	if endStream {
		flags |= h2FlagEndStream
	}
	p.frame(h2Headers, flags, id, b)
}

func hpackString(b []byte, s string) []byte {
	v := len(s)
	if v < 127 {
		b = append(b, byte(v))
	} else {
		b = append(b, 127)
		for v -= 127; v >= 128; v /= 128 {
			b = append(b, byte(v%128+128))
		}
		b = append(b, byte(v))
	}
	return append(b, s...)
}

func (p *h2peer) windowUpdate(stream uint32, inc uint32) {
	p.frame(h2WindowUpdate, 0, stream, binary.BigEndian.AppendUint32(nil, inc))
}

// until reads frames until match returns true (true) or d passes (false).
func (p *h2peer) until(d time.Duration, match func(h2frame) bool) bool {
	deadline := time.After(d)
	for {
		select {
		case f, ok := <-p.frames:
			if !ok {
				return false
			}
			if match(f) {
				return true
			}
		case <-deadline:
			return false
		}
	}
}

// settingsOf decodes a SETTINGS payload.
func settingsOf(f h2frame) map[uint16]uint32 {
	m := map[uint16]uint32{}
	for i := 0; i+6 <= len(f.payload); i += 6 {
		m[binary.BigEndian.Uint16(f.payload[i:])] = binary.BigEndian.Uint32(f.payload[i+2:])
	}
	return m
}
