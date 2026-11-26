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

// Limits of the hash queue: 2 hashes run at once (64 MiB each). Work for a
// request from home (LAN or tailnet, see WithHomeLane) may use both slots
// and queue up to argonMaxWaiting behind them. Any other work, above all
// anonymous logins from the internet, runs on at most argonPublicSlots slot
// with its own queue of argonPublicMaxWaiting: a flood from outside keeps at
// most one core busy and can neither take every slot nor fill the queue that
// home sign-ins use. Nobody waits longer than argonMaxWait.
const (
	argonSlots            = 2
	argonMaxWaiting       = 8
	argonPublicSlots      = 1
	argonPublicMaxWaiting = 4
	argonMaxWait          = 5 * time.Second
)

// argonGate bounds concurrent hashes so a login burst cannot exhaust a small
// host's RAM, and bounds the queues behind them (length and wait) so a flood
// of requests cannot pile up work. Waiting honours the request context, so an
// abandoned request never runs its hash.
type argonGate struct {
	slots  chan struct{} // held by every running hash
	public chan struct{} // also held by every running public-lane hash
	// Each lane's queue. A public hash queues for its lane token, then
	// (holding it, so at most cap(public) of them) for a slot.
	homeWaiting, publicWaiting       atomic.Int32
	maxHomeWaiting, maxPublicWaiting int32
	maxWait                          time.Duration
}

func newArgonGate(slots, maxWaiting, publicSlots, maxPublicWaiting int, maxWait time.Duration) *argonGate {
	return &argonGate{slots: make(chan struct{}, slots), public: make(chan struct{}, publicSlots),
		maxHomeWaiting: int32(maxWaiting), maxPublicWaiting: int32(maxPublicWaiting), maxWait: maxWait}
}

var argonSem = newArgonGate(argonSlots, argonMaxWaiting, argonPublicSlots, argonPublicMaxWaiting, argonMaxWait)

// argonRuns counts argon2id computations since start (hashes and checks).
var argonRuns atomic.Uint64

// ArgonRuns is how many argon2id computations ran since start; tests use it
// to prove a code path did not hash.
func ArgonRuns() uint64 { return argonRuns.Load() }

type homeLaneKey struct{}

// WithHomeLane marks ctx as belonging to a request from a private zone (LAN
// or tailnet): its password hashes may use every slot. Work without the mark
// gets the restricted public lane, so a path that forgets it fails safe.
func WithHomeLane(ctx context.Context) context.Context {
	return context.WithValue(ctx, homeLaneKey{}, true)
}

// HomeLane reports whether ctx carries the WithHomeLane mark.
func HomeLane(ctx context.Context) bool {
	home, _ := ctx.Value(homeLaneKey{}).(bool)
	return home
}

