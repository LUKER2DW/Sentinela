package nas

import "path"

// Who put a file or folder in the Nuvem is stored on the item itself (an
// extended attribute, owner_linux.go), so it follows renames and moves
// (including through Samba) and does not depend on the database.

// Owner returns who put p in the share, or "" when it is not known (made
// over Samba, or before owners were recorded).
func (s *Share) Owner(p string) string {
	p, err := Clean(p)
	if err != nil || p == "" {
		return ""
	}
	f, err := s.root.Open(p)
	if err != nil {
		return ""
	}
	defer f.Close()
	return getOwner(f)
}

// Owners returns the known owners of names inside dir.
func (s *Share) Owners(dir string, names []string) map[string]string {
	out := map[string]string{}
	for _, n := range names {
		if u := s.Owner(path.Join(dir, n)); u != "" {
			out[n] = u
		}
	}
	return out
}

// SetOwner records user as whoever put p in the share (best effort: a
// filesystem without extended attributes just keeps no owner).
func (s *Share) SetOwner(p, user string) {
	p, err := Clean(p)
	if err != nil || p == "" || user == "" {
		return
	}
	f, err := s.root.Open(p)
	if err != nil {
		return
	}
	defer f.Close()
	setOwner(f, user)
}
