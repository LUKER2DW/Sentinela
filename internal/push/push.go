// Package push sends Web Push notifications (ADR-0013) with the standard
// library only: VAPID (RFC 8292, ES256 JWT) and message encryption
// (RFC 8291, aes128gcm per RFC 8188). The payload is end-to-end encrypted:
// the browser vendor's push service only sees ciphertext.
package push

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var b64 = base64.RawURLEncoding

// Keys is the server's VAPID key pair.
type Keys struct {
	priv *ecdsa.PrivateKey
	// Public is the uncompressed P-256 point, base64url (applicationServerKey).
	Public string
}

type keyFile struct {
	Private string `json:"private"`
	Public  string `json:"public"`
}

// LoadOrCreateKeys reads the VAPID keys from path (0600), creating them once.
func LoadOrCreateKeys(path string) (*Keys, error) {
	if raw, err := os.ReadFile(path); err == nil {
		var kf keyFile
		if err := json.Unmarshal(raw, &kf); err != nil {
			return nil, fmt.Errorf("vapid keys: %w", err)
		}
		d, err := b64.DecodeString(kf.Private)
		if err != nil {
			return nil, fmt.Errorf("vapid keys: %w", err)
		}
		priv, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), d)
		if err != nil {
			return nil, fmt.Errorf("vapid keys: %w", err)
		}
		return newKeys(priv)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	k, err := newKeys(priv)
	if err != nil {
		return nil, err
	}
	d, err := priv.Bytes()
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(keyFile{Private: b64.EncodeToString(d), Public: k.Public})
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return nil, err
	}
	return k, nil
}

func newKeys(priv *ecdsa.PrivateKey) (*Keys, error) {
	pub, err := priv.PublicKey.Bytes()
	if err != nil {
		return nil, err
	}
	return &Keys{priv: priv, Public: b64.EncodeToString(pub)}, nil
}

// Subscription is what the browser's PushManager returns.
type Subscription struct {
	Endpoint string `json:"endpoint"`
	P256dh   string `json:"p256dh"`
	Auth     string `json:"auth"`
}

// allowedHosts are the push services of current browsers. Anything else is
// refused, so a logged-in user cannot make the server POST to an arbitrary
// (e.g. LAN) address.
var allowedHosts = []string{
	"fcm.googleapis.com", "android.googleapis.com", // Chrome, Edge on Android, Samsung
	".push.services.mozilla.com", // Firefox
	".push.apple.com",            // Safari / iOS
	".notify.windows.com",        // Edge on Windows (WNS)
}

// ValidEndpoint reports whether u is an https URL of a known push service.
func ValidEndpoint(u string) bool {
	p, err := url.Parse(u)
	if err != nil || p.Scheme != "https" || p.User != nil || p.Port() != "" {
		return false
	}
	h := strings.ToLower(p.Hostname())
	for _, a := range allowedHosts {
		if h == a || strings.HasPrefix(a, ".") && strings.HasSuffix(h, a) {
			return true
		}
	}
	return false
}

// ErrGone means the subscription expired or was revoked: delete it.
var ErrGone = errors.New("push subscription gone")

// Sender posts encrypted messages to push services.
type Sender struct {
	Keys    *Keys
	Subject string // "mailto:…" or an https URL identifying the sender
	Client  *http.Client
}

// Send delivers payload (≤ 3 KB of JSON) to one subscription.
func (s *Sender) Send(ctx context.Context, sub Subscription, payload []byte, ttl time.Duration) error {
	if !ValidEndpoint(sub.Endpoint) {
		return errors.New("push endpoint not allowed")
	}
	uaPub, err := b64.DecodeString(strings.TrimRight(sub.P256dh, "="))
	if err != nil {
		return fmt.Errorf("p256dh: %w", err)
	}
	auth, err := b64.DecodeString(strings.TrimRight(sub.Auth, "="))
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	body, err := Encrypt(payload, uaPub, auth, rand.Reader)
	if err != nil {
		return err
	}
	jwt, err := s.vapidJWT(sub.Endpoint, time.Now())
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.Endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("TTL", fmt.Sprint(int(ttl.Seconds())))
	req.Header.Set("Urgency", "high")
	req.Header.Set("Authorization", "vapid t="+jwt+", k="+s.Keys.Public)
	c := s.Client
	if c == nil {
		c = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return ErrGone
	case resp.StatusCode >= 300:
		return fmt.Errorf("push service: HTTP %d", resp.StatusCode)
	}
	return nil
}

