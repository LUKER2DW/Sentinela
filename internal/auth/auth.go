// Package auth implements password hashing (argon2id), opaque session tokens,
// CSRF tokens, a login rate limiter and short-lived stream tickets.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Roles, from most to least privileged.
const (
	RoleAdmin    = "admin"
	RoleOperator = "operator"
	RoleViewer   = "viewer"
)

// ValidRole reports whether r is a known role.
func ValidRole(r string) bool { return r == RoleAdmin || r == RoleOperator || r == RoleViewer }

// RoleAtLeast reports whether have grants at least need.
func RoleAtLeast(have, need string) bool {
	rank := map[string]int{RoleViewer: 1, RoleOperator: 2, RoleAdmin: 3}
	return rank[have] >= rank[need] && rank[need] > 0
}

// argon2id parameters (docs/SECURITY.md): m=64 MiB, t=3, p=1.
const (
	argonTime    = 3
	argonMemory  = 64 * 1024
	argonThreads = 1
	argonKeyLen  = 32
	saltLen      = 16
)

// argonSem bounds concurrent hashes so a login burst cannot exhaust 4 GB RAM.
var argonSem = make(chan struct{}, 2)

// HashPassword returns a PHC-formatted argon2id hash.
func HashPassword(pw string) (string, error) {
	if err := CheckPasswordPolicy(pw); err != nil {
		return "", err
	}
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	argonSem <- struct{}{}
	key := argon2.IDKey([]byte(pw), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	<-argonSem
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemory, argonTime, argonThreads,
		b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// VerifyPassword checks pw against a hash produced by HashPassword.
func VerifyPassword(hash, pw string) bool {
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var m uint32
	var t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil || m > 256*1024 || t > 10 {
		return false
	}
	b64 := base64.RawStdEncoding
	salt, err1 := b64.DecodeString(parts[4])
	want, err2 := b64.DecodeString(parts[5])
	if err1 != nil || err2 != nil || len(want) == 0 {
		return false
	}
	argonSem <- struct{}{}
	got := argon2.IDKey([]byte(pw), salt, t, m, p, uint32(len(want)))
	<-argonSem
	return subtle.ConstantTimeCompare(got, want) == 1
}

// dummyHash is verified against when the user does not exist, to keep timing
// uniform. Computed on first use: an argon2id hash costs 64 MB, which would
// otherwise sit in RSS from process start (budget: 80 MB, AGENTS.md §2.3).
var dummyHash = sync.OnceValue(func() string {
	h, _ := HashPassword("timing-equalizer-not-a-password")
	return h
})

// VerifyDummy burns the same time as a real verification.
func VerifyDummy(pw string) { VerifyPassword(dummyHash(), pw) }

// MinPasswordLen is the shortest accepted password. The system is reachable
// from the internet (ADR-0019 P1), so 8 is no longer enough.
const MinPasswordLen = 10

// PassphraseLen: at this length one kind of character is enough.
const PassphraseLen = 16

// commonPasswords are refused whatever their length (lower case).
var commonPasswords = map[string]bool{
	"1234567890": true, "12345678910": true, "123456789012": true, "0123456789": true, "qwertyuiop": true,
	"password123": true, "senha12345": true, "senha123456": true, "sentinela123": true, "iloveyou123": true,
	"administrador": true, "admin12345": true, "abcdefghij": true, "1q2w3e4r5t": true, "qwerty12345": true,
}

// CheckPasswordPolicy: at least 10 characters mixing two kinds (letters,
// digits, symbols/spaces), or a 16+ character passphrase; no common
// passwords and no single repeated character.
func CheckPasswordPolicy(pw string) error {
	if utf8.RuneCountInString(pw) < MinPasswordLen {
		return fmt.Errorf("password must have at least %d characters", MinPasswordLen)
	}
	if len(pw) > 256 {
		return errors.New("password too long")
	}
	if commonPasswords[strings.ToLower(pw)] || strings.Count(pw, string([]rune(pw)[0])) == utf8.RuneCountInString(pw) {
		return errors.New("password too common")
	}
	var letters, digits, others bool
	for _, r := range pw {
		switch {
		case unicode.IsLetter(r):
			letters = true
		case unicode.IsDigit(r):
			digits = true
		default:
			others = true
		}
	}
	kinds := 0
	for _, k := range []bool{letters, digits, others} {
		if k {
			kinds++
		}
	}
	if kinds < 2 && utf8.RuneCountInString(pw) < PassphraseLen {
		return errors.New("password too weak: mix letters with numbers or symbols, or use 16+ characters")
	}
	return nil
}

// NewToken returns a random 256-bit URL-safe token.
func NewToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failure is unrecoverable
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// HashToken returns the SHA-256 of a token for storage.
func HashToken(tok string) []byte {
	h := sha256.Sum256([]byte(tok))
	return h[:]
}

// EqualToken compares two tokens in constant time.
func EqualToken(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

// ---------- rate limiter ----------

// Limiter is a fixed-window counter keyed by client (e.g. IP).
type Limiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	hits   map[string]*window
	now    func() time.Time
}

type window struct {
	start time.Time
	n     int
}

// NewLimiter allows max events per window per key.
func NewLimiter(max int, per time.Duration) *Limiter {
	return &Limiter{max: max, window: per, hits: map[string]*window{}, now: time.Now}
}

// Allow records one event for key and reports whether it is within the limit.
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if len(l.hits) > 10000 {
		for k, w := range l.hits {
			if now.Sub(w.start) > l.window {
				delete(l.hits, k)
			}
		}
	}
	w, ok := l.hits[key]
	if !ok || now.Sub(w.start) > l.window {
		l.hits[key] = &window{start: now, n: 1}
		return true
	}
	w.n++
	return w.n <= l.max
}

// Over reports whether key already used up its window, without counting.
func (l *Limiter) Over(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	w, ok := l.hits[key]
	return ok && l.now().Sub(w.start) <= l.window && w.n >= l.max
}

// ---------- invites ----------

// inviteAlphabet has no look-alikes (0/O, 1/I/L) so a code read aloud or
// typed from a message works.
const inviteAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

// NewInviteCode returns a random 10-character code as "XXXXX-XXXXX"
// (about 50 bits; redeeming it is rate limited).
func NewInviteCode() string {
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failure is unrecoverable
	}
	out := make([]byte, 0, 11)
	for i, x := range b {
		if i == 5 {
			out = append(out, '-')
		}
		out = append(out, inviteAlphabet[int(x)%len(inviteAlphabet)])
	}
	return string(out)
}

