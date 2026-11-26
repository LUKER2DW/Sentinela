package api

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"sync"
)

// ConnLimits are the open-connection limits of a listener that faces the
// internet directly. Sources count like the sign-in limits (login_limits.go):
// an IPv4 address or IPv6 /64 (limiterKey) inside a network, IPv4 /24 or
// IPv6 /48 (limiterNet).
type ConnLimits struct {
	Total     int // open connections
	PerSource int // per source
	PerNet    int // per network
	// Anon bounds the connections that have not carried a signed-in request
	// (every connection starts so). It is the memory bound for strangers
	// (publicHTTP2); signed-in connections use the rest of Total.
	Anon int
}

// PublicConnLimits are the port-forwarded TLS listener's (ADR-0019). The
// household needs a few dozen connections; the anonymous share is what the
// memory math of publicHTTP2 allows.
func PublicConnLimits() ConnLimits {
	return ConnLimits{Total: 1024, PerSource: 64, PerNet: 128, Anon: publicAnonConns}
}

// LimitConns wraps ln, a listener that faces the internet, in a pool with
// limits lim. A connection over a limit is closed right after accept, before
// any TLS or HTTP work, except when the pool is full: then the newcomer takes
// the place of an anonymous connection of the network holding the most of
// them (idle ones first, then the oldest), so a few sources holding many
// connections cannot lock everyone else out. Signed-in connections are never
// closed for a newcomer. hs, when not nil, is the server of ln: its hooks
// tell the pool which connections are idle and which signed in (closedDoor).
// Listeners behind a local proxy see every client as 127.0.0.1 and must not
// use it.
func LimitConns(ln net.Listener, lim ConnLimits, hs *http.Server) net.Listener {
	p := newConnPool(ln, lim)
	if hs != nil {
		p.hook(hs)
	}
	return p
}

func newConnPool(ln net.Listener, lim ConnLimits) *connPool {
	return &connPool{Listener: ln, lim: lim,
		src: map[string]int{}, net: map[string]int{}, anonSrc: map[string]int{}, anonNet: map[string]int{},
		anon: map[*poolConn]struct{}{}}
}

type connPool struct {
	net.Listener
	lim ConnLimits

	mu               sync.Mutex
	n, anonN         int
	src, net         map[string]int // open connections per source and network
	anonSrc, anonNet map[string]int // the anonymous ones
	anon             map[*poolConn]struct{}
	seq              uint64
}

// poolConn is one admitted connection. Its flags are guarded by the pool's mu.
type poolConn struct {
	net.Conn
	p        *connPool
	src, net string
	seq      uint64 // accept order
	trusted  bool   // carried a signed-in request
	active   bool   // a request is in progress
	gone     bool   // released (closed or evicted)
}

func (p *connPool) Accept() (net.Conn, error) {
	for {
		c, err := p.Listener.Accept()
		if err != nil {
			return nil, err
		}
		src, nw := connKeys(c.RemoteAddr())
		pc, victim := p.admit(c, src, nw)
		if victim != nil {
			_ = victim.Conn.Close() // its server goroutine sees the error and ends
		}
		if pc == nil {
			_ = c.Close()
			continue
		}
		return pc, nil
	}
}

// admit counts a new connection from src in network nw. It returns nil when
// a limit refuses it, and the connection it evicted to make room, if any.
func (p *connPool) admit(c net.Conn, src, nw string) (pc, victim *poolConn) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.src[src] >= p.lim.PerSource || p.net[nw] >= p.lim.PerNet {
		return nil, nil
	}
	if p.n >= p.lim.Total || p.anonN >= p.lim.Anon {
		victim = p.victimFor(nw)
		if victim == nil {
			return nil, nil
		}
		p.releaseLocked(victim)
	}
	p.seq++
	pc = &poolConn{Conn: c, p: p, src: src, net: nw, seq: p.seq}
	p.n++
	p.src[src]++
	p.net[nw]++
	p.anonN++
	p.anonSrc[src]++
	p.anonNet[nw]++
	p.anon[pc] = struct{}{}
	return pc, victim
}

