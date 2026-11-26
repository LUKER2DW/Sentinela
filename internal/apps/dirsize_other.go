//go:build !linux

package apps

import (
	"errors"
	"os"
	"path/filepath"
)

// walkDir is an open folder of the disk walk. The agent runs on Linux
// (dirsize_linux.go); this version, by path, serves development builds.
type walkDir struct {
	f    *os.File
	path string
}

func openWalkDir(path string) (*walkDir, error) {
	st, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, errors.New("not a folder")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	return &walkDir{f: f, path: path}, nil
}

func (d *walkDir) openDir(name string) (*walkDir, error) {
	return openWalkDir(filepath.Join(d.path, name))
}

func (d *walkDir) stat(name string) (isDir bool, size int64, ok bool) {
	st, err := os.Lstat(filepath.Join(d.path, name))
	switch {
	case err != nil:
		return false, 0, false
	case st.IsDir():
		return true, 0, true
	case st.Mode().IsRegular():
		return false, st.Size(), true
	}
	return false, 0, false
}

func (d *walkDir) close() { _ = d.f.Close() }
