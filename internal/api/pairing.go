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
// Minha conta → Aplicativo on a computer and points the phone's camera at a QR code
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
	Kind  string `json:"kind"` // tailnet | public
	URL   string `json:"url"`
	Pin   string `json:"pin,omitempty"`
	Link  string `json:"link"`
	QRSVG string `json:"qr_svg"`
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
		opts = append(opts, pairingOption{Kind: kind, URL: origin, Pin: pin, Link: link, QRSVG: svg})
	}
	add("tailnet", strings.TrimRight(s.Cfg.Remote.TailnetURL, "/"), "")
	pin := ""
	if s.Cfg.Remote.Public.SelfSigned {
		pin = s.publicCertPin()
	}
	add("public", publicOrigin(s.Cfg.Remote.Public), pin)
	if opts == nil {
		opts = []pairingOption{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"options": opts})
}