// take puts a token in ch: at once when there is room, otherwise after
// waiting, unless the queue counted by waiting already holds max (ErrBusy).
// The wait ends with ErrBusy at deadline or with ctx's error. A nil waiting
// means the caller bounds the waiters itself.
func take(ctx context.Context, ch chan struct{}, waiting *atomic.Int32, max int32, deadline time.Time) error {
	select {
	case ch <- struct{}{}:
		return nil
	default:
	}
	if waiting != nil {
		if waiting.Add(1) > max {
			waiting.Add(-1)
			return ErrBusy
		}
		defer waiting.Add(-1)
	}
	t := time.NewTimer(time.Until(deadline))
	defer t.Stop()
	select {
	case ch <- struct{}{}:
		if err := ctx.Err(); err != nil { // gave up while we got the token
			<-ch
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return ErrBusy
	}
}

// acquire takes a slot on ctx's lane. It returns ErrBusy when that lane's
// queue is full or the wait exceeds maxWait, and the context's error when
// ctx ends first. On success the caller must call release once.
func (g *argonGate) acquire(ctx context.Context) (release func(), err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(g.maxWait)
	if HomeLane(ctx) {
		if err := take(ctx, g.slots, &g.homeWaiting, g.maxHomeWaiting, deadline); err != nil {
			return nil, err
		}
		return func() { <-g.slots }, nil
	}
	if err := take(ctx, g.public, &g.publicWaiting, g.maxPublicWaiting, deadline); err != nil {
		return nil, err
	}
	if err := take(ctx, g.slots, nil, 0, deadline); err != nil {
		<-g.public
		return nil, err
	}
	return func() { <-g.slots; <-g.public }, nil
}

// acquireWait takes a slot with no queue or time limit (internal one-time
// work only, never request-driven).
func (g *argonGate) acquireWait() (func(), error) {
	g.slots <- struct{}{}
	return func() { <-g.slots }, nil
}

// HashPassword returns a PHC-formatted argon2id hash. When the hash queue is
// saturated it fails with ErrBusy; request handlers use HashPasswordContext.
func HashPassword(pw string) (string, error) {
	return HashPasswordContext(context.Background(), pw)
}

// HashPasswordContext is HashPassword that gives up with ErrBusy (queue full
// or waited too long) or ctx's error instead of waiting without limit.
func HashPasswordContext(ctx context.Context, pw string) (string, error) {
	return hashPassword(pw, func() (func(), error) { return argonSem.acquire(ctx) })
}

func hashPassword(pw string, acquire func() (func(), error)) (string, error) {
	if err := CheckPasswordPolicy(pw); err != nil {
		return "", err
	}
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	release, err := acquire()
	if err != nil {
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
	return verifyPassword(hash, pw, func() (func(), error) { return argonSem.acquire(ctx) })
}

func verifyPassword(hash, pw string, acquire func() (func(), error)) (bool, error) {
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
	release, err := acquire()
	if err != nil {
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
	h, _ := hashPassword("timing-equalizer-not-a-password", argonSem.acquireWait)
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

// DefaultLimiterKeys is how many distinct keys a Limiter tracks at once.
// Keys come from the network, so the count must not follow the attacker. A
// short key (an address, a network, a user id, a hashed login name) costs
// about 135-160 bytes, so a full table is about 3 MB (measured).
const DefaultLimiterKeys = 20000

// Limiter is a fixed-window counter keyed by client (e.g. IP). It tracks at
// most maxKeys live keys and never forgets a live counter to make room:
// forgetting one would hand whoever it was counting a fresh window.
//
// While the table is full of unexpired keys a new key is not tracked: Allow
// lets it through uncounted and Over reports false for it. Anyone who can
// mint keys can fill a table, and that must never lock out someone else: a
// key that is not tracked never inherits a lock. Filling a table only means
// that new keys go uncounted in it until keys expire, so callers back every
// table strangers can fill with one they cannot (a network budget, accounts
// keyed by user id).
//
// Keys sit in a queue in the order their window started, which is also the
// order they expire, so expired keys are dropped from the front without
// scanning the table: every call costs amortised O(1) under the mutex.
type Limiter struct {
	mu      sync.Mutex
	max     int
	maxKeys int
	window  time.Duration
	hits    map[string]window
	queue   []string // keys by window start, oldest first, from head on
	head    int
	peak    int // largest len(hits) since the map was last rebuilt
	now     func() time.Time
	pruned  int // entries examined by prune, for tests
}

type window struct {
	start time.Time
	n     int
}

func (l *Limiter) expired(w window, now time.Time) bool { return now.Sub(w.start) > l.window }

// NewLimiter allows max events per window per key, over at most
// DefaultLimiterKeys keys.
func NewLimiter(max int, per time.Duration) *Limiter {
	return NewLimiterCap(max, per, DefaultLimiterKeys)
}

// NewLimiterCap is NewLimiter with an explicit cap on live keys.
func NewLimiterCap(max int, per time.Duration, maxKeys int) *Limiter {
	return &Limiter{max: max, maxKeys: maxKeys, window: per, hits: map[string]window{}, now: time.Now}
}

// prune drops expired keys from the front of the queue. A key's window only
// starts when it is pushed, so with a clock that does not go back the queue
// stays in expiry order. Callers hold l.mu.
func (l *Limiter) prune(now time.Time) {
	for l.head < len(l.queue) {
		k := l.queue[l.head]
		l.pruned++
		if w, ok := l.hits[k]; ok && !l.expired(w, now) {
			break
		}
		delete(l.hits, k)
		l.queue[l.head] = ""
		l.head++
	}
	// Give memory back after a flood: the queue's dead prefix, and a map
	// that shrank a lot (a Go map keeps its buckets after deletes).
	if l.head > 1024 && l.head*2 > len(l.queue) {
		l.queue = append([]string(nil), l.queue[l.head:]...)
		l.head = 0
	}
	if l.peak > 1024 && len(l.hits)*4 < l.peak {
		kept := make(map[string]window, len(l.hits))
		for k, w := range l.hits {
			kept[k] = w
		}
		l.hits, l.peak = kept, len(kept)
	}
}

func (l *Limiter) full() bool { return l.maxKeys > 0 && len(l.hits) >= l.maxKeys }

// Allow records one event for key and reports whether it is within the limit.
// A new key while the table is full is not tracked: it is allowed and not
// counted (see Limiter).
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.prune(now)
	if w, ok := l.hits[key]; ok {
		if l.expired(w, now) {
			// Only after the clock went back (the queue is then out of
			// order): restart the window in place.
			w = window{start: now}
		}
		w.n++
		l.hits[key] = w
		return w.n <= l.max
	}
	if l.full() {
		return true
	}
	l.hits[key] = window{start: now, n: 1}
	l.queue = append(l.queue, key)
	l.peak = max(l.peak, len(l.hits))
	return 1 <= l.max
}

// Over reports whether key already used up its window, without counting. A
// key the table does not track is never over.
func (l *Limiter) Over(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.prune(now)
	w, ok := l.hits[key]
	return ok && !l.expired(w, now) && w.n >= l.max
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
	Stream   string // "main", "sub" or "talk" (operators only)
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
