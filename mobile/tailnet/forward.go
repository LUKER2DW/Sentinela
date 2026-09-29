package tailnet

import (
	"context"
	"io"
	"net"
	"sync"
	"time"
)

// forwardTCP accepts on ln and pipes each connection to target over dial
// (WebRTC ICE-TCP to go2rtc).
func forwardTCP(ctx context.Context, ln net.Listener, dial dialFunc, target string) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			defer c.Close()
			dctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			up, err := dial(dctx, "tcp", target)
			cancel()
			if err != nil {
				return
			}
			defer up.Close()
			done := make(chan struct{}, 2)
			go func() { _, _ = io.Copy(up, c); done <- struct{}{} }()
			go func() { _, _ = io.Copy(c, up); done <- struct{}{} }()
			<-done
		}()
	}
}

// udpIdle closes a UDP flow nobody used for this long.
const udpIdle = 2 * time.Minute

// forwardUDP relays datagrams between local peers (the WebView's ICE
// sockets) and target, one upstream flow per local peer address.
func forwardUDP(ctx context.Context, pc net.PacketConn, dial dialFunc, target string) {
	var mu sync.Mutex
	flows := map[string]net.Conn{}
	buf := make([]byte, 64<<10)
	for {
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			mu.Lock()
			for _, f := range flows {
				f.Close()
			}
			mu.Unlock()
			return
		}
		key := from.String()
		mu.Lock()
		up := flows[key]
		mu.Unlock()
		if up == nil {
			dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			up, err = dial(dctx, "udp", target)
			cancel()
			if err != nil {
				continue
			}
			mu.Lock()
			flows[key] = up
			mu.Unlock()
			go func(up net.Conn, to net.Addr) {
				defer func() {
					mu.Lock()
					delete(flows, to.String())
					mu.Unlock()
					up.Close()
				}()
				b := make([]byte, 64<<10)
				for {
					_ = up.SetReadDeadline(time.Now().Add(udpIdle))
					m, err := up.Read(b)
					if err != nil {
						return
					}
					if _, err := pc.WriteTo(b[:m], to); err != nil {
						return
					}
				}
			}(up, from)
		}
		_, _ = up.Write(buf[:n])
	}
}
