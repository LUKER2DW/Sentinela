package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type nopConn struct{ net.Conn }

func (nopConn) Close() error { return nil }

// poolStep is one event in a pool scenario: a connection from ip opening
// (name it to refer to it later), or a named one closing, signing in or
// starting a request.
type poolStep struct {
	op, name, ip string
	// open: whether it is admitted, and which named connection it evicts.
	admitted bool
	evicts   string
}

func runPool(t *testing.T, lim ConnLimits, steps []poolStep) *connPool {
	t.Helper()
	p := newConnPool(nil, lim)
	conns := map[string]*poolConn{}
	names := map[*poolConn]string{}
	for i, s := range steps {
		switch s.op {
		case "open":
			src, nw := limiterKey(s.ip), limiterNet(s.ip)
			pc, victim := p.admit(nopConn{}, src, nw)
			if (pc != nil) != s.admitted {
				t.Fatalf("step %d: open %s from %s admitted=%v, want %v", i, s.name, s.ip, pc != nil, s.admitted)
			}
			if got := names[victim]; got != s.evicts {
				t.Fatalf("step %d: open %s evicted %q, want %q", i, s.name, got, s.evicts)
			}
			if pc != nil {
				conns[s.name], names[pc] = pc, s.name
			}
		case "close":
			_ = conns[s.name].Close()
		case "trust":
			conns[s.name].trust()
		case "active":
			conns[s.name].setActive(true)
		default:
			t.Fatalf("bad op %q", s.op)
		}
	}
	return p
}

func TestConnPoolLimitsAndEviction(t *testing.T) {
	open := func(name, ip string) poolStep { return poolStep{op: "open", name: name, ip: ip, admitted: true} }
	refused := func(name, ip string) poolStep { return poolStep{op: "open", name: name, ip: ip} }
	evicts := func(name, ip, victim string) poolStep {
		return poolStep{op: "open", name: name, ip: ip, admitted: true, evicts: victim}
	}
	cases := []struct {
		name  string
		lim   ConnLimits
		steps []poolStep
	}{
		{"per source (IPv4 address)", ConnLimits{Total: 10, PerSource: 2, PerNet: 10, Anon: 10}, []poolStep{
			open("a", "203.0.113.1"), open("b", "203.0.113.1"), refused("c", "203.0.113.1"),
			open("d", "203.0.113.2"),
		}},
		{"per network (IPv4 /24)", ConnLimits{Total: 10, PerSource: 5, PerNet: 3, Anon: 10}, []poolStep{
			open("a", "203.0.113.1"), open("b", "203.0.113.2"), open("c", "203.0.113.3"),
			refused("d", "203.0.113.4"), open("e", "198.51.100.1"),
		}},
		{"IPv6 source is the /64, network the /48", ConnLimits{Total: 10, PerSource: 2, PerNet: 3, Anon: 10}, []poolStep{
			open("a", "2001:db8:1:1::1"), open("b", "2001:db8:1:1::2"), refused("c", "2001:db8:1:1::3"),
			open("d", "2001:db8:1:2::1"), refused("e", "2001:db8:1:3::1"), open("f", "2001:db8:2::1"),
		}},
		{"closing frees the slot", ConnLimits{Total: 10, PerSource: 1, PerNet: 10, Anon: 10}, []poolStep{
			open("a", "203.0.113.1"), refused("b", "203.0.113.1"), {op: "close", name: "a"}, open("c", "203.0.113.1"),
		}},
		{"full: a newcomer evicts the network holding the most", ConnLimits{Total: 4, PerSource: 4, PerNet: 4, Anon: 4}, []poolStep{
			open("x1", "203.0.113.1"), open("x2", "203.0.113.2"), open("x3", "203.0.113.3"),
			open("home", "198.51.100.7"),
			evicts("phone", "192.0.2.9", "x1"),     // oldest of the heaviest network
			evicts("x4", "203.0.113.4", "x2"),      // the heaviest only replaces its own
			evicts("tablet", "198.51.100.8", "x3"), // home holds fewer: the stranger goes
		}},
		{"idle anonymous connections go before busy ones", ConnLimits{Total: 3, PerSource: 3, PerNet: 3, Anon: 3}, []poolStep{
			open("x1", "203.0.113.1"), open("x2", "203.0.113.1"), open("x3", "203.0.113.1"),
			{op: "active", name: "x1"},
			evicts("home", "198.51.100.7", "x2"),
		}},
		{"signed-in connections are never evicted", ConnLimits{Total: 3, PerSource: 3, PerNet: 3, Anon: 3}, []poolStep{
			open("h1", "198.51.100.7"), open("h2", "198.51.100.7"), open("h3", "198.51.100.8"),
			{op: "trust", name: "h1"}, {op: "trust", name: "h2"}, {op: "trust", name: "h3"},
			refused("x", "203.0.113.1"),
		}},
		{"signing in leaves the anonymous share", ConnLimits{Total: 10, PerSource: 10, PerNet: 10, Anon: 2}, []poolStep{
			open("a", "198.51.100.7"), open("b", "198.51.100.7"), {op: "trust", name: "a"},
			open("c", "203.0.113.1"), // room: a is no longer anonymous
			evicts("d", "192.0.2.1", "b"),
		}},
		{"spread thin: a held network cannot displace an equal one", ConnLimits{Total: 3, PerSource: 3, PerNet: 3, Anon: 3}, []poolStep{
			open("x1", "203.0.113.1"), open("y1", "198.51.100.1"), open("z1", "192.0.2.1"),
			evicts("x2", "203.0.113.2", "x1"), // a network as heavy as any replaces its own
			evicts("w1", "233.252.0.1", "y1"), // one holding none takes the oldest elsewhere
		}},
		{"a network at its share cannot take more by churning", ConnLimits{Total: 4, PerSource: 4, PerNet: 4, Anon: 4}, []poolStep{
			open("x1", "203.0.113.1"), open("x2", "203.0.113.2"), open("home", "198.51.100.7"), open("y1", "192.0.2.1"),
			evicts("x3", "203.0.113.3", "x1"), evicts("x4", "203.0.113.4", "x2"), evicts("x5", "203.0.113.5", "x3"),
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := runPool(t, c.lim, c.steps)
			// The counters always agree with the connections still held.
			n, anon := 0, 0
			for range p.anon {
				anon++
			}
			for _, v := range p.src {
				n += v
			}
			if n != p.n || anon != p.anonN || p.n > c.lim.Total || p.anonN > c.lim.Anon {
				t.Fatalf("n=%d (sum %d) anon=%d (set %d), limits %+v", p.n, n, p.anonN, anon, c.lim)
			}
		})
	}
}