// NormalizeInviteCode upper-cases a typed code and drops spaces and dashes.
func NormalizeInviteCode(code string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(code) {
		if r != ' ' && r != '-' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ---------- stream tickets ----------

// Ticket grants one stream connection for one camera to one user.
type Ticket struct {
	UserID   int64
	CameraID string
	Stream   string // "main" or "sub"
	Expires  time.Time
}

// Tickets is an in-memory, single-use ticket store (lives in the API process).
type Tickets struct {
	mu  sync.Mutex
	ttl time.Duration
	m   map[string]Ticket // key: hex of token hash
	now func() time.Time
}

// NewTickets creates a store with the given TTL.
func NewTickets(ttl time.Duration) *Tickets {
	return &Tickets{ttl: ttl, m: map[string]Ticket{}, now: time.Now}
}

// Issue creates a ticket and returns the token to hand to the browser.
func (t *Tickets) Issue(userID int64, cameraID, stream string) (string, time.Time) {
	tok := NewToken()
	exp := t.now().Add(t.ttl)
	t.mu.Lock()
	defer t.mu.Unlock()
	for k, v := range t.m {
		if t.now().After(v.Expires) {
			delete(t.m, k)
		}
	}
	t.m[string(HashToken(tok))] = Ticket{UserID: userID, CameraID: cameraID, Stream: stream, Expires: exp}
	return tok, exp
}

// Redeem consumes a ticket. It fails if unknown, expired or already used.
func (t *Tickets) Redeem(tok string) (Ticket, bool) {
	key := string(HashToken(tok))
	t.mu.Lock()
	defer t.mu.Unlock()
	tk, ok := t.m[key]
	delete(t.m, key)
	if !ok || t.now().After(tk.Expires) {
		return Ticket{}, false
	}
	return tk, true
}
