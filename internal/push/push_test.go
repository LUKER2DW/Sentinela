package push

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// decrypt is the user agent side of RFC 8291, used to check Encrypt.
func decrypt(t *testing.T, body []byte, ua *ecdh.PrivateKey, auth []byte) []byte {
	t.Helper()
	salt, rs, idlen := body[:16], binary.BigEndian.Uint32(body[16:20]), int(body[20])
	if rs != recordSize {
		t.Fatalf("record size %d", rs)
	}
	asPub := body[21 : 21+idlen]
	as, err := ecdh.P256().NewPublicKey(asPub)
	if err != nil {
		t.Fatal(err)
	}
	cek, nonce, err := deriveKeys(ua, as, asPub, ua.PublicKey().Bytes(), auth, salt)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := aes.NewCipher(cek)
	gcm, _ := cipher.NewGCM(block)
	plain, err := gcm.Open(nil, nonce, body[21+idlen:], nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if plain[len(plain)-1] != 0x02 {
		t.Fatal("missing last-record delimiter")
	}
	return plain[:len(plain)-1]
}

func TestEncryptRoundTrip(t *testing.T) {
	ua, _ := ecdh.P256().GenerateKey(rand.Reader)
	auth := make([]byte, 16)
	_, _ = rand.Read(auth)
	msg := []byte(`{"title":"Movimento","body":"Frente, 04:59"}`)
	body, err := Encrypt(msg, ua.PublicKey().Bytes(), auth, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if got := decrypt(t, body, ua, auth); string(got) != string(msg) {
		t.Fatalf("got %q", got)
	}
	wrong := make([]byte, 16)
	if _, err := Encrypt(make([]byte, 5000), ua.PublicKey().Bytes(), wrong, rand.Reader); err == nil {
		t.Fatal("oversized payload accepted")
	}
}

func TestValidEndpoint(t *testing.T) {
	for u, ok := range map[string]bool{
		"https://fcm.googleapis.com/fcm/send/abc":              true,
		"https://updates.push.services.mozilla.com/wpush/v2/x": true,
		"https://web.push.apple.com/QGu":                       true,
		"https://wns2-bl2p.notify.windows.com/w/?token=x":      true,
		"http://fcm.googleapis.com/fcm/send/abc":               false,
		"https://192.168.1.10/x":                             false,
		"https://fcm.googleapis.com.evil.example/x":            false,
		"https://evilpush.apple.com.example/x":                 false,
		"https://fcm.googleapis.com:8443/x":                    false,
		"https://user@fcm.googleapis.com/x":                    false,
		"https://notapush.services.mozilla.com.attacker.net/x": false,
	} {
		if ValidEndpoint(u) != ok {
			t.Errorf("ValidEndpoint(%q) = %v", u, !ok)
		}
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSendSignsAndEncrypts(t *testing.T) {
	keys, err := LoadOrCreateKeys(filepath.Join(t.TempDir(), "vapid.json"))
	if err != nil {
		t.Fatal(err)
	}
	again, _ := LoadOrCreateKeys(filepath.Join(t.TempDir(), "other.json"))
	if again.Public == keys.Public {
		t.Fatal("keys not random")
	}
	ua, _ := ecdh.P256().GenerateKey(rand.Reader)
	auth := make([]byte, 16)
	_, _ = rand.Read(auth)
	status := http.StatusCreated
	var gotBody []byte
	var gotAuth string
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		gotBody, _ = io.ReadAll(r.Body)
		gotAuth = r.Header.Get("Authorization")
		if r.Header.Get("Content-Encoding") != "aes128gcm" || r.Header.Get("TTL") != "60" {
			t.Errorf("headers %v", r.Header)
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	s := &Sender{Keys: keys, Subject: "mailto:admin@sentinela.invalid", Client: client}
	sub := Subscription{Endpoint: "https://fcm.googleapis.com/fcm/send/abc", P256dh: b64.EncodeToString(ua.PublicKey().Bytes()), Auth: b64.EncodeToString(auth)}
	if err := s.Send(context.Background(), sub, []byte(`{"t":1}`), time.Minute); err != nil {
		t.Fatal(err)
	}
	if string(decrypt(t, gotBody, ua, auth)) != `{"t":1}` {
		t.Fatal("payload mismatch")
	}
	// VAPID: "vapid t=<jwt>, k=<key>", ES256 over header.claims with aud = origin.
	parts := strings.SplitN(strings.TrimPrefix(gotAuth, "vapid t="), ", k=", 2)
	if len(parts) != 2 || parts[1] != keys.Public {
		t.Fatalf("authorization %q", gotAuth)
	}
	seg := strings.Split(parts[0], ".")
	claimsRaw, _ := b64.DecodeString(seg[1])
	var claims map[string]any
	_ = json.Unmarshal(claimsRaw, &claims)
	if claims["aud"] != "https://fcm.googleapis.com" || claims["sub"] != "mailto:admin@sentinela.invalid" {
		t.Fatalf("claims %v", claims)
	}
	sig, _ := b64.DecodeString(seg[2])
	pubRaw, _ := b64.DecodeString(keys.Public)
	pub, err := ecdsa.ParseUncompressedPublicKey(keys.priv.Curve, pubRaw)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(seg[0] + "." + seg[1]))
	if !ecdsa.Verify(pub, sum[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		t.Fatal("bad VAPID signature")
	}
	status = http.StatusGone
	if err := s.Send(context.Background(), sub, []byte(`{}`), time.Minute); err != ErrGone {
		t.Fatalf("gone: %v", err)
	}
	sub.Endpoint = "https://192.168.1.10/x"
	if err := s.Send(context.Background(), sub, []byte(`{}`), time.Minute); err == nil {
		t.Fatal("LAN endpoint accepted")
	}
}

// RFC 8291 Appendix A: the published example must encrypt byte for byte.
func TestRFC8291Vector(t *testing.T) {
	d := func(v string) []byte {
		b, err := b64.DecodeString(v)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	as, err := ecdh.P256().NewPrivateKey(d("yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw"))
	if err != nil {
		t.Fatal(err)
	}
	uaPub := d("BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4")
	got, err := encrypt([]byte("When I grow up, I want to be a watermelon"), uaPub, d("BTBZMqHH6r4Tts7J_aSIgg"), as, d("DGv6ra1nlYgDCS1FRnbzlw"))
	if err != nil {
		t.Fatal(err)
	}
	want := "DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A_yl95bQpu6cVPTpK4Mqgkf1CXztLVBSt2Ks3oZwbuwXPXLWyouBWLVWGNWQexSgSxsj_Qulcy4a-fN"
	if b64.EncodeToString(got) != want {
		t.Fatalf("got  %s\nwant %s", b64.EncodeToString(got), want)
	}
}
