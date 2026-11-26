package api

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// The recordings folder is written by the recorder, which parses untrusted
// camera data, while the API's own mount also holds tokens and keys. A link
// planted there must never lead the API out of the folder.

// openSegment opens a recording (rel: a segment row's path) inside the
// recordings folder; a link that leaves the folder is refused (os.Root), so
// there is no window between a check and the open.
func (s *Server) openSegment(rel string) (*os.File, error) {
	root, err := os.OpenRoot(filepath.Join(s.Cfg.DataDir, "recordings"))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return root.Open(filepath.FromSlash(rel))
}

// linkLeaves reports whether p (inside root) exists and resolves, through
// links, to somewhere outside root, or cannot be resolved. A missing file
// does not leave: callers open it and get the usual "not found".
func linkLeaves(root, p string) bool {
	real, err := filepath.EvalSymlinks(p)
	if errors.Is(err, fs.ErrNotExist) {
		return false
	}
	if err != nil {
		return true
	}
	realRoot, err := filepath.EvalSymlinks(root)
	return err != nil || !strings.HasPrefix(real, realRoot+string(filepath.Separator))
}
