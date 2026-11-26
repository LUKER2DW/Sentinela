package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LUKER2DW/sentinela/internal/auth"
	"github.com/LUKER2DW/sentinela/internal/store"
)

func src(ip string) source { return source{addr: limiterKey(ip), net: limiterNet(ip)} }

// A stranger who knows a username must not be able to lock the owner out of
// remote login by spending the account's attempts: the count is per account
// and per source, and only failures count.
func TestAccountLockIsPerSource(t *testing.T) {
	_, srv := newTestServer(t)
	lim := srv.pubLim
	admin := accountOf(nil, " Admin ")
	for i := 0; i < 10; i++ {
		lim.acctFailed(admin, src("203.0.113.9"))
	}
	if !lim.acctLocked(accountOf(nil, "admin"), src("203.0.113.9")) {
		t.Fatal("10 failures from one source must lock that source out of the account")
	}
	if lim.acctLocked(admin, src("198.51.100.4")) {
		t.Fatal("another source must not be locked by a stranger's failures")
	}
	if lim.acctLocked(accountOf(nil, "someone-else"), src("203.0.113.9")) {
		t.Fatal("the lock is per account")
	}
	if srv.homeLim.acctLocked(admin, src("203.0.113.9")) {
		t.Fatal("home zones have no account lock")
	}
}

// A guess spread over many sources still hits the all-sources backstop, for
// real accounts and for names that match none alike.
func TestAccountLockBackstopForSpreadGuessing(t *testing.T) {
	for _, a := range []account{accountOf(&store.User{ID: 7}, "victim"), accountOf(nil, "victim")} {
		_, srv := newTestServer(t)
		for i := 0; i < 300; i++ {
			srv.pubLim.acctFailed(a, src(fmt.Sprintf("2001:db8:%x::1", i)))
		}
		if !srv.pubLim.acctLocked(a, src("192.0.2.77")) {
			t.Fatalf("%s: 300 failures from many sources must lock the account for everyone", a.key)
		}
	}
}

// A real account counts by id: its username and its e-mail share one budget
// (the audit found two independent 300/h budgets). Names that match no
// account live in tables of their own, so made-up names cannot fill or reset
// a real account's counters.
func TestAccountBudgetIsPerUserID(t *testing.T) {
	u := &store.User{ID: 42, Username: "ana", Email: "ana@example.com"}
	if accountOf(u, "ana") != accountOf(u, "ANA@example.com") {
		t.Fatal("username and e-mail of one account must share its key")
	}
	_, srv := newTestServer(t)
	lim := srv.pubLim
	lim.acctAll = auth.NewLimiterCap(acctFailsAll, time.Hour, 4)
	lim.nameAll = auth.NewLimiterCap(acctFailsAll, time.Hour, 4)
	victim := accountOf(u, "ana")
	for i := 0; i < acctFailsAll-1; i++ {
		lim.acctFailed(victim, src(fmt.Sprintf("2001:db8:1:%x::1", i)))
	}
	for i := 0; i < 1000; i++ { // a flood of filler names, each failing once
		lim.acctFailed(accountOf(nil, fmt.Sprintf("filler%d", i)), src(fmt.Sprintf("2001:db8:2:%x::1", i)))
	}
	if lim.acctLocked(victim, src("192.0.2.1")) {
		t.Fatal("locked before its 300th failure")
	}
	lim.acctFailed(victim, src("192.0.2.1"))
	if !lim.acctLocked(victim, src("192.0.2.2")) {
		t.Fatal("the victim's counter was reset by filler names")
	}
}

// A name that matches no account is keyed by a hash of the name (case and
// spaces ignored), so every key has the same small size: a flood of 254-byte
// names fills the name tables with 3 MB each, not 8.
func TestUnknownNameKeyIsHashed(t *testing.T) {
	long := strings.Repeat("x", 254)
	cases := []struct {
		a, b string
		same bool
	}{
		{" Ana ", "ana", true},
		{"ana", "ana2", false},
		{long, long[:253] + "y", false},
	}
	for _, c := range cases {
		ka, kb := accountOf(nil, c.a).key, accountOf(nil, c.b).key
		if (ka == kb) != c.same {
			t.Errorf("%.12q and %.12q: same key = %v, want %v", c.a, c.b, ka == kb, c.same)
		}
		if strings.Contains(ka, strings.ToLower(strings.TrimSpace(c.a))) {
			t.Errorf("key %q holds the name", ka)
		}
	}
	if short, full := accountOf(nil, "a").key, accountOf(nil, long).key; len(short) != len(full) || len(full) > 32 {
		t.Fatalf("key sizes %d and %d, want the same small size", len(short), len(full))
	}
}

