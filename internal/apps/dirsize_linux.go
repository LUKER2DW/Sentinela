package apps

import (
	"os"

	"golang.org/x/sys/unix"
)

// walkDir is an open folder of the disk walk; children are reached through
// its descriptor (openat/fstatat), never by a path.
type walkDir struct {
	f  *os.File
	fd int
}

// walkOpenFlags open only a folder, never through a link, never blocking.
const walkOpenFlags = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC

func openWalkDir(path string) (*walkDir, error) {
	fd, err := unix.Open(path, walkOpenFlags, 0)
	if err != nil {
		return nil, err
	}
	return &walkDir{f: os.NewFile(uintptr(fd), path), fd: fd}, nil
}

func (d *walkDir) openDir(name string) (*walkDir, error) {
	fd, err := unix.Openat(d.fd, name, walkOpenFlags, 0)
	if err != nil {
		return nil, err
	}
	return &walkDir{f: os.NewFile(uintptr(fd), name), fd: fd}, nil
}

// stat reports a child folder, or a regular file's size (ok is false for
// links, FIFOs, devices and entries gone meanwhile).
func (d *walkDir) stat(name string) (isDir bool, size int64, ok bool) {
	var st unix.Stat_t
	if unix.Fstatat(d.fd, name, &st, unix.AT_SYMLINK_NOFOLLOW) != nil {
		return false, 0, false
	}
	switch st.Mode & unix.S_IFMT {
	case unix.S_IFDIR:
		return true, 0, true
	case unix.S_IFREG:
		return false, st.Size, true
	}
	return false, 0, false
}

func (d *walkDir) close() { _ = d.f.Close() }
