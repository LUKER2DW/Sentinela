package api

import (
	"context"
	"errors"
	"io"
	"strconv"
	"sync"
	"time"

	"github.com/LUKER2DW/sentinela/internal/recording"
)

// segCache keeps the fragment index of recently used segments and the
// timeline previews built from them. Reading an index walks every moof of
// a 5-minute file on the HDD (~300 ms); playback and every preview of the
// same segment then reuse it.
//
// Its memory is bounded by bytes, not only by entries: the indexes by their
// fragment count, the previews by their size; a preview is built once at a
// time for a key (other requests for it wait), by at most
// maxPreviewBuilders at once, and at most maxPreviewResponses are served
// from memory at a time (an evicted preview stays alive until its response
// ends). A request beyond those gets errPreviewBusy and is served the
// fragment from the file instead (FragIndex.GOP).
type segCache struct {
	mu       sync.Mutex
	idx      map[string]idxEntry
	idxKeys  []string
	idxFrags int
	kf       map[string][]byte
	kfKeys   []string
	kfBytes  int
	building map[string]*kfCall
	builders int
	serving  int
}

type idxEntry struct {
	size int64
	mod  time.Time
	idx  *recording.FragIndex
}

// kfCall is a preview being built; done closes when b/err are set.
type kfCall struct {
	done chan struct{}
	b    []byte
	err  error
}

const (
	maxCachedIndexes   = 96
	maxCachedFragments = 200_000 // ~5 MB of fragment entries in total
	maxCachedPreviews  = 64      // ~100–200 KB each
	// maxCachedPreviewBytes caps the previews kept; one preview is at most
	// recording.MaxKeyFrameBytes (~2.5 MB).
	maxCachedPreviewBytes = 8 << 20
	maxPreviewBuilders    = 2
	maxPreviewResponses   = 8
)

var errPreviewBusy = errors.New("timeline previews are busy")

// index returns the segment's fragment index; a file still growing (the
// segment being recorded) is read again when its size changes.
func (c *segCache) index(path string, r io.ReaderAt, size int64, mod time.Time) (*recording.FragIndex, error) {
	c.mu.Lock()
	if e, ok := c.idx[path]; ok && e.size == size && e.mod.Equal(mod) {
		c.mu.Unlock()
		return e.idx, nil
	}
	c.mu.Unlock()
	idx, err := recording.ReadFragIndex(r, size)
	if err != nil {
		return nil, err
	}
	n := len(idx.Fragments)
	if n > maxCachedFragments/4 {
		return idx, nil // used once, not kept
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.idx == nil {
		c.idx = map[string]idxEntry{}
	}
	if old, ok := c.idx[path]; ok {
		c.idxFrags -= len(old.idx.Fragments)
	} else {
		c.idxKeys = append(c.idxKeys, path)
	}
	c.idx[path] = idxEntry{size, mod, idx}
	c.idxFrags += n
	c.trimIndexes()
	return idx, nil
}

// trimIndexes evicts the oldest indexes beyond the entry and fragment
// limits, never the newest one. c.mu is held.
func (c *segCache) trimIndexes() {
	for len(c.idxKeys) > 1 && (len(c.idxKeys) > maxCachedIndexes || c.idxFrags > maxCachedFragments) {
		old := c.idxKeys[0]
		c.idxKeys = c.idxKeys[1:]
		c.idxFrags -= len(c.idx[old].idx.Fragments)
		delete(c.idx, old)
	}
}

// preview returns the keyframe file for the fragment at `at`, built once.
// done must be called when the response that uses the bytes has ended.
func (c *segCache) preview(ctx context.Context, path string, r io.ReaderAt, idx *recording.FragIndex, at time.Duration) (b []byte, done func(), err error) {
	var start time.Duration
	for _, f := range idx.Fragments {
		if f.Start <= at && f.Offset < idx.End {
			start = f.Start
		}
	}
	key := path + "@" + strconv.FormatInt(int64(start), 10)
	return c.keyFrame(ctx, key, func() ([]byte, error) {
		b, _, err := idx.KeyFrame(r, at)
		return b, err
	})
}

// keyFrame is preview's cache: the cached bytes, the result of a build
// already running for key, or a new build when a builder is free.
func (c *segCache) keyFrame(ctx context.Context, key string, build func() ([]byte, error)) ([]byte, func(), error) {
	c.mu.Lock()
	if c.serving >= maxPreviewResponses {
		c.mu.Unlock()
		return nil, nil, errPreviewBusy
	}
	c.serving++
	var once sync.Once
	release := func() {
		once.Do(func() {
			c.mu.Lock()
			c.serving--
			c.mu.Unlock()
		})
	}
	if b, ok := c.kf[key]; ok {
		c.mu.Unlock()
		return b, release, nil
	}
	call, running := c.building[key]
	if !running {
		if c.builders >= maxPreviewBuilders || ctx.Err() != nil {
			c.mu.Unlock()
			release()
			return nil, nil, errPreviewBusy
		}
		c.builders++
		call = &kfCall{done: make(chan struct{})}
		if c.building == nil {
			c.building = map[string]*kfCall{}
		}
		c.building[key] = call
		c.mu.Unlock()
		// The build reads at most a few MB from the file: it finishes even
		// when this request is cancelled, for whoever waits on it.
		call.b, call.err = build()
		if call.err == nil && len(call.b) > recording.MaxKeyFrameBytes {
			call.b, call.err = nil, errors.New("preview too large")
		}
		c.mu.Lock()
		c.builders--
		delete(c.building, key)
		if call.err == nil {
			c.keep(key, call.b)
		}
		c.mu.Unlock()
		close(call.done)
	} else {
		c.mu.Unlock()
		select {
		case <-call.done:
		case <-ctx.Done():
			release()
			return nil, nil, ctx.Err()
		}
	}
	if call.err != nil {
		release()
		return nil, nil, call.err
	}
	return call.b, release, nil
}

// keep caches a preview, evicting the oldest ones to stay within the
// entry and byte limits. c.mu is held.
func (c *segCache) keep(key string, b []byte) {
	if len(b) > maxCachedPreviewBytes/4 {
		return
	}
	if c.kf == nil {
		c.kf = map[string][]byte{}
	}
	if old, ok := c.kf[key]; ok {
		c.kfBytes -= len(old)
	} else {
		c.kfKeys = append(c.kfKeys, key)
	}
	c.kf[key] = b
	c.kfBytes += len(b)
	for len(c.kfKeys) > 1 && (len(c.kfKeys) > maxCachedPreviews || c.kfBytes > maxCachedPreviewBytes) {
		old := c.kfKeys[0]
		c.kfKeys = c.kfKeys[1:]
		c.kfBytes -= len(c.kf[old])
		delete(c.kf, old)
	}
}
