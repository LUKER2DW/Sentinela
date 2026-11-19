// Package auth implements password hashing (argon2id), opaque session tokens,
// CSRF tokens, a login rate limiter and short-lived stream tickets.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
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

// ErrBusy means too many password hashes are already running or queued: the
// caller should answer "try again shortly" (HTTP 503) instead of waiting.
var ErrBusy = errors.New("auth: too many password checks in progress")

// Limits of the hash queue: 2 hashes run at once (64 MiB each); at most
// argonMaxWaiting more may wait, each for at most argonMaxWait.
const (
	argonSlots      = 2
	argonMaxWaiting = 8
	argonMaxWait    = 5 * time.Second
)

// argonGate bounds concurrent hashes so a login burst cannot exhaust a small
// host's RAM, and bounds the queue behind them (length and wait) so a flood
// of requests cannot pile up work. Waiting honours the request context, so an
// abandoned request never runs its hash.
type argonGate struct {
	slots      chan struct{}
	waiting    atomic.Int32
	maxWaiting int32
	maxWait    time.Duration
}

func newArgonGate(slots, maxWaiting int, maxWait time.Duration) *argonGate {
	return &argonGate{slots: make(chan struct{}, slots), maxWaiting: int32(maxWaiting), maxWait: maxWait}
}

var argonSem = newArgonGate(argonSlots, argonMaxWaiting, argonMaxWait)

// argonRuns counts argon2id computations since start (hashes and checks).
var argonRuns atomic.Uint64

// ArgonRuns is how many argon2id computations ran since start; tests use it
// to prove a code path did not hash.
func ArgonRuns() uint64 { return argonRuns.Load() }

// acquire takes a slot. It returns ErrBusy when the queue is full or the wait
// exceeds maxWait, and the context's error when ctx ends first. On nil the
// caller must call release.
func (g *argonGate) acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case g.slots <- struct{}{}:
		return nil
	default:
	}
	if g.waiting.Add(1) > g.maxWaiting {
		g.waiting.Add(-1)
		return ErrBusy
	}
	defer g.waiting.Add(-1)
	t := time.NewTimer(g.maxWait)
	defer t.Stop()
	select {
	case g.slots <- struct{}{}:
		if err := ctx.Err(); err != nil { // gave up while we got the slot
			<-g.slots
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return ErrBusy
	}
}

// acquireWait takes a slot with no queue or time limit (internal one-time
// work only, never request-driven).
func (g *argonGate) acquireWait() error {
	g.slots <- struct{}{}
	return nil
}

func (g *argonGate) release() { <-g.slots }

// HashPassword returns a PHC-formatted argon2id hash. When the hash queue is
// saturated it fails with ErrBusy; request handlers use HashPasswordContext.
func HashPassword(pw string) (string, error) {
	return HashPasswordContext(context.Background(), pw)
}

// HashPasswordContext is HashPassword that gives up with ErrBusy (queue full
// or waited too long) or ctx's error instead of waiting without limit.
func HashPasswordContext(ctx context.Context, pw string) (string, error) {
	return hashPassword(pw, func() error { return argonSem.acquire(ctx) }, argonSem.release)
}

