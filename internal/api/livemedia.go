package api

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/LUKER2DW/sentinela/internal/auth"
)

// Live media lifetime. Closing the signalling WebSocket we proxy ends an MSE
// stream, but not a WebRTC one: go2rtc 1.9 keeps a WebRTC consumer until its
// PeerConnection closes, which a client may never do. So every proxied
// WebSocket gets a tag, sent to go2rtc as its User-Agent (go2rtc keeps it on
// the consumer it creates), and a sweep compares go2rtc's consumers with the
// sessions that opened them. A consumer whose session is gone, whose account
// is disabled, demoted or deleted, or whose camera the user may no longer
// see is stale.
//
// go2rtc 1.9.14 cannot drop one consumer, nor one stream's consumers:
// DELETE /api/streams only takes the name out of its map (the stream and
// its producer go on feeding the consumers they have) and rewrites go2rtc's
// config file. So the only cut is a restart of go2rtc, which drops every
// live view, talk, the detector's restream and event snapshots for a few
// seconds (recording does not go through go2rtc). Cuts are rationed:
//
//   - One cut takes every stale consumer seen so far, and cuts are at
//     least liveCutGap apart; stale consumers found in between wait for the
//     next slot.
//   - A cut is charged to each account whose stream it cut (an admin's
//     change is charged to the account it changed, not to the admin). The
//     first cut of an account in an hour is free, so revoking a stolen
//     session always works. Each further one refuses that account new
//     stream tickets for a cool-down (10 min, doubling up to a day) and is
//     written to the audit log; a day without a charged cut after the
//     cool-down ended clears it. A cooled-down account cannot open streams,
//     so it cannot cause another cut until the cool-down ends.
//   - A cut whose own consumers are still there after liveRetryGap did not
//     take: go2rtc is told to exit instead (Docker starts it again).
//
// The honest client (web/src/lib/player.ts) closes its PeerConnection when
// its WebSocket closes, so it never causes a cut. Bound: an admin change to
// the account, a revoked session or a sign-out sweeps at once; anything
// else (an expired session) is caught by the next periodic sweep
// (streamRecheckEvery). Either way the media stops about liveSettle later
// (the time a closing client gets to go away by itself), or at the next
// cut slot.

// liveTagPrefix marks the consumers this API opened in go2rtc.
const liveTagPrefix = "sentinela-live/"

// maxLiveStreams bounds the streams that may hold a go2rtc consumer at once
// (open WebSockets, plus WebRTC consumers that outlived theirs), and
// maxUserLiveStreams those of one account, so one account cannot take every
// slot. maxUserLiveRemembered bounds what one account leaves remembered
// (closed streams waiting for go2rtc to show their consumer is gone).
const (
	maxLiveStreams        = 1024
	maxUserLiveStreams    = 64
	maxUserLiveRemembered = 4 * maxUserLiveStreams
)

// Cut charges (see the package comment above).
const (
	cutFreeEvery = time.Hour // one free cut per account per hour
	cutCoolFirst = 10 * time.Minute
	cutCoolMax   = 24 * time.Hour
	cutQuiet     = 24 * time.Hour // after the cool-down, then strikes clear
)

// Tests shorten these.
var (
	// liveSettle is how long a consumer of a revoked stream may still take
	// to close by itself (a client that closed its PeerConnection: go2rtc
	// notices within about 5 s) before it is cut.
	liveSettle = 10 * time.Second
	// liveLinger is how long a closed stream is remembered while go2rtc
	// shows no consumer for it (go2rtc may still be dialling the camera).
	liveLinger = 30 * time.Second
	// liveForget bounds how long a closed stream is remembered at all while
	// go2rtc's consumers cannot be read.
	liveForget = 5 * time.Minute
	// liveCutGap is the least time between two cuts (go2rtc restarts).
	liveCutGap = 2 * time.Minute
	// liveRetryGap is how long a cut may take before the same consumers
	// still being there means it failed.
	liveRetryGap = 30 * time.Second
	// liveCloseSweep is how soon after a stream closes a sweep checks
	// whether go2rtc dropped its consumer (so it stops counting).
	liveCloseSweep = 3 * time.Second
)

var (
	errTooManyStreams = errors.New("too many live streams")
	errLiveCoolDown   = errors.New("live view paused for this account")
)

