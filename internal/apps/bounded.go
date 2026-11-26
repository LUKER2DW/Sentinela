package apps

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
)

// Bounds for reading files and listings that untrusted parties (a source
// repository, a Cloud folder, a service's own data folder) control.
const (
	maxDetectFile  = 1 << 20 // project files Detect reads
	maxDetectNames = 10_000  // top-level names Detect considers
	maxGitSmall    = 4 << 10 // .git/HEAD and a loose ref
	maxPackedRefs  = 1 << 20
	maxListing     = 5_000 // entries of one folder shown by the file explorer
	maxBrowse      = 2_000 // entries read of one folder by the local picker
)

var (
	errNotRegular = errors.New("not a regular file")
	errTooLarge   = errors.New("file too large")
)

// openRegular opens name below root for reading without blocking on a FIFO,
// and refuses anything that is not a regular file once opened (so a swap
// between check and open cannot slip a device or pipe in). Symbolic links
// that stay inside root are followed by os.Root; those leaving it fail.
func openRegular(root *os.Root, name string) (*os.File, os.FileInfo, error) {
	f, err := root.OpenFile(name, os.O_RDONLY|openNonblock, 0)
	if err != nil {
		return nil, nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	if !st.Mode().IsRegular() {
		f.Close()
		return nil, nil, errNotRegular
	}
	return f, st, nil
}

// readCapped reads a regular file of at most max bytes: the size is checked
// before reading, and the read itself is limited as well (a file may grow).
func readCapped(root *os.Root, name string, max int64) ([]byte, error) {
	f, st, err := openRegular(root, name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if st.Size() > max {
		return nil, errTooLarge
	}
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, errTooLarge
	}
	return b, nil
}

// Bounds of walkBounded: entries read per folder at a time, and folders deep.
const (
	walkBatch    = 128
	walkMaxDepth = 128
)

var (
	errTooManyEntries = errors.New("too many entries")
	errTooDeep        = errors.New("folders nested too deeply")
)

// walkBounded calls visit for the entries below root, depth-first, a folder
// before what it holds (p is slash-separated, relative to root). Unlike
// fs.WalkDir, which loads and sorts each folder's whole listing before the
// first callback, it reads folders in batches of walkBatch entries, stops
// with errTooManyEntries after maxEntries entries, with errTooDeep below
// walkMaxDepth folders, and when ctx ends, so a folder someone else filled
// (a Cloud folder, a service's files) cannot make it hold more than a few
// batches. Folders are opened through root without blocking and must still
// be folders once open. visit returning fs.SkipDir for a folder skips its
// content; with skipErrors, folders that cannot be opened or read are skipped
// instead of failing the walk.
func walkBounded(ctx context.Context, root *os.Root, maxEntries int, skipErrors bool, visit func(p string, d fs.DirEntry) error) error {
	open := func(p string) (*os.File, error) {
		f, err := root.OpenFile(p, os.O_RDONLY|openNonblock, 0)
		if err != nil {
			return nil, err
		}
		if st, err := f.Stat(); err != nil || !st.IsDir() {
			f.Close()
			if err == nil {
				err = fmt.Errorf("%s: not a folder", p)
			}
			return nil, err
		}
		return f, nil
	}
	top, err := open(".")
	if err != nil {
		return err
	}
	type level struct {
		f     *os.File
		dir   string
		batch []fs.DirEntry
		done  bool
	}
	stack := []level{{f: top, dir: "."}}
	defer func() {
		for _, l := range stack {
			l.f.Close()
		}
	}()
	n := 0
	for len(stack) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		cur := &stack[len(stack)-1]
		if len(cur.batch) == 0 {
			if cur.done {
				cur.f.Close()
				stack = stack[:len(stack)-1]
				continue
			}
			batch, err := cur.f.ReadDir(walkBatch)
			if err != nil && err != io.EOF && !skipErrors {
				return err
			}
			cur.batch, cur.done = batch, err != nil
			continue
		}
		d := cur.batch[0]
		cur.batch = cur.batch[1:]
		if n++; n > maxEntries {
			return errTooManyEntries
		}
		p := path.Join(cur.dir, d.Name())
		err := visit(p, d)
		if !d.IsDir() {
			if err != nil && err != fs.SkipDir {
				return err
			}
			continue
		}
		if err == fs.SkipDir {
			continue
		}
		if err != nil {
			return err
		}
		if len(stack) >= walkMaxDepth {
			return errTooDeep
		}
		f, err := open(p)
		if err != nil {
			if skipErrors {
				continue
			}
			return err
		}
		stack = append(stack, level{f: f, dir: p})
	}
	return nil
}

// copyRegular copies the regular file p of src to dst (mode 0644, or 0755
// if executable) and returns its size. The source is opened without
// blocking and checked once open, and at most max bytes are copied: a file
// that changed after it was listed cannot exceed the caller's budget.
func copyRegular(src, dst *os.Root, p string, mode fs.FileMode, max int64) (int64, error) {
	in, st, err := openRegular(src, p)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	if st.Size() > max {
		return 0, errTooLarge
	}
	m := fs.FileMode(0o644)
	if mode&0o111 != 0 {
		m = 0o755
	}
	out, err := dst.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, m)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(out, io.LimitReader(in, max+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > max {
		err = errTooLarge
	}
	return n, err
}

// readDirCapped lists at most max names of the folder f without loading a
// larger listing; more reports that the folder has further entries.
func readDirCapped(f *os.File, max int) (entries []os.DirEntry, more bool, err error) {
	entries, err = f.ReadDir(max + 1)
	if err == io.EOF {
		err = nil
	}
	if len(entries) > max {
		return entries[:max], true, err
	}
	return entries, false, err
}
