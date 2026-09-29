package tailnet

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestProxy(t *testing.T) {
	var got *http.Request
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		http.SetCookie(w, &http.Cookie{Name: "sentinela_session", Value: "s", Secure: true, HttpOnly: true, Path: "/"})
		w.Header().Set("Strict-Transport-Security", "max-age=1")
		w.Header().Set("Location", "https://sentinela.example.ts.net/#/live")
		_, _ = io.WriteString(w, "ok")
	}))
	defer up.Close()
	upAddr := up.Listener.Addr().String()
	// The proxy dials the MagicDNS name; the test resolves it to the fake.
	dial := func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, upAddr)
	}
	p := httptest.NewServer(newProxy(dial, "sentinela.example.ts.net", "tok",
		&tls.Config{InsecureSkipVerify: true})) //nolint:gosec // test server
	defer p.Close()

	resp, err := http.Get(p.URL + "/api/me")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("no token: %d", resp.StatusCode)
	}

	req, _ := http.NewRequest("POST", p.URL+"/api/login", strings.NewReader("{}"))
	req.Header.Set("Cookie", "a=1; "+TokenCookie+"=tok; sentinela_session=x")
	req.Header.Set("Origin", p.URL)
	resp, err = http.DefaultTransport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if got.Host != "sentinela.example.ts.net" || got.Header.Get("Origin") != "https://sentinela.example.ts.net" {
		t.Errorf("host=%q origin=%q", got.Host, got.Header.Get("Origin"))
	}
	if c := got.Header.Get("Cookie"); strings.Contains(c, TokenCookie) || !strings.Contains(c, "sentinela_session=x") {
		t.Errorf("forwarded cookie %q", c)
	}
	if sc := resp.Header.Get("Set-Cookie"); strings.Contains(strings.ToLower(sc), "secure") || !strings.Contains(sc, "HttpOnly") {
		t.Errorf("set-cookie %q", sc)
	}
	if resp.Header.Get("Strict-Transport-Security") != "" || resp.Header.Get("Location") != "/#/live" {
		t.Errorf("headers %v", resp.Header)
	}
}

func TestForwardTCPAndUDP(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}

	// TCP echo upstream.
	tu, _ := net.Listen("tcp", "127.0.0.1:0")
	defer tu.Close()
	go func() {
		for {
			c, err := tu.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(c, c); c.Close() }()
		}
	}()
	tl, _ := net.Listen("tcp", "127.0.0.1:0")
	go forwardTCP(ctx, tl, dial, tu.Addr().String())
	c, err := net.Dial("tcp", tl.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = c.Write([]byte("stun"))
	buf := make([]byte, 4)
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(c, buf); err != nil || string(buf) != "stun" {
		t.Fatalf("tcp echo %q %v", buf, err)
	}
	c.Close()

	// UDP echo upstream.
	uu, _ := net.ListenPacket("udp", "127.0.0.1:0")
	defer uu.Close()
	go func() {
		b := make([]byte, 1500)
		for {
			n, a, err := uu.ReadFrom(b)
			if err != nil {
				return
			}
			_, _ = uu.WriteTo(b[:n], a)
		}
	}()
	ul, _ := net.ListenPacket("udp", "127.0.0.1:0")
	defer ul.Close()
	go forwardUDP(ctx, ul, dial, uu.LocalAddr().String())
	uc, err := net.Dial("udp", ul.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer uc.Close()
	_, _ = uc.Write([]byte("ice!"))
	_ = uc.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := uc.Read(buf)
	if err != nil || string(buf[:n]) != "ice!" {
		t.Fatalf("udp echo %q %v", buf[:n], err)
	}
}

func TestParseInterfaces(t *testing.T) {
	ifs := parseInterfaces("lo 1 65536 up,loopback 127.0.0.1/8 ::1/128\nwlan0 30 1500 up,broadcast,multicast 192.168.1.50/24 fe80::1/64\nbad line\n")
	if len(ifs) != 2 || ifs[1].Name != "wlan0" || ifs[1].Flags&net.FlagUp == 0 || len(ifs[1].AltAddrs) != 2 {
		t.Fatalf("%+v", ifs)
	}
	if ifs[0].Flags&net.FlagLoopback == 0 || ifs[1].AltAddrs[0].String() != "192.168.1.50/24" {
		t.Fatalf("%+v", ifs)
	}
}
