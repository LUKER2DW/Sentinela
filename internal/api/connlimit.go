package api

import (
	"net"
	"net/netip"
	"sync"
)

// LimitConns wraps ln so that at most total connections are open at once and
// at most perIP of them come from one source (an IPv6 client counts as its
// /64). A connection over a limit is closed right after accept, before any
// TLS or HTTP work. It is for listeners that face the internet directly: the
// server has no Read/WriteTimeout (streams need long writes), so without it a
// client could open connections until the 384 MB container is out of memory.
// Listeners behind a local proxy see every client as 127.0.0.1 and must not use it.
func LimitConns(ln net.Listener, total, perIP int) net.Listener {
	return &limitListener{Listener: ln, total: total, perIP: perIP, per: map[string]int{}}
}

type limitListener struct {
	net.Listener
	total, perIP int

	mu  sync.Mutex
	n   int
	per map[string]int
}

func (l *limitListener) take(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.n >= l.total || l.per[key] >= l.perIP {
		return false
	}
	l.n++
	l.per[key]++
	return true
}

func (l *limitListener) release(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.n--
	if l.per[key]--; l.per[key] <= 0 {
		delete(l.per, key)
	}
}

func (l *limitListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		key := connKey(c.RemoteAddr())
		if !l.take(key) {
			_ = c.Close()
			continue
		}
		return &limitConn{Conn: c, l: l, key: key}, nil
	}
}

// connKey is the source of a connection: its address, IPv6 as /64.
func connKey(a net.Addr) string {
	ap, err := netip.ParseAddrPort(a.String())
	if err != nil {
		return a.String()
	}
	return limiterKey(ap.Addr().String())
}

type limitConn struct {
	net.Conn
	l    *limitListener
	key  string
	once sync.Once
}

func (c *limitConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { c.l.release(c.key) })
	return err
}
