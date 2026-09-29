// Package publiccert keeps a Let's Encrypt certificate for the home's fixed
// public IP address (ADR-0019 P1 without a domain name).
//
// Let's Encrypt signs IP addresses only with the "shortlived" profile
// (about 6 days), which autocert cannot ask for, so this uses lego. The
// TLS-ALPN-01 challenge is answered by the same listener (router WAN 443 →
// remote.public.listen). Where the ISP blocks inbound 443, HTTP01 switches
// to the HTTP-01 challenge, answered by HTTPHandler on its own listener
// (router WAN 80 → remote.public.http01_listen); the site itself can then
// use any router port.
package publiccert

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/challenge/tlsalpn01"
	"github.com/go-acme/lego/v4/lego"
	legolog "github.com/go-acme/lego/v4/log"
	"github.com/go-acme/lego/v4/registration"
)

// Manager obtains and renews the certificate and serves it (GetCertificate).
type Manager struct {
	IP    string
	Email string
	Dir   string // e.g. /data/acme/ip; account key and certificate, 0600
	Log   *slog.Logger
	// CA is the ACME directory; empty = Let's Encrypt production.
	CA string
	// HTTP01: validate over plain HTTP (HTTPHandler) instead of TLS-ALPN-01.
	HTTP01 bool

	mu     sync.RWMutex
	cert   *tls.Certificate
	chal   *tls.Certificate  // TLS-ALPN-01 answer while an order is pending
	tokens map[string]string // HTTP-01 token -> key authorization
}

// HTTPHandler answers HTTP-01 challenges and nothing else (404).
func (m *Manager) HTTPHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.URL.Path, "/.well-known/acme-challenge/")
		m.mu.RLock()
		ka := m.tokens[token]
		m.mu.RUnlock()
		if !ok || r.Method != http.MethodGet || ka == "" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(ka))
	})
}

// http01 is lego's HTTP-01 provider, backed by the Manager's token table.
type http01 struct{ m *Manager }

func (h http01) Present(_, token, keyAuth string) error {
	h.m.mu.Lock()
	defer h.m.mu.Unlock()
	if h.m.tokens == nil {
		h.m.tokens = map[string]string{}
	}
	h.m.tokens[token] = keyAuth
	return nil
}

func (h http01) CleanUp(_, token, _ string) error {
	h.m.mu.Lock()
	defer h.m.mu.Unlock()
	delete(h.m.tokens, token)
	return nil
}

// TLSConfig for the public listener.
func (m *Manager) TLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion:     tls.VersionTLS12,
		NextProtos:     []string{"h2", "http/1.1", tlsalpn01.ACMETLS1Protocol},
		GetCertificate: m.getCertificate,
	}
}

func (m *Manager) getCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	// The CA's validation connection offers only acme-tls/1 (RFC 8737);
	// for an IP it sends the reverse-DNS name as SNI, browsers send none.
	if slices.Equal(hello.SupportedProtos, []string{tlsalpn01.ACMETLS1Protocol}) {
		if m.chal == nil {
			return nil, errors.New("publiccert: no challenge pending")
		}
		return m.chal, nil
	}
	if m.cert == nil {
		return nil, errors.New("publiccert: certificate not issued yet")
	}
	return m.cert, nil
}

// Present and CleanUp make Manager lego's TLS-ALPN-01 provider.
func (m *Manager) Present(domain, _, keyAuth string) error {
	c, err := tlsalpn01.ChallengeCert(domain, keyAuth)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.chal = c
	m.mu.Unlock()
	return nil
}

func (m *Manager) CleanUp(_, _, _ string) error {
	m.mu.Lock()
	m.chal = nil
	m.mu.Unlock()
	return nil
}

// renewAt: a third of the lifetime left (2 days of a 6-day certificate).
func renewAt(leaf *x509.Certificate) time.Time {
	return leaf.NotAfter.Add(-leaf.NotAfter.Sub(leaf.NotBefore) / 3)
}

