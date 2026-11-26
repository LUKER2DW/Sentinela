package auth

import (
	"sync"
	"time"
)

// Second-factor lock (ADR-0019). A wrong TOTP code after the right password
// means someone holds the password. Address and account limits alone still
// let about 300 codes an hour through, enough to hit a 6-digit code (three
// are valid at any time) within weeks; with this lock a holder of the
// password gets 5 codes, then one more after 1, 2, 4 ... 32 minutes and then
// one an hour: about 24 a day.
const (
	// CodeLockAfter wrong codes in a row lock the factor (fewer are typos or
	// a phone clock off by a step).
	CodeLockAfter  = 5
	codeFirstLock  = time.Minute
	codeMaxLock    = time.Hour
	codeAlertEvery = 24 * time.Hour
	// codeForget: a run whose last wrong code is older than this starts
	// over, so a few typos weeks apart never add up to a lock. Waiting it
	// out gains a guesser nothing: a fresh run gives 11 codes in its first
	// hour, fewer than the 24 that the quiet day cost.
	codeForget = 24 * time.Hour
)

// CodeGuard counts wrong second-factor codes in a row per account. It is
// keyed by user id, so every login name and source address of the account
// share one count. Home zones (LAN, tailnet) and public zones count apart: a
// stranger on the internet who has the password can lock the outside door
// but never the owner's sign-in at home, and a good code at home also clears
// the outside lock. Only accounts whose password was right reach it, so it
// holds at most two entries per user. The count lives in memory; a restart,
// or a day without wrong codes, clears it.
type CodeGuard struct {
	mu  sync.Mutex
	m   map[codeKey]*codeState
	now func() time.Time
}

type codeKey struct {
	user   int64
	public bool
}

type codeState struct {
	fails   int
	last    time.Time // the last wrong code
	until   time.Time
	alerted time.Time
}

// NewCodeGuard returns an empty guard.
func NewCodeGuard() *CodeGuard {
	return &CodeGuard{m: map[codeKey]*codeState{}, now: time.Now}
}

// Locked returns how much longer user's factor is locked in public or home
// zones; 0 means it is not.
func (g *CodeGuard) Locked(user int64, public bool) time.Duration {
	g.mu.Lock()
	defer g.mu.Unlock()
	st := g.m[codeKey{user, public}]
	if st == nil {
		return 0
	}
	return max(st.until.Sub(g.now()), 0)
}

// CodeFailure is what one wrong code did.
type CodeFailure struct {
	Fails int           // wrong codes in a row, this one included
	Lock  time.Duration // the lock it started; 0 below CodeLockAfter
	Alert bool          // tell the account: first lock of a run, then at most daily (a good code or a restart starts a new run)
}

// Failed records a wrong code for user and locks the factor from the
// CodeLockAfter-th one in a row, each lock twice as long as the last, up to
// an hour. A run quiet for codeForget starts over.
func (g *CodeGuard) Failed(user int64, public bool) CodeFailure {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	k := codeKey{user, public}
	st := g.m[k]
	if st == nil || now.Sub(st.last) > codeForget {
		st = &codeState{}
		g.m[k] = st
	}
	st.fails++
	st.last = now
	f := CodeFailure{Fails: st.fails}
	if st.fails < CodeLockAfter {
		return f
	}
	f.Lock = codeMaxLock
	if n := st.fails - CodeLockAfter; n < 6 { // 1, 2, 4 ... 32 minutes
		f.Lock = min(codeFirstLock<<n, codeMaxLock)
	}
	st.until = now.Add(f.Lock)
	if st.alerted.IsZero() || now.Sub(st.alerted) >= codeAlertEvery {
		st.alerted, f.Alert = now, true
	}
	return f
}

// Passed clears user's count after a good code. A good code at home also
// clears the outside count: the owner proved they are there.
func (g *CodeGuard) Passed(user int64, public bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.m, codeKey{user, public})
	if !public {
		delete(g.m, codeKey{user, true})
	}
}