type liveStream struct {
	user     int64
	name     string                     // username, for the audit log
	allowed  func(context.Context) bool // re-reads the session behind it
	cancel   context.CancelFunc         // closes the proxied WebSocket
	open     bool
	closedAt time.Time
	revoked  bool
	seen     time.Time // the sweep that last found its consumer in go2rtc
	charged  bool      // a cut of its consumer was charged to its account
}

// cutAccount is what the cuts cost one account so far.
type cutAccount struct {
	lastFree  time.Time
	strikes   int
	coolUntil time.Time
}

type liveMedia struct {
	mu      sync.Mutex
	streams map[string]*liveStream // by tag
	kick    chan struct{}
	now     func() time.Time // tests replace it
	// swept is when the last successful read of go2rtc's consumers began.
	swept time.Time
	// The last cut: when, and which consumers it was for.
	cutAt   time.Time
	cutTags map[string]bool
	slot    *time.Timer // wakes the guard at the next cut slot
	closing *time.Timer // wakes the guard after streams closed
	charges map[int64]*cutAccount
}

func newLiveMedia() *liveMedia {
	return &liveMedia{streams: map[string]*liveStream{}, kick: make(chan struct{}, 1), now: time.Now, charges: map[int64]*cutAccount{}}
}

// holds reports whether a stream may still have a consumer in go2rtc: it is
// open, the last sweep saw its consumer, or it closed after that sweep began
// (and go2rtc's answer is not too old to tell). l.mu is held.
func (l *liveMedia) holds(ls *liveStream, now time.Time) bool {
	switch {
	case ls.open:
		return true
	case !l.swept.IsZero() && ls.seen.Equal(l.swept):
		return true
	}
	return !ls.closedAt.Before(l.swept) && now.Sub(ls.closedAt) < liveForget
}

// open registers a proxied WebSocket of user and returns its tag and the
// function to call when it closes.
func (l *liveMedia) open(user int64, name string, allowed func(context.Context) bool, cancel context.CancelFunc) (string, func(), error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if l.coolingLocked(user, now) > 0 {
		return "", nil, errLiveCoolDown
	}
	l.forgetOldLocked(now)
	all, mine, kept := 0, 0, 0
	for _, ls := range l.streams {
		h := l.holds(ls, now)
		if h {
			all++
		}
		if ls.user == user {
			kept++
			if h {
				mine++
			}
		}
	}
	if all >= maxLiveStreams || mine >= maxUserLiveStreams || kept >= maxUserLiveRemembered {
		return "", nil, errTooManyStreams
	}
	tag := hex.EncodeToString(auth.HashToken(auth.NewToken())[:12])
	ls := &liveStream{user: user, name: name, allowed: allowed, cancel: cancel, open: true}
	l.streams[tag] = ls
	return tag, func() {
		l.mu.Lock()
		ls.open, ls.closedAt = false, l.now()
		// Soon after, a sweep tells whether go2rtc dropped its consumer.
		if l.closing == nil {
			l.closing = time.AfterFunc(liveCloseSweep, func() {
				l.mu.Lock()
				l.closing = nil
				l.mu.Unlock()
				l.sweepSoon()
			})
		}
		l.mu.Unlock()
	}, nil
}

// forgetOldLocked drops closed streams nothing has been learnt about for
// liveForget (go2rtc's consumers could not be read). l.mu is held.
func (l *liveMedia) forgetOldLocked(now time.Time) {
	for tag, ls := range l.streams {
		if !ls.open && now.Sub(ls.closedAt) > liveForget && now.Sub(ls.seen) > liveForget {
			delete(l.streams, tag)
		}
	}
}

// revoke marks a stream as no longer allowed (its WebSocket is being closed)
// and asks for a sweep, so a WebRTC consumer it left behind goes too.
func (l *liveMedia) revoke(tag string) {
	l.mu.Lock()
	if ls := l.streams[tag]; ls != nil {
		ls.revoked = true
	}
	l.mu.Unlock()
	l.sweepSoon()
}

// sweepSoon asks the guard for a sweep now (an account or session changed).
func (l *liveMedia) sweepSoon() {
	select {
	case l.kick <- struct{}{}:
	default:
	}
}

