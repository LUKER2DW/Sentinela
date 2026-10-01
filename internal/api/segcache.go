package api

import (
	"io"
	"strconv"
	"sync"
	"time"

	"github.com/LUKER2DW/sentinela/internal/recording"
)

// segCache keeps the fragment index of recently used segments and the
// timeline previews built from them. Reading an index walks every moof of
// a 5-minute file on the HDD (~300 ms); playback and every preview of the
// same segment then reuse it. Bounded: a few MB at most.
type segCache struct {
	mu      sync.Mutex
	idx     map[string]idxEntry
	idxKeys []string
	kf      map[string][]byte
	kfKeys  []string
}

type idxEntry struct {
	size int64
	mod  time.Time
	idx  *recording.FragIndex
}

const (
	maxCachedIndexes  = 96
	maxCachedPreviews = 64 // ~100–200 KB each
)

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
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.idx == nil {
		c.idx = map[string]idxEntry{}
	}
	if _, ok := c.idx[path]; !ok {
		c.idxKeys = append(c.idxKeys, path)
		if len(c.idxKeys) > maxCachedIndexes {
			delete(c.idx, c.idxKeys[0])
			c.idxKeys = c.idxKeys[1:]
		}
	}
	c.idx[path] = idxEntry{size, mod, idx}
	return idx, nil
}

// preview returns the keyframe file for the fragment at `at`, built once.
func (c *segCache) preview(path string, r io.ReaderAt, idx *recording.FragIndex, at time.Duration) ([]byte, error) {
	var start time.Duration
	for _, f := range idx.Fragments {
		if f.Start <= at && f.Offset < idx.End {
			start = f.Start
		}
	}
	key := path + "@" + strconv.FormatInt(int64(start), 10)
	c.mu.Lock()
	if b, ok := c.kf[key]; ok {
		c.mu.Unlock()
		return b, nil
	}
	c.mu.Unlock()
	b, _, err := idx.KeyFrame(r, at)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.kf == nil {
		c.kf = map[string][]byte{}
	}
	if _, ok := c.kf[key]; !ok {
		c.kfKeys = append(c.kfKeys, key)
		if len(c.kfKeys) > maxCachedPreviews {
			delete(c.kf, c.kfKeys[0])
			c.kfKeys = c.kfKeys[1:]
		}
	}
	c.kf[key] = b
	return b, nil
}
