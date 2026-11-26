package apps

import "time"

// dirSize sums file sizes under dir, giving up after limit.
func dirSize(dir string, limit time.Duration) int64 {
	n, _ := dirSizeBounded(dir, limit, diskMaxEntries)
	return n
}

// dirSizeBounded sums the sizes of the regular files under dir, a folder a
// service can write while it is read. Nothing is followed or opened by a
// path the service could change: each folder is opened relative to its
// parent's open handle, never through a link, and only as a folder without
// blocking (a FIFO or device swapped in is skipped). Folders are read in
// batches of diskBatch names and the walk goes depth-first, holding one batch
// per level and at most diskMaxDepth levels, so its memory does not depend on
// the names or the shape of the tree. complete is false when the deadline,
// the entry cap or the depth cap stopped it (the sum is then a lower bound).
func dirSizeBounded(dir string, limit time.Duration, maxEntries int) (total int64, complete bool) {
	deadline := time.Now().Add(limit)
	top, err := openWalkDir(dir)
	if err != nil {
		return 0, true
	}
	type level struct {
		d     *walkDir
		names []string
		done  bool
	}
	stack := []level{{d: top}}
	defer func() {
		for _, l := range stack {
			l.d.close()
		}
	}()
	complete = true
	entries := 0
	for len(stack) > 0 {
		if time.Now().After(deadline) {
			return total, false
		}
		cur := &stack[len(stack)-1]
		if len(cur.names) == 0 {
			if cur.done {
				cur.d.close()
				stack = stack[:len(stack)-1]
				continue
			}
			names, err := cur.d.f.Readdirnames(diskBatch)
			cur.names, cur.done = names, err != nil // io.EOF or a read error: last batch
			continue
		}
		name := cur.names[0]
		cur.names = cur.names[1:]
		if entries++; entries > maxEntries {
			return total, false
		}
		isDir, size, ok := cur.d.stat(name)
		switch {
		case !ok:
		case !isDir:
			total += size
		case len(stack) >= diskMaxDepth:
			complete = false
		default:
			if child, err := cur.d.openDir(name); err == nil {
				stack = append(stack, level{d: child})
			}
		}
	}
	return total, complete
}
