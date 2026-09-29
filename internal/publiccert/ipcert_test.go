package publiccert

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"log/slog"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-acme/lego/v4/challenge/tlsalpn01"
)

func selfSigned(t *testing.T, ip string, from, to time.Time) (certPEM, keyPEM []byte) {
	t.Helper()
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: from, NotAfter: to, IPAddresses: []net.IP{net.ParseIP(ip)}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	kder, _ := x509.MarshalECPrivateKey(k)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder})
}

func TestGetCertificate(t *testing.T) {
	dir := t.TempDir()
	m := &Manager{IP: "203.0.113.7", Dir: dir, Log: slog.New(slog.DiscardHandler)}
	browser := &tls.ClientHelloInfo{SupportedProtos: []string{"h2", "http/1.1"}}
	validation := &tls.ClientHelloInfo{ServerName: "7.113.0.203.in-addr.arpa", SupportedProtos: []string{tlsalpn01.ACMETLS1Protocol}}

	if _, err := m.getCertificate(browser); err == nil {
		t.Fatal("served a certificate before one was issued")
	}
	if _, err := m.getCertificate(validation); err == nil {
		t.Fatal("answered a validation with no challenge pending")
	}
	if err := m.Present("203.0.113.7", "tok", "keyauth"); err != nil {
		t.Fatal(err)
	}
	c, err := m.getCertificate(validation)
	if err != nil || c == nil {
		t.Fatalf("challenge: %v", err)
	}
	if _, err := m.getCertificate(browser); err == nil {
		t.Fatal("a browser got the challenge certificate")
	}
	_ = m.CleanUp("203.0.113.7", "tok", "keyauth")

	// A stored certificate for this IP is served after a restart; one for
	// another address is ignored (a new order follows).
	now := time.Now()
	cp, kp := selfSigned(t, "203.0.113.7", now.Add(-time.Hour), now.Add(143*time.Hour))
	_ = os.WriteFile(filepath.Join(dir, "cert.pem"), cp, 0o600)
	_ = os.WriteFile(filepath.Join(dir, "key.pem"), kp, 0o600)
	leaf := m.load()
	if leaf == nil {
		t.Fatal("stored certificate not loaded")
	}
	if c, err := m.getCertificate(browser); err != nil || c.Leaf != leaf {
		t.Fatalf("browser: %v", err)
	}
	if got, want := renewAt(leaf), leaf.NotAfter.Add(-48*time.Hour); !got.Equal(want) {
		t.Fatalf("renew at %v, want %v (a third of 6 days before expiry)", got, want)
	}
	other := &Manager{IP: "198.51.100.1", Dir: dir, Log: m.Log}
	if other.load() != nil {
		t.Fatal("loaded a certificate for another IP")
	}
}