// End to end: from one source, failures under the username and under the
// e-mail add up to the 10 an hour that source has for the account.
func TestAccountLockSharedByAliases(t *testing.T) {
	_, srv := newTestServer(t)
	ctx := context.Background()
	hash, err := auth.HashPassword("long enough pw")
	if err != nil {
		t.Fatal(err)
	}
	id, err := srv.Store.CreateFirstUser(ctx, "ana", hash)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Store.UpdateUserEmail(ctx, id, "ana@example.com"); err != nil {
		t.Fatal(err)
	}
	pub := newPublicServer(t, srv, ZonePublic)
	srv.pubLim.attempts = auth.NewLimiter(1000, time.Minute) // isolate the account limit
	srv.pubLim.attemptNets = auth.NewLimiter(1000, time.Minute)
	c := newClient(t, pub.URL)
	for i := 0; i < 10; i++ {
		name := "ana"
		if i%2 == 1 {
			name = "ANA@example.com"
		}
		if code, _, _ := c.do("POST", "/api/login", `{"username":"`+name+`","password":"wrong password"}`); code != 401 {
			t.Fatalf("attempt %d = %d, want 401", i+1, code)
		}
	}
	if code, _, body := c.do("POST", "/api/login", `{"username":"ana","password":"long enough pw"}`); code != 429 ||
		!strings.Contains(body, "account") {
		t.Fatalf("11th attempt over both aliases = %d %s, want 429", code, body)
	}
}

// One holder of many addresses shares a network budget in public zones: an
// IPv6 /48 (65 536 /64s) or an IPv4 /24 cannot multiply the per-address
// limits. Home zones have none (the whole LAN is one /24).
func TestPublicNetworkBudgets(t *testing.T) {
	_, srv := newTestServer(t)
	pub := srv.pubLim
	for i := 0; i < failsPerNetHour; i++ {
		s := src(fmt.Sprintf("2001:db8:7:%x::1", i))
		if pub.tooManyFailures(s) {
			t.Fatalf("failure %d refused before the network budget", i)
		}
		pub.failed(s)
		srv.homeLim.failed(s)
	}
	if !pub.tooManyFailures(src("2001:db8:7:ffff::1")) {
		t.Fatal("a fresh /64 of a /48 that failed 100 times must be refused")
	}
	if pub.tooManyFailures(src("2001:db8:8::1")) {
		t.Fatal("another /48 must not be refused")
	}
	if srv.homeLim.tooManyFailures(src("2001:db8:7:ffff::1")) {
		t.Fatal("home zones have no network budget, and keep tables of their own")
	}

	for i := 0; i < attemptsPerNetMinute; i++ {
		if !pub.allowAttempt("login", src(fmt.Sprintf("198.51.100.%d", i+1))) {
			t.Fatalf("attempt %d refused before the network budget", i+1)
		}
	}
	if pub.allowAttempt("login", src("198.51.100.200")) {
		t.Fatal("a fresh address of a /24 that used 30 attempts this minute must be refused")
	}
	if !pub.allowAttempt("login", src("203.0.113.1")) {
		t.Fatal("another /24 must not be refused")
	}
	if !srv.homeLim.allowAttempt("login", src("198.51.100.200")) {
		t.Fatal("home attempts are counted apart from public ones")
	}
}

