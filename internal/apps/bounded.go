package apps

import (
	"errors"
	"io"
	"os"
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