// Run loads the stored certificate, then obtains or renews it as needed
// until ctx ends. Failures are retried with backoff (at most hourly, well
// under Let's Encrypt's failed-validation limit).
func (m *Manager) Run(ctx context.Context) {
	legolog.Logger = log.New(slogWriter{m.Log}, "", 0)
	if err := os.MkdirAll(m.Dir, 0o700); err != nil {
		m.Log.Error("public certificate: data dir", "err", err)
		return
	}
	leaf := m.load()
	backoff := 5 * time.Minute
	for {
		wait := time.Duration(0)
		if leaf != nil {
			wait = time.Until(renewAt(leaf))
		}
		if wait <= 0 {
			l, err := m.obtain()
			if err != nil {
				hint := "is WAN 443 forwarded to the public listener?"
				if m.HTTP01 {
					hint = "is WAN 80 forwarded to the HTTP-01 listener?"
				}
				m.Log.Warn("public certificate: order failed ("+hint+")", "ip", m.IP, "err", err, "retry_in", backoff)
				wait = backoff
				backoff = min(backoff*2, time.Hour)
			} else {
				leaf, backoff = l, 5*time.Minute
				m.Log.Info("public certificate issued", "ip", m.IP, "not_after", leaf.NotAfter)
				wait = time.Until(renewAt(leaf))
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(min(wait, 6*time.Hour)): // re-check; sleeps drift on suspend
		}
	}
}

func (m *Manager) load() *x509.Certificate {
	certPEM, err1 := os.ReadFile(filepath.Join(m.Dir, "cert.pem"))
	keyPEM, err2 := os.ReadFile(filepath.Join(m.Dir, "key.pem"))
	if err1 != nil || err2 != nil {
		return nil
	}
	return m.install(certPEM, keyPEM)
}

func (m *Manager) install(certPEM, keyPEM []byte) *x509.Certificate {
	c, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil
	}
	leaf, err := x509.ParseCertificate(c.Certificate[0])
	if err != nil || !slices.ContainsFunc(leaf.IPAddresses, func(ip net.IP) bool { return ip.Equal(net.ParseIP(m.IP)) }) {
		return nil // a certificate for another address: order a new one
	}
	c.Leaf = leaf
	m.mu.Lock()
	m.cert = &c
	m.mu.Unlock()
	return leaf
}

func (m *Manager) obtain() (*x509.Certificate, error) {
	u, err := m.account()
	if err != nil {
		return nil, err
	}
	cfg := lego.NewConfig(u)
	cfg.CADirURL = m.CA
	if cfg.CADirURL == "" {
		cfg.CADirURL = lego.LEDirectoryProduction
	}
	cfg.Certificate.KeyType = certcrypto.EC256
	cfg.UserAgent = "sentinela"
	client, err := lego.NewClient(cfg)
	if err != nil {
		return nil, err
	}
	if m.HTTP01 {
		err = client.Challenge.SetHTTP01Provider(http01{m})
	} else {
		err = client.Challenge.SetTLSALPN01Provider(m)
	}
	if err != nil {
		return nil, err
	}
	if u.reg == nil {
		reg, err := client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
		if err != nil {
			return nil, fmt.Errorf("account: %w", err)
		}
		u.reg = reg
		b, _ := json.Marshal(reg)
		if err := os.WriteFile(filepath.Join(m.Dir, "account.json"), b, 0o600); err != nil {
			return nil, err
		}
	}
	res, err := client.Certificate.Obtain(certificate.ObtainRequest{Domains: []string{m.IP}, Bundle: true, Profile: "shortlived"})
	if err != nil {
		return nil, err
	}
	leaf := m.install(res.Certificate, res.PrivateKey)
	if leaf == nil {
		return nil, errors.New("issued certificate does not load")
	}
	if err := writeFile(filepath.Join(m.Dir, "key.pem"), res.PrivateKey); err != nil {
		return nil, err
	}
	return leaf, writeFile(filepath.Join(m.Dir, "cert.pem"), res.Certificate)
}

// account is the ACME account: an EC key made once, kept in Dir.
func (m *Manager) account() (*user, error) {
	u := &user{email: m.Email}
	path := filepath.Join(m.Dir, "account.key")
	if b, err := os.ReadFile(path); err == nil {
		blk, _ := pem.Decode(b)
		if blk == nil {
			return nil, errors.New("account.key: not PEM")
		}
		k, err := x509.ParseECPrivateKey(blk.Bytes)
		if err != nil {
			return nil, err
		}
		u.key = k
	} else {
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, err
		}
		der, err := x509.MarshalECPrivateKey(k)
		if err != nil {
			return nil, err
		}
		if err := writeFile(path, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})); err != nil {
			return nil, err
		}
		u.key = k
	}
	if b, err := os.ReadFile(filepath.Join(m.Dir, "account.json")); err == nil {
		var reg registration.Resource
		if json.Unmarshal(b, &reg) == nil && reg.URI != "" {
			u.reg = &reg
		}
	}
	return u, nil
}

func writeFile(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

type user struct {
	email string
	key   crypto.PrivateKey
	reg   *registration.Resource
}

func (u *user) GetEmail() string                        { return u.email }
func (u *user) GetRegistration() *registration.Resource { return u.reg }
func (u *user) GetPrivateKey() crypto.PrivateKey        { return u.key }

// slogWriter routes lego's log lines to slog.
type slogWriter struct{ l *slog.Logger }

func (w slogWriter) Write(p []byte) (int, error) {
	w.l.Info("acme", "msg", string(p))
	return len(p), nil
}