func hashPassword(pw string, acquire func() error, release func()) (string, error) {
	if err := CheckPasswordPolicy(pw); err != nil {
		return "", err
	}
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	if err := acquire(); err != nil {
		return "", err
	}
	argonRuns.Add(1)
	key := argon2.IDKey([]byte(pw), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	release()
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemory, argonTime, argonThreads,
		b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// VerifyPassword checks pw against a hash produced by HashPassword. It is
// false when the hash queue is saturated (fail closed); request handlers use
// VerifyPasswordContext to tell "busy" from "wrong".
func VerifyPassword(hash, pw string) bool {
	ok, _ := VerifyPasswordContext(context.Background(), hash, pw)
	return ok
}

// VerifyPasswordContext is VerifyPassword with a bounded, cancellable wait:
// the error is ErrBusy or ctx's error when no hash was run.
func VerifyPasswordContext(ctx context.Context, hash, pw string) (bool, error) {
	return verifyPassword(hash, pw, func() error { return argonSem.acquire(ctx) }, argonSem.release)
}

func verifyPassword(hash, pw string, acquire func() error, release func()) (bool, error) {
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, nil
	}
	var m uint32
	var t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil || m > 256*1024 || t > 10 {
		return false, nil
	}
	b64 := base64.RawStdEncoding
	salt, err1 := b64.DecodeString(parts[4])
	want, err2 := b64.DecodeString(parts[5])
	if err1 != nil || err2 != nil || len(want) == 0 {
		return false, nil
	}
	if err := acquire(); err != nil {
		return false, err
	}
	argonRuns.Add(1)
	got := argon2.IDKey([]byte(pw), salt, t, m, p, uint32(len(want)))
	release()
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// dummyHash is verified against when the user does not exist, to keep timing
// uniform. Computed on first use: an argon2id hash costs 64 MB, which would
// otherwise sit in RSS from process start (budget: 80 MB, AGENTS.md §2.3).
// It waits for a slot (once, never cached as a failure).
var dummyHash = sync.OnceValue(func() string {
	h, _ := hashPassword("timing-equalizer-not-a-password", argonSem.acquireWait, argonSem.release)
	return h
})

// VerifyDummy burns the same time as a real verification (false and no work
// when the queue is saturated).
func VerifyDummy(pw string) { _ = VerifyDummyContext(context.Background(), pw) }

// VerifyDummyContext is VerifyDummy with a bounded, cancellable wait: the
// error is ErrBusy or ctx's error when no hash was run.
func VerifyDummyContext(ctx context.Context, pw string) error {
	_, err := VerifyPasswordContext(ctx, dummyHash(), pw)
	return err
}

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

// DefaultLimiterKeys is how many distinct keys a Limiter tracks at once
// (about 150 bytes each, so a few MB at most). Keys come from the network, so
// the count must not follow the attacker.
const DefaultLimiterKeys = 20000

// Limiter is a fixed-window counter keyed by client (e.g. IP). It tracks at
// most maxKeys live keys: when full of unexpired keys, Allow refuses a new key
// (fail closed) rather than evicting a key that may be counting someone's
// failures. Expired keys are swept at most once per sweepEvery, not on every
// call, so the cost under the mutex stays amortised O(1).
type Limiter struct {
	mu         sync.Mutex
	max        int
	maxKeys    int
	window     time.Duration
	hits       map[string]*window
	now        func() time.Time
	sweepEvery time.Duration
	nextSweep  time.Time
	lastSweep  time.Time
	sweeps     int // number of full scans, for tests
}

type window struct {
	start time.Time
	n     int
}

// NewLimiter allows max events per window per key, over at most
// DefaultLimiterKeys keys.
func NewLimiter(max int, per time.Duration) *Limiter {
	return NewLimiterCap(max, per, DefaultLimiterKeys)
}

// NewLimiterCap is NewLimiter with an explicit cap on live keys.
func NewLimiterCap(max int, per time.Duration, maxKeys int) *Limiter {
	every := min(max64(per/2, time.Second), time.Minute)
	return &Limiter{max: max, maxKeys: maxKeys, window: per, hits: map[string]*window{}, now: time.Now,
		sweepEvery: every}
}

func max64(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

// sweep drops expired keys. Callers hold l.mu.
func (l *Limiter) sweep(now time.Time) {
	l.sweeps++
	l.lastSweep, l.nextSweep = now, now.Add(l.sweepEvery)
	before := len(l.hits)
	for k, w := range l.hits {
		if now.Sub(w.start) > l.window {
			delete(l.hits, k)
		}
	}
	// A Go map keeps its buckets after deletes: rebuild one that shrank a lot.
	if removed := before - len(l.hits); removed > 1024 && removed > len(l.hits) {
		kept := make(map[string]*window, len(l.hits))
		for k, w := range l.hits {
			kept[k] = w
		}
		l.hits = kept
	}
}

// evictProbes is how many map entries Allow looks at when it needs room.
const evictProbes = 64

// makeRoom frees one slot for a new key by evicting an entry that has not
// used up its limit: a source that is already limited keeps its counter (that
// is the brute-force protection), while sources that only touched the limiter
// once or twice are the cheap ones to forget. It looks at a bounded number of
// entries (Go's map order is random), so the cost stays constant; false means
// every entry it saw is limited and the caller should refuse the new key.
// Callers hold l.mu.
func (l *Limiter) makeRoom() bool {
	probes := 0
	for k, w := range l.hits {
		if w.n < l.max {
			delete(l.hits, k)
			return true
		}
		if probes++; probes >= evictProbes {
			break
		}
	}
	return false
}

// Allow records one event for key and reports whether it is within the limit.
// When the limiter is full of live keys it makes room by forgetting a source
// that is not limited; only if it finds none is the new key refused (fail
// closed), so a flood of fresh sources cannot lock genuine users out.
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	full := l.maxKeys > 0 && len(l.hits) >= l.maxKeys
	// Once per interval; when full, at most once a second so a flood of new
	// keys cannot make every call a full scan.
	if !now.Before(l.nextSweep) || full && now.Sub(l.lastSweep) >= time.Second {
		l.sweep(now)
	}
	w, ok := l.hits[key]
	if !ok || now.Sub(w.start) > l.window {
		if !ok && l.maxKeys > 0 && len(l.hits) >= l.maxKeys && !l.makeRoom() {
			return false
		}
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
