package tailnet

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"tailscale.com/tsnet"
)

// RTCPort is go2rtc's WebRTC port on the server; the app forwards the same
// port on 127.0.0.1 (the candidate go2rtc advertises for the app).
const RTCPort = 8555

var (
	mu      sync.Mutex
	node    *tsnet.Server
	cancel  context.CancelFunc
	closers []func() error
	state   = "stopped"
	lastErr string
	upCh    chan struct{}
)

func setState(s, e string) {
	mu.Lock()
	state, lastErr = s, e
	mu.Unlock()
}

// Status is "stopped", "starting", "running" or "error: <reason>".
func Status() string {
	mu.Lock()
	defer mu.Unlock()
	if state == "error" {
		return "error: " + lastErr
	}
	return state
}

// Start brings the node up in stateDir (the app's private storage) and
// serves the proxy on httpAddr (e.g. "127.0.0.1:47080") and the WebRTC
// forwarders on 127.0.0.1:8555. authKey is only needed the first time;
// afterwards the saved node key is used. server is the Sentinela host's
// MagicDNS name (sentinela.<tailnet>.ts.net). The listeners are bound
// before Start returns, so the WebView can load at once; requests wait
// until the node is up.
func Start(stateDir, hostname, authKey, server, httpAddr, token string) error {
	mu.Lock()
	if node != nil {
		mu.Unlock()
		return nil
	}
	mu.Unlock()
	if server == "" || token == "" {
		return errors.New("server and token are required")
	}
	// No telemetry to Tailscale's log service from the app.
	_ = os.Setenv("TS_NO_LOGS_NO_SUPPORT", "true")

	s := &tsnet.Server{
		Dir:      stateDir,
		Hostname: hostname,
		AuthKey:  authKey,
		Logf:     func(string, ...any) {},
	}
	ctx, cc := context.WithCancel(context.Background())
	ready := make(chan struct{})
	// dial waits for the node, so early WebView requests just take longer.
	dial := func(dctx context.Context, network, addr string) (net.Conn, error) {
		select {
		case <-ready:
		case <-dctx.Done():
			return nil, dctx.Err()
		}
		return s.Dial(dctx, network, addr)
	}

	ln, err := net.Listen("tcp", httpAddr)
	if err != nil {
		cc()
		return fmt.Errorf("proxy listen: %w", err)
	}
	hs := &http.Server{
		Handler:           newProxy(dial, server, token, &tls.Config{ServerName: server, MinVersion: tls.VersionTLS12}),
		ReadHeaderTimeout: 15 * time.Second,
	}
	go func() { _ = hs.Serve(ln) }()
	cl := []func() error{hs.Close}

	target := net.JoinHostPort(server, strconv.Itoa(RTCPort))
	rtc := net.JoinHostPort("127.0.0.1", strconv.Itoa(RTCPort))
	// WebRTC forwarders are best effort: without them live video falls
	// back to MSE through the proxy.
	if tl, err := net.Listen("tcp", rtc); err == nil {
		go forwardTCP(ctx, tl, dial, target)
		cl = append(cl, tl.Close)
	}
	if pc, err := net.ListenPacket("udp", rtc); err == nil {
		go forwardUDP(ctx, pc, dial, target)
		cl = append(cl, pc.Close)
	}

	mu.Lock()
	node, cancel, closers, upCh = s, cc, append(cl, s.Close), ready
	state, lastErr = "starting", ""
	mu.Unlock()

	go func() {
		for attempt := 0; ; attempt++ {
			uctx, ucancel := context.WithTimeout(ctx, 60*time.Second)
			_, err := s.Up(uctx)
			ucancel()
			if err == nil {
				setState("running", "")
				close(ready)
				return
			}
			if ctx.Err() != nil {
				return
			}
			setState("error", shortErr(err))
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff(attempt)):
			}
			setState("starting", "")
		}
	}()
	return nil
}

// WaitUp blocks until the node is running or timeoutMs passes.
func WaitUp(timeoutMs int) error {
	mu.Lock()
	ch := upCh
	mu.Unlock()
	if ch == nil {
		return errors.New("not started")
	}
	select {
	case <-ch:
		return nil
	case <-time.After(time.Duration(timeoutMs) * time.Millisecond):
		return errors.New(Status())
	}
}

// Stop shuts the node and the local listeners down (state is kept).
func Stop() {
	mu.Lock()
	defer mu.Unlock()
	if node == nil {
		return
	}
	cancel()
	for _, c := range closers {
		_ = c()
	}
	node, closers, upCh, state = nil, nil, nil, "stopped"
}

func backoff(attempt int) time.Duration {
	d := time.Duration(1<<min(attempt, 5)) * time.Second
	return min(d, 30*time.Second)
}

func shortErr(err error) string {
	s := err.Error()
	if len(s) > 160 {
		s = s[:160]
	}
	return s
}
