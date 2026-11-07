// Package nas is the minimal web file access to the Cloud share (ADR-0012).
// Every path is opened through os.Root, so nothing outside the share can be
// reached (no "..", no symlink escapes). Deletes move files to the same
// recycle bin Samba uses (.lixeira/<user>/); nothing is removed for good here.
package nas

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"
	"sync"
	"time"
)

// Shared is the folder every user can read and (with a write role) change.
const Shared = "Compartilhado"

// Trash is the recycle bin folder, shared with Samba (deploy/nas/smb.conf).
const Trash = ".lixeira"

const partSuffix = ".sentinela-part"

// Errors safe to show to API clients.
var (
	ErrForbidden = errors.New("not allowed")
	ErrNotFound  = errors.New("not found")
	ErrExists    = errors.New("already exists")
	ErrInvalid   = errors.New("invalid path or name")
	ErrOffset    = errors.New("upload offset does not match")
	ErrTooLarge  = errors.New("file too large")
)

// Share is the Cloud root.
type Share struct {
	root *os.Root
	now  func() time.Time
}

// Open opens the share rooted at dir.
func Open(dir string) (*Share, error) {
	r, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("open nas root: %w", err)
	}
	return &Share{root: r, now: time.Now}, nil
}

// Close releases the root.
func (s *Share) Close() error { return s.root.Close() }

// Access is who is asking. Admins see the whole share; everyone else sees
// their own folder, the shared folder and their own recycle bin.
type Access struct {
	User  string
	Admin bool
	Write bool
}

