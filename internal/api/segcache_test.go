package api

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LUKER2DW/sentinela/internal/recording"
)

// Concurrent requests for one preview build it once and share the bytes.
func TestPreviewBuiltOncePerKey(t *testing.T) {
	var c segCache
	var builds atomic.Int32
	gate := make(chan struct{})
	build := func() ([]byte, error) {
		builds.Add(1)
		<-gate
		return []byte("frame"), nil
	}
	var wg sync.WaitGroup
	results := make(chan []byte, 5)
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b, done, err := c.keyFrame(context.Background(), "k", build)
			if err != nil {
				t.Error(err)
				return
			}
			results <- b
			done()
		}()
	}
	// Let the waiters queue on the running build, then finish it.
	for {
		c.mu.Lock()
		n := c.serving
		c.mu.Unlock()
		if n == 5 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	close(gate)
	wg.Wait()
	close(results)
	for b := range results {
		if string(b) != "frame" {
			t.Fatalf("got %q", b)
		}
	}
	if n := builds.Load(); n != 1 {
		t.Fatalf("built %d times", n)
	}
	if c.serving != 0 || c.builders != 0 || len(c.building) != 0 {
		t.Fatalf("leaked: serving %d builders %d building %d", c.serving, c.builders, len(c.building))
	}
}

// Builders beyond the limit, and responses beyond theirs, are refused (the
// handler then serves the fragment from the file).
func TestPreviewBuildersAndResponsesBounded(t *testing.T) {
	var c segCache
	gate := make(chan struct{})
	var wg sync.WaitGroup
	for i := range maxPreviewBuilders {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, done, err := c.keyFrame(context.Background(), "busy"+strconv.Itoa(i), func() ([]byte, error) {
				<-gate
				return []byte("x"), nil
			})
			if err == nil {
				done()
			}
		}()
	}
	waitFor(t, "preview state", func() bool { c.mu.Lock(); defer c.mu.Unlock(); return c.builders == maxPreviewBuilders })
	if _, _, err := c.keyFrame(context.Background(), "other", func() ([]byte, error) { return []byte("y"), nil }); !errors.Is(err, errPreviewBusy) {
		t.Fatalf("a third builder: %v", err)
	}
	close(gate)
	wg.Wait()

	// Hold every response slot: one more is refused even for a cached key.
	var dones []func()
	for range maxPreviewResponses {
		_, done, err := c.keyFrame(context.Background(), "busy0", nil)
		if err != nil {
			t.Fatal(err)
		}
		dones = append(dones, done)
	}
	if _, _, err := c.keyFrame(context.Background(), "busy0", nil); !errors.Is(err, errPreviewBusy) {
		t.Fatalf("one response too many: %v", err)
	}
	for _, d := range dones {
		d()
		d() // twice is harmless
	}
	if c.serving != 0 {
		t.Fatalf("serving %d after release", c.serving)
	}
}

// The kept previews stay within their byte budget, oldest out first.
func TestPreviewCacheBoundedByBytes(t *testing.T) {
	var c segCache
	big := bytes.Repeat([]byte{1}, maxCachedPreviewBytes/4)
	for i := range 10 {
		_, done, err := c.keyFrame(context.Background(), strconv.Itoa(i), func() ([]byte, error) { return big, nil })
		if err != nil {
			t.Fatal(err)
		}
		done()
	}
	if c.kfBytes > maxCachedPreviewBytes || len(c.kf) != 4 {
		t.Fatalf("kept %d bytes in %d previews", c.kfBytes, len(c.kf))
	}
	if _, ok := c.kf["9"]; !ok {
		t.Fatal("the newest preview was evicted")
	}
	// Larger than a quarter of the budget: served, not kept.
	_, done, err := c.keyFrame(context.Background(), "huge", func() ([]byte, error) { return append(big, 0), nil })
	if err != nil {
		t.Fatal(err)
	}
	done()
	if _, ok := c.kf["huge"]; ok {
		t.Fatal("an oversized preview was kept")
	}
	// Beyond what KeyFrame can return: refused.
	if _, _, err := c.keyFrame(context.Background(), "absurd", func() ([]byte, error) {
		return make([]byte, recording.MaxKeyFrameBytes+1), nil
	}); err == nil {
		t.Fatal("an absurd preview was served")
	}
}

// A request that gives up while waiting for another's build releases its slot.
func TestPreviewWaiterCancelled(t *testing.T) {
	var c segCache
	gate := make(chan struct{})
	go func() {
		_, done, err := c.keyFrame(context.Background(), "k", func() ([]byte, error) { <-gate; return []byte("x"), nil })
		if err == nil {
			done()
		}
	}()
	waitFor(t, "preview state", func() bool { c.mu.Lock(); defer c.mu.Unlock(); return c.builders == 1 })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := c.keyFrame(ctx, "k", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled waiter: %v", err)
	}
	close(gate)
	waitFor(t, "preview state", func() bool { c.mu.Lock(); defer c.mu.Unlock(); return c.serving == 0 && c.builders == 0 })
}

// The index cache is bounded by its total fragment count as well.
func TestIndexCacheBoundedByFragments(t *testing.T) {
	var c segCache
	c.idx = map[string]idxEntry{}
	add := func(path string, n int) {
		idx := &recording.FragIndex{Fragments: make([]recording.Fragment, n)}
		c.idx[path] = idxEntry{idx: idx}
		c.idxKeys = append(c.idxKeys, path)
		c.idxFrags += n
	}
	add("a", maxCachedFragments/2)
	add("b", maxCachedFragments/2)
	add("c", maxCachedFragments/4)
	c.trimIndexes()
	if _, ok := c.idx["a"]; ok || c.idxFrags > maxCachedFragments || len(c.idx) != 2 {
		t.Fatalf("fragments kept: %d in %d indexes", c.idxFrags, len(c.idx))
	}
	// One index alone above the limit is never evicted by itself.
	c = segCache{idx: map[string]idxEntry{}}
	add("solo", maxCachedFragments+1)
	c.trimIndexes()
	if len(c.idx) != 1 {
		t.Fatal("the newest index was evicted")
	}
}
