package publiccert

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// SelfSignedLifetime is long on purpose: the Android app pins this exact
// certificate (its SHA-256 is built into the APK), so a new one means a new
// APK. Browsers warn about a self-signed certificate whatever its lifetime.
const SelfSignedLifetime = 10 * 365 * 24 * time.Hour

// SelfSigned loads, or creates once, a self-signed certificate for ip in
// dir (cert.pem, key.pem, 0600). It is used where Let's Encrypt cannot
// validate the home (the ISP blocks inbound 80 and 443). It returns the
// listener's TLS config and the certificate's SHA-256 (hex, lower case),
// which the app pins (SENTINELA_PUBLIC_CERT_SHA256).
func SelfSigned(dir, ip string) (*tls.Config, string, error) {
	addr := net.ParseIP(ip)
	if addr == nil {
		return nil, "", errors.New("self-signed certificate needs an IP address")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, "", err
	}
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	c, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil || !covers(c, addr) {
		if err := createSelfSigned(certPath, keyPath, addr); err != nil {
			return nil, "", err
		}
		if c, err = tls.LoadX509KeyPair(certPath, keyPath); err != nil {
			return nil, "", err
		}
	}
	sum := sha256.Sum256(c.Certificate[0])
	return &tls.Config{MinVersion: tls.VersionTLS12, NextProtos: []string{"h2", "http/1.1"}, Certificates: []tls.Certificate{c}},
		hex.EncodeToString(sum[:]), nil
}

// covers reports whether a stored certificate is for addr and still valid.
func covers(c tls.Certificate, addr net.IP) bool {
	leaf, err := x509.ParseCertificate(c.Certificate[0])
	if err != nil || time.Now().After(leaf.NotAfter) {
		return false
	}
	return slices.ContainsFunc(leaf.IPAddresses, addr.Equal)
}

func createSelfSigned(certPath, keyPath string, addr net.IP) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 126))
	if err != nil {
		return err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "Sentinela " + addr.String(), Organization: []string{"Sentinela"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(SelfSignedLifetime),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{addr},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return fmt.Errorf("self-signed certificate: %w", err)
	}
	kder, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	if err := writeFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder})); err != nil {
		return err
	}
	return writeFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}
