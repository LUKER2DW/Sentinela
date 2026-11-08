package api

import (
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/LUKER2DW/sentinela/internal/config"
)

// Android app pairing. One APK serves every house: the person opens
// Sistema → Aplicativo on a computer and points the phone's camera at a QR code
// holding sentinela://pair?url=<origin>&pin=<sha256>. The app asks before
// it switches servers, then trusts exactly that certificate (or, without a
// pin, only publicly trusted ones). Nothing here is secret: the address and
// the certificate fingerprint are what the phone would see anyway; the QR
// saves typing and makes the pin exact.

// pinState is the SHA-256 of the public listener's self-signed certificate
// (set by main when it creates one); "" = a publicly trusted certificate.
type pinState struct {
	mu  sync.Mutex
	pin string
}

// SetPublicCertPin records the public listener's self-signed certificate.
func (s *Server) SetPublicCertPin(sha256hex string) {
	s.pubPin.mu.Lock()
	s.pubPin.pin = strings.ToLower(sha256hex)
	s.pubPin.mu.Unlock()
}

func (s *Server) publicCertPin() string {
	s.pubPin.mu.Lock()
	defer s.pubPin.mu.Unlock()
	return s.pubPin.pin
}

type pairingOption struct {
	Kind string `json:"kind"` // tailnet | public | proxy | lan
	URL  string `json:"url"`
	Pin  string `json:"pin,omitempty"`
	// Code is the short form of Pin (homeCode). A phone given only the
	// address reads the certificate itself and asks the person to confirm it
	// matches this code.
	Code  string `json:"code,omitempty"`
	Link  string `json:"link"`
	QRSVG string `json:"qr_svg"`
}

// homeCode is the first 80 bits of a certificate's SHA-256 as
// XXXX-XXXX-XXXX-XXXX-XXXX, "" when pin is not a 64-digit hex string. The
// app shows the same code for the certificate it reads from an address.
func homeCode(pin string) string {
	if len(pin) != 64 {
		return ""
	}
	for _, c := range pin {
		if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			return ""
		}
	}
	h := strings.ToUpper(pin[:20])
	return h[0:4] + "-" + h[4:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20]
}

// lanIPv4 is the server's address on the home network: the first private
// IPv4 of an interface that is up and not a container, VPN or loopback one.
func lanIPv4() string {
	ifs, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, in := range ifs {
		if in.Flags&net.FlagUp == 0 || in.Flags&net.FlagLoopback != 0 {
			continue
		}
		skip := false
		for _, p := range []string{"docker", "br-", "veth", "tailscale", "sn-", "lo"} {
			if strings.HasPrefix(in.Name, p) {
				skip = true
			}
		}
		if skip {
			continue
		}
		addrs, _ := in.Addrs()
		for _, ad := range addrs {
			if n, ok := ad.(*net.IPNet); ok && n.IP.To4() != nil && n.IP.IsPrivate() {
				return n.IP.String()
			}
		}
	}
	return ""
}

// publicOrigin is the address people use from outside: remote.public.url,
// or the domain with the listener's port.
func publicOrigin(p config.PublicTLS) string {
	if p.URL != "" {
		return strings.TrimRight(p.URL, "/")
	}
	if p.Domain == "" {
		return ""
	}
	_, port, err := net.SplitHostPort(p.Listen)
	if err != nil || port == "443" {
		return "https://" + p.Domain
	}
	return "https://" + net.JoinHostPort(p.Domain, port)
}

// outsideURL is the address people use from outside the home: the
// port-forward's address, else the tunnel's; "" when neither is set.
func (s *Server) outsideURL() string {
	if u := publicOrigin(s.Cfg.Remote.Public); u != "" {
		return u
	}
	return strings.TrimRight(s.Cfg.Remote.Proxy.URL, "/")
}

// appPairing: GET /api/app/pairing → the ways the app can reach this house.
func (s *Server) appPairing(w http.ResponseWriter, _ *http.Request) {
	var opts []pairingOption
	add := func(kind, origin, pin string) {
		if origin == "" {
			return
		}
		link := "sentinela://pair?url=" + url.QueryEscape(origin)
		if pin != "" {
			link += "&pin=" + pin
		}
		svg, err := qrSVG(link)
		if err != nil {
			return
		}
		opts = append(opts, pairingOption{Kind: kind, URL: origin, Pin: pin, Code: homeCode(pin), Link: link, QRSVG: svg})
	}
	add("tailnet", strings.TrimRight(s.Cfg.Remote.TailnetURL, "/"), "")
	pin := ""
	if s.Cfg.Remote.Public.SelfSigned {
		pin = s.publicCertPin()
	}
	add("public", publicOrigin(s.Cfg.Remote.Public), pin)
	// A tunnel or reverse proxy serves a publicly trusted certificate.
	add("proxy", strings.TrimRight(s.Cfg.Remote.Proxy.URL, "/"), "")
	// At home: the same TLS listener on the server's LAN address, with the
	// same pin. A self-signed certificate is checked by its pin, not by
	// name, so the LAN address works without internet or the router's NAT
	// loopback; a typed LAN address gets the pin from this list.
	if pin != "" && publicOrigin(s.Cfg.Remote.Public) != "" && s.lanIP != nil {
		if _, port, err := net.SplitHostPort(s.Cfg.Remote.Public.Listen); err == nil {
			if ip := s.lanIP(); ip != "" {
				add("lan", "https://"+net.JoinHostPort(ip, port), pin)
			}
		}
	}
	if opts == nil {
		opts = []pairingOption{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"options": opts})
}