func TestLimiterNet(t *testing.T) {
	cases := []struct{ in, want string }{
		{"203.0.113.7", "203.0.113.0/24"},
		{"::ffff:203.0.113.7", "203.0.113.0/24"},
		{"2001:db8:1:2:aaaa::1", "2001:db8:1::/48"},
		{"fe80::1%eth0", "fe80::/48"},
		{"not an address", "not an address"},
	}
	for _, c := range cases {
		if got := limiterNet(c.in); got != c.want {
			t.Errorf("limiterNet(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// proxyLogin signs in through the loopback proxy listener as a client at ip
// (the tunnel forwards it in X-Forwarded-For).
func proxyLogin(h http.Handler, ip, name, password string) (int, string) {
	r := httptest.NewRequest("POST", "/api/login",
		strings.NewReader(`{"username":"`+name+`","password":"`+password+`"}`))
	r.RemoteAddr = "127.0.0.1:41000"
	r.Header.Set("X-Forwarded-For", ip)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w.Code, w.Body.String()
}

// fill records n made-up keys in l, each failing once, so the table is full,
// then keeps going with fresh keys past any limit (the flood goes on).
func fill(l *auth.Limiter, prefix string, n int) {
	for i := 0; i < n+2*acctFailsAll; i++ {
		l.Allow(fmt.Sprintf("%s%d", prefix, i))
	}
}

// The review of the limiter fix: once a public table was full, new keys
// shared one counter, so a flood (free with over-long login names) made
// every new internet source, the owner's included, get 429 before its
// password was checked. A key a full table cannot track must not inherit a
// lock: whatever table strangers fill, a fresh source with the right
// password still signs in, and made-up and real names answer alike.
func TestFullPublicTablesLockNobodyOut(t *testing.T) {
	type tables func(l *loginLimits)
	all := func(l *loginLimits) {
		for _, f := range []tables{
			func(l *loginLimits) { fill(l.attempts, "login:junk", auth.DefaultLimiterKeys) },
			func(l *loginLimits) { fill(l.attemptNets, "loginnet:junk", auth.DefaultLimiterKeys) },
			func(l *loginLimits) { fill(l.fails, "fail:junk", auth.DefaultLimiterKeys) },
			func(l *loginLimits) { fill(l.failNets, "failnet:junk", auth.DefaultLimiterKeys) },
			func(l *loginLimits) { fill(l.nameAcct, "name:junk", auth.DefaultLimiterKeys) },
			func(l *loginLimits) { fill(l.nameAll, "name:junk", auth.DefaultLimiterKeys) },
		} {
			f(l)
		}
	}
	cases := []struct {
		name string
		full tables
	}{
		{"attempts", func(l *loginLimits) { fill(l.attempts, "login:junk", auth.DefaultLimiterKeys) }},
		{"attempt networks", func(l *loginLimits) { fill(l.attemptNets, "loginnet:junk", auth.DefaultLimiterKeys) }},
		{"failures", func(l *loginLimits) { fill(l.fails, "fail:junk", auth.DefaultLimiterKeys) }},
		{"failure networks", func(l *loginLimits) { fill(l.failNets, "failnet:junk", auth.DefaultLimiterKeys) }},
		{"name tables", func(l *loginLimits) {
			fill(l.nameAcct, "name:junk", auth.DefaultLimiterKeys)
			fill(l.nameAll, "name:junk", auth.DefaultLimiterKeys)
		}},
		{"every public table", all},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, srv := newTestServer(t)
			hash, err := auth.HashPassword("long enough pw")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := srv.Store.CreateFirstUser(context.Background(), "owner", hash); err != nil {
				t.Fatal(err)
			}
			c.full(srv.pubLim)
			h := srv.PublicHandler(ZoneProxy)
			if code, body := proxyLogin(h, "203.0.113.50", "owner", "long enough pw"); code != http.StatusOK {
				t.Fatalf("owner from a fresh address = %d %s, want 200", code, body)
			}
			if code, body := proxyLogin(h, "2001:db8:aa::1", "owner", "long enough pw"); code != http.StatusOK {
				t.Fatalf("owner from a fresh IPv6 network = %d %s, want 200", code, body)
			}
			// A made-up name and a real one with a wrong password answer
			// the same way: no lock is inherited before any hash.
			made, madeBody := proxyLogin(h, "198.51.100.7", "nobody-here", "wrong password")
			real, realBody := proxyLogin(h, "192.0.2.7", "owner", "wrong password")
			if made != http.StatusUnauthorized || made != real || madeBody != realBody {
				t.Fatalf("made-up name = %d %s, real name = %d %s; want the same 401", made, madeBody, real, realBody)
			}
		})
	}
}

// The real account stays bounded while the name tables are full: a guess
// from one address still locks after 10 failures, and a guess spread over
// many addresses after 300.
func TestFullNameTablesKeepAccountLimits(t *testing.T) {
	_, srv := newTestServer(t)
	lim := srv.pubLim
	fill(lim.nameAcct, "name:junk", auth.DefaultLimiterKeys)
	fill(lim.nameAll, "name:junk", auth.DefaultLimiterKeys)
	owner := accountOf(&store.User{ID: 1}, "owner")
	for i := 0; i < acctFailsPerSource; i++ {
		lim.acctFailed(owner, src("203.0.113.9"))
	}
	if !lim.acctLocked(owner, src("203.0.113.9")) {
		t.Fatal("a real account must still lock per source")
	}
	for i := 0; i < acctFailsAll; i++ {
		lim.acctFailed(owner, src(fmt.Sprintf("2001:db8:%x::1", i)))
	}
	if !lim.acctLocked(owner, src("192.0.2.200")) {
		t.Fatal("a real account must still lock for everyone after 300 failures")
	}
	// An untracked made-up name is not counted, so it never locks.
	ghost := accountOf(nil, "ghost")
	for i := 0; i < 2*acctFailsAll; i++ {
		lim.acctFailed(ghost, src("203.0.113.10"))
	}
	if lim.acctLocked(ghost, src("203.0.113.10")) {
		t.Fatal("a name the full tables cannot track must not inherit a lock")
	}
}

// A login name over 254 bytes is refused without a hash, so it is not a
// failed sign-in: a flood of them can no longer fill the failure tables.
func TestOverlongNameIsNotAFailure(t *testing.T) {
	_, srv := newTestServer(t)
	srv.pubLim.attempts = auth.NewLimiter(1000, time.Minute) // isolate the failure limit
	srv.pubLim.attemptNets = auth.NewLimiter(1000, time.Minute)
	h := srv.PublicHandler(ZoneProxy)
	long := strings.Repeat("u", 300)
	before := auth.ArgonRuns()
	for i := 0; i < 3*failsPerHour; i++ {
		if code, body := proxyLogin(h, "203.0.113.60", long, "x"); code != http.StatusUnauthorized {
			t.Fatalf("attempt %d = %d %s, want 401", i+1, code, body)
		}
	}
	if n := auth.ArgonRuns() - before; n != 0 {
		t.Fatalf("over-long names ran %d hashes, want 0", n)
	}
	if srv.pubLim.tooManyFailures(src("203.0.113.60")) {
		t.Fatal("over-long names must not count as failed sign-ins")
	}
}