// Entry is one listed file or folder.
type Entry struct {
	Name     string    `json:"name"`
	Dir      bool      `json:"dir"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
}

// Clean normalises a client path to a slash path relative to the share
// ("" is the share itself). It rejects NUL bytes and backslashes.
func Clean(p string) (string, error) {
	if strings.ContainsAny(p, "\x00\\") {
		return "", ErrInvalid
	}
	return strings.TrimPrefix(path.Clean("/"+p), "/"), nil
}

// ownRoot is the user's personal folder, or "" when the name cannot be one.
func (a Access) ownRoot() string {
	if a.User == "" || strings.HasPrefix(a.User, ".") || a.User == Shared || a.User == "lost+found" {
		return ""
	}
	return a.User
}

// canRead reports whether a (cleaned) path is visible to a.
func (a Access) canRead(p string) bool {
	if p == "" {
		return true
	}
	parts := strings.Split(p, "/")
	if parts[0] == "lost+found" {
		return false
	}
	if slices.ContainsFunc(parts, func(x string) bool { return strings.HasSuffix(x, partSuffix) }) {
		return false
	}
	if a.Admin {
		return true
	}
	switch parts[0] {
	case a.ownRoot(), Shared:
		return parts[0] != ""
	case Trash:
		return len(parts) == 1 || parts[1] == a.ownRoot() && a.ownRoot() != ""
	}
	return false
}

// canChange reports whether a may create, move or delete at p.
func (a Access) canChange(p string) bool {
	if !a.Write || p == "" || !a.canRead(p) {
		return false
	}
	parts := strings.Split(p, "/")
	if parts[0] == Trash {
		return false // the bin is managed by Samba's recycle module and the purge timer
	}
	// Top-level folders belong to the admin; users work inside theirs.
	return a.Admin || len(parts) > 1
}

// protected reports whether p is a top-level folder (a user's folder, the
// shared one, the bin): they are never deleted or renamed through the API,
// even by an admin (round 6, owner). Admins may still create new ones.
func protected(p string) bool { return p != "" && !strings.Contains(p, "/") }

// canRestore reports whether p is inside a's own recycle bin (it may be moved out).
func (a Access) canRestore(p string) bool {
	parts := strings.Split(p, "/")
	return a.Write && a.ownRoot() != "" && len(parts) > 2 && parts[0] == Trash && parts[1] == a.ownRoot()
}

// Ensure creates the user's folder and the shared folder if missing.
func (s *Share) Ensure(a Access) error {
	for _, d := range []string{a.ownRoot(), Shared} {
		if d == "" {
			continue
		}
		if err := s.root.Mkdir(d, 0o775); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
	}
	return nil
}

// List returns the folder's entries, folders first, hiding dot files.
func (s *Share) List(a Access, p string) ([]Entry, error) {
	p, err := Clean(p)
	if err != nil {
		return nil, err
	}
	if !a.canRead(p) {
		return nil, ErrForbidden
	}
	name := p
	if name == "" {
		name = "."
		if err := s.Ensure(a); err != nil {
			return nil, err
		}
	}
	f, err := s.root.Open(name)
	if err != nil {
		return nil, mapErr(err)
	}
	defer f.Close()
	des, err := f.ReadDir(-1)
	if err != nil {
		return nil, mapErr(err)
	}
	out := []Entry{}
	for _, de := range des {
		child := path.Join(p, de.Name())
		if strings.HasPrefix(de.Name(), ".") && !(p == "" && de.Name() == Trash) || !a.canRead(child) {
			continue
		}
		info, err := de.Info()
		if err != nil {
			continue
		}
		dir := info.IsDir()
		if info.Mode()&fs.ModeSymlink != 0 {
			// Follow only links that stay inside the root.
			st, err := s.root.Stat(child)
			if err != nil {
				continue
			}
			info, dir = st, st.IsDir()
		}
		e := Entry{Name: de.Name(), Dir: dir, Modified: info.ModTime().UTC()}
		if !dir {
			e.Size = info.Size()
		}
		out = append(out, e)
	}
	slices.SortFunc(out, func(x, y Entry) int {
		if x.Dir != y.Dir {
			if x.Dir {
				return -1
			}
			return 1
		}
		return strings.Compare(strings.ToLower(x.Name), strings.ToLower(y.Name))
	})
	return out, nil
}

// OpenFile opens a regular file for reading.
func (s *Share) OpenFile(a Access, p string) (*os.File, fs.FileInfo, error) {
	p, err := Clean(p)
	if err != nil {
		return nil, nil, err
	}
	if p == "" || !a.canRead(p) {
		return nil, nil, ErrForbidden
	}
	f, err := s.root.Open(p)
	if err != nil {
		return nil, nil, mapErr(err)
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, nil, ErrNotFound
	}
	return f, info, nil
}

// Mkdir creates one folder.
func (s *Share) Mkdir(a Access, p string) error {
	p, err := Clean(p)
	if err != nil {
		return err
	}
	if !a.canChange(p) || !validName(path.Base(p)) {
		return ErrForbidden
	}
	if err := s.root.Mkdir(p, 0o775); err != nil {
		return mapErr(err)
	}
	s.SetOwner(p, a.User)
	return nil
}

// Move renames a file or folder; the target must not exist. Moving out of
// the user's own recycle bin restores it, recreating missing folders.
func (s *Share) Move(a Access, from, to string) error {
	from, err := Clean(from)
	if err != nil {
		return err
	}
	if to, err = Clean(to); err != nil {
		return err
	}
	restore := a.canRestore(from)
	if !(a.canChange(from) || restore) || protected(from) || !a.canChange(to) || !validName(path.Base(to)) {
		return ErrForbidden
	}
	if from == to || strings.HasPrefix(to+"/", from+"/") {
		return ErrInvalid // into itself
	}
	if _, err := s.root.Lstat(to); err == nil {
		return ErrExists
	}
	if restore {
		if err := s.root.MkdirAll(path.Dir(to), 0o775); err != nil {
			return mapErr(err)
		}
	}
	return mapErr(s.root.Rename(from, to))
}

// Delete moves p into the user's recycle bin, keeping its folder tree, and
// returns where it went. The bin's purge timer removes it after 30 days.
func (s *Share) Delete(a Access, p string) (string, error) {
	p, err := Clean(p)
	if err != nil {
		return "", err
	}
	if !a.canChange(p) || protected(p) {
		return "", ErrForbidden
	}
	bin := a.ownRoot()
	if bin == "" {
		return "", ErrForbidden
	}
	if _, err := s.root.Lstat(p); err != nil {
		return "", mapErr(err)
	}
	dst := path.Join(Trash, bin, p)
	if err := s.root.MkdirAll(path.Dir(dst), 0o775); err != nil {
		return "", mapErr(err)
	}
	dst = s.freeName(dst)
	if err := s.root.Rename(p, dst); err != nil {
		return "", mapErr(err)
	}
	s.touchTree(dst)
	return dst, nil
}

// Purge removes p for good from the user's own recycle bin: one item
// inside .lixeira/<user>/, or the whole bin (emptied, the folder stays).
func (s *Share) Purge(a Access, p string) error {
	p, err := Clean(p)
	if err != nil {
		return err
	}
	own := a.ownRoot()
	bin := path.Join(Trash, own)
	if !a.Write || own == "" || (p != bin && !strings.HasPrefix(p, bin+"/")) {
		return ErrForbidden
	}
	if _, err := s.root.Lstat(p); err != nil {
		return mapErr(err)
	}
	if err := s.root.RemoveAll(p); err != nil {
		return mapErr(err)
	}
	if p == bin {
		return mapErr(s.root.MkdirAll(bin, 0o775))
	}
	return nil
}

// freeName returns p, or "name (2).ext", "name (3).ext"… if p exists.
func (s *Share) freeName(p string) string {
	ext := path.Ext(p)
	base := strings.TrimSuffix(p, ext)
	for i := 2; ; i++ {
		if _, err := s.root.Lstat(p); errors.Is(err, fs.ErrNotExist) {
			return p
		}
		p = fmt.Sprintf("%s (%d)%s", base, i, ext)
	}
}

// touchTree sets the access time of everything under p to now: the purge
// timer (sentinela-trash.service) counts 30 days from the delete, as with
// Samba's recycle:touch.
func (s *Share) touchTree(p string) {
	now := s.now()
	_ = fs.WalkDir(s.root.FS(), p, func(q string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if info, err := d.Info(); err == nil {
			_ = s.root.Chtimes(q, now, info.ModTime())
		}
		return nil
	})
}

// PartSize is how much of an interrupted upload to p is already on disk.
func (s *Share) PartSize(a Access, p string) (int64, error) {
	part, err := s.partPath(a, p)
	if err != nil {
		return 0, err
	}
	st, err := s.root.Stat(part)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, mapErr(err)
	}
	return st.Size(), nil
}

// WriteChunk appends body at offset to the upload of p (total bytes). The
// data goes straight to a hidden partial file next to the target, never to
// memory or /tmp. When the last byte arrives the partial file is renamed to
// p. It returns the bytes now on disk and whether the file is complete.
func (s *Share) WriteChunk(a Access, p string, offset, total int64, body io.Reader) (int64, bool, error) {
	if total < 0 {
		return 0, false, ErrInvalid
	}
	part, err := s.partPath(a, p)
	if err != nil {
		return 0, false, err
	}
	final, _ := Clean(p)
	if _, err := s.root.Lstat(final); err == nil {
		return 0, false, ErrExists
	}
	have, err := s.PartSize(a, p)
	if err != nil {
		return 0, false, err
	}
	if offset != have || offset > total {
		return have, false, ErrOffset
	}
	f, err := s.root.OpenFile(part, os.O_WRONLY|os.O_CREATE, 0o664)
	if err != nil {
		return have, false, mapErr(err)
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		f.Close()
		return have, false, err
	}
	// One byte more than allowed tells us the client sent too much.
	n, err := copyLarge(f, io.LimitReader(body, total-offset+1))
	cerr := f.Close()
	have = offset + n
	if n > total-offset {
		if t, err := s.root.OpenFile(part, os.O_WRONLY, 0); err == nil {
			_ = t.Truncate(total)
			t.Close()
		}
		return total, false, ErrTooLarge
	}
	if err != nil {
		return have, false, err
	}
	if cerr != nil {
		return have, false, cerr
	}
	if have < total {
		return have, false, nil
	}
	if err := s.root.Rename(part, final); err != nil {
		return have, false, mapErr(err)
	}
	s.SetOwner(final, a.User)
	return have, true, nil
}

// uploadBufs hold 1 MiB copy buffers: a network body would otherwise reach
// the file through os.File.ReadFrom's generic 32 KiB copy, 32× the writes.
var uploadBufs = sync.Pool{New: func() any { b := make([]byte, 1<<20); return &b }}

func copyLarge(dst io.Writer, src io.Reader) (int64, error) {
	b := uploadBufs.Get().(*[]byte)
	defer uploadBufs.Put(b)
	return io.CopyBuffer(struct{ io.Writer }{dst}, src, *b) // hide ReadFrom so the buffer is used
}

// MaxText bounds a text file edited in the browser.
const MaxText = 2 << 20

// ReplaceText overwrites an existing file with data (the Cloud's text
// editor). The new content goes to a partial file first and is renamed
// over the old one, so a failed save never leaves half a file.
func (s *Share) ReplaceText(a Access, p string, data []byte) error {
	if len(data) > MaxText {
		return ErrTooLarge
	}
	part, err := s.partPath(a, p)
	if err != nil {
		return err
	}
	final, _ := Clean(p)
	st, err := s.root.Lstat(final)
	if err != nil {
		return mapErr(err)
	}
	if !st.Mode().IsRegular() {
		return ErrInvalid
	}
	f, err := s.root.OpenFile(part, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o664)
	if err != nil {
		return mapErr(err)
	}
	_, werr := f.Write(data)
	if u := s.Owner(final); u != "" { // the new content keeps its owner
		setOwner(f, u)
	}
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = s.root.Remove(part)
		return werr
	}
	if err := s.root.Rename(part, final); err != nil {
		_ = s.root.Remove(part)
		return mapErr(err)
	}
	return nil
}

func (s *Share) partPath(a Access, p string) (string, error) {
	p, err := Clean(p)
	if err != nil {
		return "", err
	}
	if !a.canChange(p) || !validName(path.Base(p)) {
		return "", ErrForbidden
	}
	dir, name := path.Split(p)
	if st, err := s.root.Stat(strings.TrimSuffix(dir, "/")); err != nil || !st.IsDir() {
		return "", ErrNotFound
	}
	return dir + "." + name + partSuffix, nil
}

// validName rejects names Windows or Samba would choke on and hidden names.
func validName(n string) bool {
	if n == "" || n == "." || n == ".." || len(n) > 255 || strings.HasPrefix(n, ".") || strings.HasSuffix(n, partSuffix) {
		return false
	}
	if strings.TrimRight(n, " .") != n {
		return false
	}
	return !strings.ContainsAny(n, `/\:*?"<>|`) && !strings.ContainsFunc(n, func(r rune) bool { return r < 32 })
}

func mapErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, fs.ErrNotExist):
		return ErrNotFound
	case errors.Is(err, fs.ErrExist):
		return ErrExists
	case errors.Is(err, fs.ErrPermission):
		return ErrForbidden
	case strings.Contains(err.Error(), "path escapes"):
		return ErrForbidden
	}
	return err
}