// victimFor picks the anonymous connection a newcomer from network nw may
// replace: one of the network holding the most anonymous connections (then
// of its busiest source; idle before active; oldest first). A newcomer only
// displaces a network holding more anonymous connections than its own;
// otherwise it can only replace one of its own network's. So the household,
// with one or two connections opening, is never the one closed while
// strangers hold more, and a network cannot grow past the others by churning.
func (p *connPool) victimFor(nw string) *poolConn {
	var best, own *poolConn
	for c := range p.anon {
		if best == nil || p.evictsBefore(c, best) {
			best = c
		}
		if c.net == nw && (own == nil || p.evictsBefore(c, own)) {
			own = c
		}
	}
	if best != nil && p.anonNet[best.net] > p.anonNet[nw] {
		return best
	}
	return own
}

// evictsBefore reports whether anonymous connection a goes before b.
func (p *connPool) evictsBefore(a, b *poolConn) bool {
	if x, y := p.anonNet[a.net], p.anonNet[b.net]; x != y {
		return x > y
	}
	if x, y := p.anonSrc[a.src], p.anonSrc[b.src]; x != y {
		return x > y
	}
	if a.active != b.active {
		return !a.active
	}
	return a.seq < b.seq
}

func (p *connPool) releaseLocked(c *poolConn) {
	if c.gone {
		return
	}
	c.gone = true
	p.n--
	dec(p.src, c.src)
	dec(p.net, c.net)
	if !c.trusted {
		p.anonN--
		dec(p.anonSrc, c.src)
		dec(p.anonNet, c.net)
		delete(p.anon, c)
	}
}

func dec(m map[string]int, k string) {
	if m[k]--; m[k] <= 0 {
		delete(m, k)
	}
}

func (c *poolConn) Close() error {
	err := c.Conn.Close()
	c.p.mu.Lock()
	c.p.releaseLocked(c)
	c.p.mu.Unlock()
	return err
}

// trust marks c as the household's: it carried a signed-in request, so it
// no longer counts as anonymous and is never evicted.
func (c *poolConn) trust() {
	p := c.p
	p.mu.Lock()
	defer p.mu.Unlock()
	if c.trusted || c.gone {
		return
	}
	c.trusted = true
	p.anonN--
	dec(p.anonSrc, c.src)
	dec(p.anonNet, c.net)
	delete(p.anon, c)
}

func (c *poolConn) setActive(active bool) {
	c.p.mu.Lock()
	c.active = active
	c.p.mu.Unlock()
}

type poolConnKey struct{}

// hook adds the pool's ConnContext and ConnState to hs (keeping any set).
func (p *connPool) hook(hs *http.Server) {
	prevCtx, prevState := hs.ConnContext, hs.ConnState
	hs.ConnContext = func(ctx context.Context, c net.Conn) context.Context {
		if prevCtx != nil {
			ctx = prevCtx(ctx, c)
		}
		if pc := poolConnOf(c); pc != nil && pc.p == p {
			ctx = context.WithValue(ctx, poolConnKey{}, pc)
		}
		return ctx
	}
	hs.ConnState = func(c net.Conn, st http.ConnState) {
		if pc := poolConnOf(c); pc != nil && pc.p == p {
			// HTTP/2 reports active while any stream is open.
			pc.setActive(st == http.StateActive || st == http.StateHijacked)
		}
		if prevState != nil {
			prevState(c, st)
		}
	}
}

// poolConnOf finds the pooled connection under c (a TLS server wraps it).
func poolConnOf(c net.Conn) *poolConn {
	for c != nil {
		switch v := c.(type) {
		case *poolConn:
			return v
		case interface{ NetConn() net.Conn }:
			c = v.NetConn()
		default:
			return nil
		}
	}
	return nil
}

// trustConn marks the connection of r, a signed-in request, as the household's.
func trustConn(r *http.Request) {
	if pc, ok := r.Context().Value(poolConnKey{}).(*poolConn); ok {
		pc.trust()
	}
}

// connKeys are the source and network of a connection (limiterKey, limiterNet).
func connKeys(a net.Addr) (src, nw string) {
	ap, err := netip.ParseAddrPort(a.String())
	if err != nil {
		return a.String(), a.String()
	}
	ip := ap.Addr().String()
	return limiterKey(ip), limiterNet(ip)
}