func TestConnPoolReleaseIsIdempotent(t *testing.T) {
	p := newConnPool(nil, ConnLimits{Total: 2, PerSource: 2, PerNet: 2, Anon: 1})
	a, _ := p.admit(nopConn{}, "a", "n")
	b, victim := p.admit(nopConn{}, "b", "n") // evicts a (same network, anonymous share full)
	if b == nil || victim != a {
		t.Fatalf("victim = %v, want a", victim)
	}
	_ = a.Close() // the evicted connection's server closes it later: no second release
	_ = b.Close()
	_ = b.Close()
	a.trust() // a late sign-in on a closed connection changes nothing
	if p.n != 0 || p.anonN != 0 || len(p.src) != 0 || len(p.net) != 0 || len(p.anon) != 0 {
		t.Fatalf("n=%d anon=%d src=%v net=%v", p.n, p.anonN, p.src, p.net)
	}
}

// A real listener: refused connections are closed before Accept returns, an
// evicted one is closed under its server, and the HTTP hooks mark signed-in
// connections.
func TestLimitConnsListener(t *testing.T) {
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("no loopback listener:", err)
	}
	ln := LimitConns(inner, ConnLimits{Total: 8, PerSource: 3, PerNet: 8, Anon: 2}, nil)
	defer ln.Close()
	accepted := make(chan net.Conn, 8)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			accepted <- c
		}
	}()
	dial := func() net.Conn {
		c, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		return c
	}
	get := func() net.Conn {
		select {
		case c := <-accepted:
			return c
		case <-time.After(time.Second):
			return nil
		}
	}
	closed := func(c net.Conn) bool { // by the server: EOF or reset, not our timeout
		_ = c.SetReadDeadline(time.Now().Add(time.Second))
		_, err := c.Read(make([]byte, 1))
		var ne net.Error
		return err != nil && !(errors.As(err, &ne) && ne.Timeout())
	}
	c1, c2 := dial(), dial()
	s1, s2 := get(), get()
	if s1 == nil || s2 == nil {
		t.Fatal("first two connections must be accepted")
	}
	// The anonymous share is full: the third replaces the oldest (same network).
	dial()
	if get() == nil {
		t.Fatal("third connection not accepted")
	}
	if !closed(c1) {
		t.Fatal("the evicted connection must be closed")
	}
	_ = s1.Close()
	// A signed-in connection leaves the anonymous share: the next one fits.
	s2.(*poolConn).trust()
	dial()
	if get() == nil {
		t.Fatal("fourth connection not accepted")
	}
	// Per source: three are open (c2 signed in, c3, c4); a fifth is refused.
	c5 := dial()
	if get() != nil || !closed(c5) {
		t.Fatal("a connection over the per-source cap must be refused")
	}
	if closed(c2) {
		t.Fatal("the signed-in connection was closed")
	}
}

func TestConnPoolHooks(t *testing.T) {
	p := newConnPool(nil, ConnLimits{Total: 4, PerSource: 4, PerNet: 4, Anon: 4})
	pc, _ := p.admit(nopConn{}, "a", "n")
	hs := &http.Server{}
	var prevState bool
	hs.ConnState = func(net.Conn, http.ConnState) { prevState = true }
	p.hook(hs)
	// The TLS server wraps the pooled connection.
	wrapped := struct {
		net.Conn
		netConner
	}{pc, netConner{pc}}
	hs.ConnState(wrapped, http.StateActive)
	if !pc.active || !prevState {
		t.Fatalf("active=%v, previous hook called=%v", pc.active, prevState)
	}
	hs.ConnState(wrapped, http.StateIdle)
	if pc.active {
		t.Fatal("still active after idle")
	}
	ctx := hs.ConnContext(context.Background(), wrapped)
	r := httptest.NewRequest("GET", "/", nil).WithContext(ctx)
	trustConn(r)
	if !pc.trusted || p.anonN != 0 {
		t.Fatalf("trusted=%v anon=%d", pc.trusted, p.anonN)
	}
	// A connection of another pool, or none, is ignored.
	trustConn(httptest.NewRequest("GET", "/", nil))
}

type netConner struct{ c net.Conn }

func (n netConner) NetConn() net.Conn { return n.c }
