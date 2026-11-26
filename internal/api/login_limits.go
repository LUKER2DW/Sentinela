package api

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/LUKER2DW/sentinela/internal/auth"
	"github.com/LUKER2DW/sentinela/internal/store"
)

// loginLimits are the sign-in budgets of one kind of zone. Home (LAN,
// tailnet) and public zones keep separate tables, so a flood from the
// internet can neither fill nor reset the counters used at home.
//
// A limiter never forgets a live counter, and a key it cannot track because
// its table is full is not counted and never locked (auth.Limiter). So
// filling a table locks nobody out: it only stops that table from counting
// new keys until its keys expire, and the tables behind it still hold. A
// source no longer counted per address still counts per network; a guess at
// a real account still counts in acct and acctAll, which strangers cannot
// fill (keyed by real user ids, and acctAll stops new failures of an account
// at 300 an hour); and the second factor has its own lock (auth.CodeGuard).
// Invite codes are the exception: their failures count only per address and
// per network, so with those tables full a guess at a code is bounded only
// by throughput. A code has about 49 bits and expires after 7 days (about
// 4e-6 odds at 5 000 tries a second for the whole week).
// Filling the attempt tables takes about 20 000 new sources a minute, the
// failure and name tables 20 000 failed sign-ins within the hour from at
// least 200 networks (each a password hash, except bad invite codes).
//
// Memory: keys are short (an address, a network, a user id or a hashed
// login name), 2.7 to 3.5 MB per full table (measured). Strangers can fill
// the six public tables other than acct and acctAll, home devices the two
// home ones: about 23 MB if all of them are full at once. Only a flood does
// that; at steady state the tables hold a few keys, inside the binary's
// 80 MB budget (AGENTS.md).
type loginLimits struct {
	public   bool
	attempts *auth.Limiter // sign-in, invite and setup attempts: 5 a minute per address
	fails    *auth.Limiter // failed sign-ins or invite codes: 20 an hour per address
	// Public zones only. One holder of many addresses (an IPv6 /48 has
	// 65 536 /64s) shares a network budget, so it cannot multiply the
	// per-address limits, nor mint endless fresh address keys.
	attemptNets *auth.Limiter // 30 attempts a minute per network
	failNets    *auth.Limiter // 100 failures an hour per network
	acct        *auth.Limiter // failed logins of a real account from one address: 10 an hour
	acctAll     *auth.Limiter // failed logins of a real account from everywhere: 300 an hour
	nameAcct    *auth.Limiter // the same two for login names that match no account,
	nameAll     *auth.Limiter // in tables of their own (see accountOf)
}

// Budgets of the public tables (ADR-0019; docs/SECURITY.md rule 4).
const (
	attemptsPerMinute    = 5
	attemptsPerNetMinute = 30
	failsPerHour         = 20
	failsPerNetHour      = 100
	acctFailsPerSource   = 10
	acctFailsAll         = 300
)

func newLoginLimits(public bool) *loginLimits {
	l := &loginLimits{public: public,
		attempts: auth.NewLimiter(attemptsPerMinute, time.Minute), fails: auth.NewLimiter(failsPerHour, time.Hour)}
	if public {
		l.attemptNets = auth.NewLimiter(attemptsPerNetMinute, time.Minute)
		l.failNets = auth.NewLimiter(failsPerNetHour, time.Hour)
		l.acct, l.acctAll = auth.NewLimiter(acctFailsPerSource, time.Hour), auth.NewLimiter(acctFailsAll, time.Hour)
		l.nameAcct, l.nameAll = auth.NewLimiter(acctFailsPerSource, time.Hour), auth.NewLimiter(acctFailsAll, time.Hour)
	}
	return l
}

// source is where a sign-in comes from, as the limits count it.
type source struct {
	addr string // IPv4 address or IPv6 /64 (limiterKey)
	net  string // IPv4 /24 or IPv6 /48 (limiterNet)
}

// limitsFor returns the tables of r's zone and r's source.
func (s *Server) limitsFor(r *http.Request) (*loginLimits, source) {
	ip := clientIP(r)
	src := source{addr: limiterKey(ip), net: limiterNet(ip)}
	if PublicZone(zoneFrom(r)) {
		return s.pubLim, src
	}
	return s.homeLim, src
}

// tooManyFailures: an address (an IPv6 /64) that failed 20 sign-ins or
// invite codes in an hour is refused until the hour is over, whatever the
// account; in public zones so is a network that failed 100.
func (l *loginLimits) tooManyFailures(src source) bool {
	return l.fails.Over("fail:"+src.addr) || l.public && l.failNets.Over("failnet:"+src.net)
}

// failed counts one failed sign-in or invite code from src.
func (l *loginLimits) failed(src source) {
	l.fails.Allow("fail:" + src.addr)
	if l.public {
		l.failNets.Allow("failnet:" + src.net)
	}
}

// allowAttempt counts one attempt of kind ("login", "invite", "setup") and
// reports whether it may go on. In public zones the network budget comes
// first: an attempt it refuses is not counted per address, so it adds no key.
func (l *loginLimits) allowAttempt(kind string, src source) bool {
	if l.public && !l.attemptNets.Allow(kind+"net:"+src.net) {
		return false
	}
	return l.attempts.Allow(kind + ":" + src.addr)
}

// account names who a public sign-in is for in the account budgets.
type account struct {
	key   string
	known bool
}

// accountOf: a real account counts by its id, so its username and e-mail
// share one budget. A name that matches no account counts by the name (lower
// case), in tables of its own: made-up names cannot fill the real accounts'
// tables, and they lock like a real account, so a lock does not tell which
// names exist. While those tables are full, a made-up name they cannot track
// does not lock (docs/SECURITY.md rule 4). The name is hashed (128 bits) so
// every key has the same small size, whatever the name's length: a full
// table of 254-byte names took about 8 MB, of hashes about 3 MB.
func accountOf(u *store.User, name string) account {
	if u != nil {
		return account{key: "user:" + strconv.FormatInt(u.ID, 10), known: true}
	}
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(name))))
	return account{key: "name:" + base64.RawURLEncoding.EncodeToString(sum[:16])}
}

func (l *loginLimits) acctTables(a account) (perSource, all *auth.Limiter) {
	if a.known {
		return l.acct, l.acctAll
	}
	return l.nameAcct, l.nameAll
}

// acctLocked: in a public zone, the account used up its failed attempts from
// this address, or from all sources together. Home zones have no account
// lock: a stranger on the internet must not lock the owner out at home.
func (l *loginLimits) acctLocked(a account, src source) bool {
	if !l.public {
		return false
	}
	per, all := l.acctTables(a)
	return per.Over(a.key+"|"+src.addr) || all.Over(a.key)
}

// acctFailed counts one failed login (bad password or bad code) of a in a
// public zone.
func (l *loginLimits) acctFailed(a account, src source) {
	if !l.public {
		return
	}
	per, all := l.acctTables(a)
	per.Allow(a.key + "|" + src.addr)
	all.Allow(a.key)
}