// coolDown returns how long user is still refused new streams.
func (l *liveMedia) coolDown(user int64) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.coolingLocked(user, l.now())
}

func (l *liveMedia) coolingLocked(user int64, now time.Time) time.Duration {
	if c := l.charges[user]; c != nil && c.coolUntil.After(now) {
		return c.coolUntil.Sub(now)
	}
	return 0
}

// chargeLocked charges one cut to user: free once an hour while the account
// has no strike, otherwise a strike and a cool-down that doubles with each.
// l.mu is held.
func (l *liveMedia) chargeLocked(user int64, now time.Time) (free bool, cool time.Duration) {
	c := l.charges[user]
	if c == nil {
		c = &cutAccount{}
		l.charges[user] = c
	}
	if c.strikes > 0 && now.Sub(c.coolUntil) >= cutQuiet {
		c.strikes = 0
	}
	if c.strikes == 0 && (c.lastFree.IsZero() || now.Sub(c.lastFree) >= cutFreeEvery) {
		c.lastFree = now
		return true, 0
	}
	cool = cutCoolFirst
	for i := 0; i < c.strikes && cool < cutCoolMax; i++ {
		cool *= 2
	}
	cool = min(cool, cutCoolMax)
	c.strikes++
	c.coolUntil = now.Add(cool)
	return false, cool
}

// pruneChargesLocked forgets accounts whose cuts no longer cost anything.
func (l *liveMedia) pruneChargesLocked(now time.Time) {
	for user, c := range l.charges {
		if (c.strikes == 0 || now.Sub(c.coolUntil) >= cutQuiet) && now.Sub(c.lastFree) >= cutFreeEvery {
			delete(l.charges, user)
		}
	}
}

// LiveMediaChanged asks for an immediate sweep after access changed.
func (s *Server) LiveMediaChanged() { s.live.sweepSoon() }

// GuardLiveMedia sweeps go2rtc's consumers every streamRecheckEvery and
// whenever access changes, until ctx ends.
func (s *Server) GuardLiveMedia(ctx context.Context) {
	t := time.NewTicker(streamRecheckEvery())
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.live.kick:
		}
		s.sweepLive(ctx)
	}
}

// sweepLive closes the WebSockets of revoked streams and cuts consumers of
// revoked (or unknown) streams that are still there after liveSettle.
func (s *Server) sweepLive(ctx context.Context) {
	l := s.live
	l.mu.Lock()
	check := make(map[string]*liveStream, len(l.streams))
	for tag, ls := range l.streams {
		if !ls.revoked {
			check[tag] = ls
		}
	}
	l.mu.Unlock()
	var revoked []*liveStream
	for _, ls := range check {
		if !ls.allowed(ctx) {
			revoked = append(revoked, ls)
		}
	}
	l.mu.Lock()
	for _, ls := range revoked {
		ls.revoked = true
		if ls.open {
			ls.cancel()
		}
	}
	l.mu.Unlock()

	tags, err := s.readLive(ctx)
	if err != nil {
		s.Log.Debug("live media sweep: go2rtc consumers", "err", err)
		return
	}
	stale := l.stale(tags)
	if len(stale) == 0 {
		return
	}
	// A client that closed its PeerConnection is gone from go2rtc within
	// seconds; only a consumer that stays is cut.
	select {
	case <-ctx.Done():
		return
	case <-time.After(liveSettle):
	}
	if tags, err = s.readLive(ctx); err != nil {
		return
	}
	var still []string
	for _, tag := range stale {
		if tags[tag] && l.isStale(tag) {
			still = append(still, tag)
		}
	}
	if len(still) > 0 {
		s.cutLive(ctx, still)
	}
}

// readLive reads go2rtc's tagged consumers and updates what is known about
// each stream: seen, or forgotten when closed and gone.
func (s *Server) readLive(ctx context.Context) (map[string]bool, error) {
	l := s.live
	start := l.now()
	tags, err := s.go2rtcConsumerTags(ctx)
	l.mu.Lock()
	defer l.mu.Unlock()
	if err != nil {
		l.forgetOldLocked(l.now())
		return nil, err
	}
	l.swept = start
	for tag, ls := range l.streams {
		switch {
		case tags[tag]:
			ls.seen = start
		case ls.open:
		case ls.revoked || start.Sub(ls.closedAt) > liveLinger:
			delete(l.streams, tag)
		}
	}
	l.pruneChargesLocked(start)
	return tags, nil
}