// vapidJWT signs the ES256 token for the push service's origin.
func (s *Sender) vapidJWT(endpoint string, now time.Time) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	header := b64.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`))
	claims, _ := json.Marshal(map[string]any{
		"aud": u.Scheme + "://" + u.Host,
		"exp": now.Add(12 * time.Hour).Unix(),
		"sub": s.Subject,
	})
	signing := header + "." + b64.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signing))
	r, sgn, err := ecdsa.Sign(rand.Reader, s.Keys.priv, sum[:])
	if err != nil {
		return "", err
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	sgn.FillBytes(sig[32:])
	return signing + "." + b64.EncodeToString(sig), nil
}

// recordSize is the RFC 8188 record size advertised in the header.
const recordSize = 4096

// Encrypt implements RFC 8291 (aes128gcm) for one record.
func Encrypt(plaintext, uaPublic, authSecret []byte, random io.Reader) ([]byte, error) {
	if len(plaintext) > recordSize-16-1-86 {
		return nil, errors.New("push payload too large")
	}
	as, err := ecdh.P256().GenerateKey(random)
	if err != nil {
		return nil, err
	}
	salt := make([]byte, 16)
	if _, err := io.ReadFull(random, salt); err != nil {
		return nil, err
	}
	return encrypt(plaintext, uaPublic, authSecret, as, salt)
}

// encrypt is Encrypt with a given ephemeral key and salt (RFC 8291 test vector).
func encrypt(plaintext, uaPublic, authSecret []byte, as *ecdh.PrivateKey, salt []byte) ([]byte, error) {
	ua, err := ecdh.P256().NewPublicKey(uaPublic)
	if err != nil {
		return nil, fmt.Errorf("p256dh: %w", err)
	}
	cek, nonce, err := deriveKeys(as, ua, as.PublicKey().Bytes(), uaPublic, authSecret, salt)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	padded := append(append([]byte{}, plaintext...), 0x02) // last-record delimiter
	asPub := as.PublicKey().Bytes()
	header := make([]byte, 0, 16+4+1+len(asPub))
	header = append(header, salt...)
	header = binary.BigEndian.AppendUint32(header, recordSize)
	header = append(header, byte(len(asPub)))
	header = append(header, asPub...)
	return gcm.Seal(header, nonce, padded, nil), nil
}

// deriveKeys is the RFC 8291 §3.4 + RFC 8188 §2.2 key schedule. priv/peer are
// the local key and the other side's public key; uaPub and asPub are always
// the user agent's and the application server's public keys.
func deriveKeys(priv *ecdh.PrivateKey, peer *ecdh.PublicKey, asPub, uaPub, authSecret, salt []byte) (cek, nonce []byte, err error) {
	shared, err := priv.ECDH(peer)
	if err != nil {
		return nil, nil, err
	}
	prkKey, err := hkdf.Extract(sha256.New, shared, authSecret)
	if err != nil {
		return nil, nil, err
	}
	keyInfo := "WebPush: info\x00" + string(uaPub) + string(asPub)
	ikm, err := hkdf.Expand(sha256.New, prkKey, keyInfo, 32)
	if err != nil {
		return nil, nil, err
	}
	prk, err := hkdf.Extract(sha256.New, ikm, salt)
	if err != nil {
		return nil, nil, err
	}
	if cek, err = hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16); err != nil {
		return nil, nil, err
	}
	if nonce, err = hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12); err != nil {
		return nil, nil, err
	}
	return cek, nonce, nil
}
