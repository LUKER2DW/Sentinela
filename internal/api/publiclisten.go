package api

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"time"
)

// Requests with no session on a public listener get a bounded time from
// their first byte to their last: reading (headers and body) and writing
// (including what net/http still flushes after the handler). Their bodies
// are at most anonBodyMax and the largest static file a few MB.
const (
	anonReadTimeout  = 15 * time.Second
	anonWriteTimeout = 2 * time.Minute
	anonBodyMax      = 8 << 10 // sign-in, invite and logout forms
)

// HTTP/2 on the port-forwarded TLS listener (the only public listener with
// TLS; the funnel and proxy listeners are plain HTTP/1.1 behind a local
// proxy).
//
// What strangers can hold there, measured with Go 1.26 on amd64 (server
// side, through the whole middleware chain): an idle connection about 40 KB;
// a stalled stream (client window 0) about 24 KB plus its headers, at most
// publicHeaderMax; request DATA up to the connection's receive window, and
// only until closedDoor drops or reads the anonymous body (measured:
// dropping frees it at once). At most publicAnonConns anonymous connections
// are open (connPool evicts the heaviest network beyond that):
//
//	40 × (40 KB + 512 KiB + 32 streams × (24 KB + 16 KiB)) ≈ 75 MB (72 MiB)
//
// inside the serve container's 384 MiB next to the steady state (≤ 80 MB,
// AGENTS.md) and two argon2id hashes (128 MB). Each stream lives at most
// the anonymous deadlines (15 s reading, 2 min writing).
//
// The receive window also paces Cloud uploads, one stream per chunk: 512
// KiB per round trip is about 5 MB/s at 100 ms and 17 MB/s at 30 ms.
const (
	publicAnonConns  = 40
	publicStreams    = 32
	publicWindow     = 512 << 10
	publicHeaderMax  = 16 << 10
	publicFrameMax   = 16 << 10 // the protocol's minimum: no larger read buffer per connection
	publicWriteStall = 30 * time.Second
)

// ConfigurePublicServer sets up hs, the server of a public listener (funnel,
// proxy or port-forward). Every request starts with the anonymous deadlines;
// closedDoor lifts them for signed-in requests only, so nothing a stranger
// sends (an unread body, a zero HTTP/2 window, a request net/http answers by
// itself) holds a connection or a stream longer.
func ConfigurePublicServer(hs *http.Server) {
	hs.ReadTimeout, hs.WriteTimeout = anonReadTimeout, anonWriteTimeout
	hs.MaxHeaderBytes = publicHeaderMax
	hs.HTTP2 = &http.HTTP2Config{
		MaxConcurrentStreams:          publicStreams,
		MaxReceiveBufferPerStream:     publicWindow,
		MaxReceiveBufferPerConnection: publicWindow,
		MaxReadFrameSize:              publicFrameMax,
		// A peer that stops reading at the TCP level loses the connection.
		WriteByteTimeout: publicWriteStall,
	}
}

// serverBounded reports whether r's server already gives every request the
// anonymous deadlines (ConfigurePublicServer).
func serverBounded(r *http.Request) bool {
	hs, _ := r.Context().Value(http.ServerContextKey).(*http.Server)
	return hs != nil && hs.ReadTimeout > 0 && hs.ReadTimeout <= anonReadTimeout &&
		hs.WriteTimeout > 0 && hs.WriteTimeout <= anonWriteTimeout
}

// anonDeadlines bounds an anonymous request on a server that does not
// (the private listener, for proxy traffic it reclassified). They are not
// cleared when the request ends: net/http resets an HTTP/1 connection's read
// deadline before the next request, closedDoor clears the write deadline of
// a next request in a private zone, and an HTTP/2 deadline belongs to its
// stream.
func anonDeadlines(w http.ResponseWriter) {
	rc := http.NewResponseController(w)
	now := time.Now()
	_ = rc.SetReadDeadline(now.Add(anonReadTimeout))
	_ = rc.SetWriteDeadline(now.Add(anonWriteTimeout))
}

// liftDeadlines lets a signed-in request run as long as it needs (live
// streams, uploads, downloads); handlers set their own idle limits.
func liftDeadlines(w http.ResponseWriter) {
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Time{})
	_ = rc.SetWriteDeadline(time.Time{})
}

// dropBody discards an anonymous request's body: HTTP/2 keeps unread DATA in
// memory until the body is closed, and no anonymous route reads one.
func dropBody(r *http.Request) {
	if r.Body != nil && r.Body != http.NoBody {
		_ = r.Body.Close()
	}
	r.Body = http.NoBody
}

var errAnonBody = errors.New("request body too large")

// bufferBody reads an anonymous request's body (at most anonBodyMax) before
// its handler runs, then closes it, so a handler that waits (a password hash
// queue) holds no unread DATA.
func bufferBody(r *http.Request) error {
	if r.Body == nil || r.Body == http.NoBody {
		return nil
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, anonBodyMax+1))
	_ = r.Body.Close()
	if err != nil {
		return err
	}
	if len(b) > anonBodyMax {
		return errAnonBody
	}
	r.Body = io.NopCloser(bytes.NewReader(b))
	return nil
}