// stale lists the tags go2rtc shows that belong to a revoked stream or to
// none this process opened (left from before a restart of the API).
func (l *liveMedia) stale(tags map[string]bool) []string {
	var out []string
	for tag := range tags {
		if l.isStale(tag) {
			out = append(out, tag)
		}
	}
	sort.Strings(out)
	return out
}

func (l *liveMedia) isStale(tag string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	ls := l.streams[tag]
	return ls == nil || ls.revoked
}

// go2rtcConsumerTags returns the tags of the consumers this API opened.
func (s *Server) go2rtcConsumerTags(ctx context.Context) (map[string]bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.go2rtc.String()+"/api/streams", nil)
	if err != nil {
		return nil, err
	}
	s.go2rtcAuth(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("go2rtc: " + resp.Status)
	}
	var raw map[string]struct {
		Consumers []struct {
			UserAgent string `json:"user_agent"`
		} `json:"consumers"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 8<<20)).Decode(&raw); err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, st := range raw {
		for _, c := range st.Consumers {
			if tag, ok := strings.CutPrefix(c.UserAgent, liveTagPrefix); ok && tag != "" {
				out[tag] = true
			}
		}
	}
	return out, nil
}

// liveBill is what one cut cost one account.
type liveBill struct {
	user int64
	name string
	free bool
	cool time.Duration
}

// cutLive drops every go2rtc consumer, still the stale ones, by restarting
// go2rtc, when the cut slot allows; otherwise it waits for the slot. If the
// last cut's own consumers are still there, that cut did not take and
// go2rtc is told to exit instead (Docker starts it again).
func (s *Server) cutLive(ctx context.Context, still []string) {
	l := s.live
	l.mu.Lock()
	now := l.now()
	retry := false
	for _, tag := range still {
		retry = retry || l.cutTags[tag]
	}
	gap := liveCutGap
	if retry {
		gap = liveRetryGap
	}
	if !l.cutAt.IsZero() {
		if wait := gap - now.Sub(l.cutAt); wait > 0 {
			l.wakeAtLocked(wait)
			l.mu.Unlock()
			return
		}
	}
	path := "/api/restart"
	if retry {
		path = "/api/exit?code=1"
	}
	l.cutAt = now
	l.cutTags = make(map[string]bool, len(still))
	billed := map[int64]bool{}
	var bills []liveBill
	unknown := 0
	for _, tag := range still {
		l.cutTags[tag] = true
		ls := l.streams[tag]
		if ls == nil {
			unknown++
			continue
		}
		if ls.charged || billed[ls.user] {
			ls.charged = true
			continue
		}
		ls.charged, billed[ls.user] = true, true
		free, cool := l.chargeLocked(ls.user, now)
		bills = append(bills, liveBill{user: ls.user, name: ls.name, free: free, cool: cool})
	}
	l.mu.Unlock()

	s.Log.Warn("live media: revoked stream still open in go2rtc, restarting it", "consumers", len(still), "unknown", unknown, "call", path)
	actx := context.WithoutCancel(ctx)
	for _, b := range bills {
		detail := "free (first this hour)"
		if !b.free {
			detail = fmt.Sprintf("new live views refused for %s", b.cool)
			s.Log.Warn("live media: account cooled down after repeated forced cuts", "user", b.name, "for", b.cool)
		}
		if err := s.Store.Audit(actx, b.user, b.name, "live.cut", "go2rtc", true, detail); err != nil {
			s.Log.Warn("audit write failed", "err", err)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.go2rtc.String()+path, nil)
	if err != nil {
		return
	}
	s.go2rtcAuth(req)
	if resp, err := http.DefaultClient.Do(req); err == nil {
		resp.Body.Close()
	} else {
		// go2rtc may re-execute before it answers.
		s.Log.Debug("live media: go2rtc restart", "err", err)
	}
}

// wakeAtLocked has the guard sweep again in d (the next cut slot). l.mu is
// held.
func (l *liveMedia) wakeAtLocked(d time.Duration) {
	if l.slot != nil {
		l.slot.Stop()
	}
	l.slot = time.AfterFunc(d, l.sweepSoon)
}
