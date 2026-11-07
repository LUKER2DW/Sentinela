package apps

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"
	"time"
)

// Version identifies one snapshot of a source.
type Version struct {
	Commit  string // git commit; "" for a folder without git
	Message string
	Branch  string
	// Label names the snapshot in release folders and the UI.
	Label string
}

// Source resolves and copies snapshots of a service's code.
type Source interface {
	Resolve(ctx context.Context, app *App) (Version, error)
	Snapshot(ctx context.Context, app *App, v Version, dir string) error
}

// GitHubSource adapts GitHub to Source.
type GitHubSource struct{ GH *GitHub }

// Resolve finds the commit the branch points to.
func (s GitHubSource) Resolve(ctx context.Context, app *App) (Version, error) {
	sha, msg, err := s.GH.Commit(ctx, app.Repo, app.Ref)
	if err != nil {
		return Version{}, err
	}
	return Version{Commit: sha, Message: msg, Branch: app.Ref, Label: sha[:12]}, nil
}

// Snapshot downloads the commit.
func (s GitHubSource) Snapshot(ctx context.Context, app *App, v Version, dir string) error {
	return s.GH.Fetch(ctx, app.Repo, v.Commit, dir)
}

// LocalSource reads project folders under Root (the Cloud by default): a
// project copied there with its .git deploys like a repository, at the
// commit its working copy is on. Everything is read through os.Root.
type LocalSource struct {
	Root    string
	Working bool
}

func (s LocalSource) open(rel string) (*os.Root, error) {
	if s.Root == "" {
		return nil, errors.New("local folders are not enabled (apps.local_root)")
	}
	base, err := os.OpenRoot(s.Root)
	if err != nil {
		return nil, fmt.Errorf("local root: %w", err)
	}
	defer base.Close()
	r, err := base.OpenRoot(rel)
	if err != nil {
		return nil, fmt.Errorf("pasta %q: %w", rel, errors.Unwrap(err))
	}
	return r, nil
}

// Resolve reads the branch and commit from the folder's .git; without git
// the snapshot is named by its newest modification time.
func (s LocalSource) Resolve(_ context.Context, app *App) (Version, error) {
	r, err := s.open(app.Path)
	if err != nil {
		return Version{}, err
	}
	defer r.Close()
	if v, ok := gitHead(r); ok && !s.Working {
		return v, nil
	}
	newest := time.Time{}
	_ = fs.WalkDir(r.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && skipDir(d.Name()) {
			return fs.SkipDir
		}
		if info, err := d.Info(); err == nil && info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		return nil
	})
	if newest.IsZero() {
		return Version{}, errors.New("the folder is empty")
	}
	format := "20060102-150405"
	if s.Working {
		format += ".000000000"
	}
	return Version{Label: "local-" + newest.UTC().Format(format)}, nil
}

// skipDir: folders never copied from a local project (VCS internals, and
// dependency folders built for another OS: setup installs them again).
func skipDir(name string) bool {
	return name == ".git" || name == "node_modules" || name == ".venv" || name == "__pycache__"
}

// Snapshot copies the folder into dir (symlinks as links, nothing outside).
func (s LocalSource) Snapshot(ctx context.Context, app *App, _ Version, dir string) error {
	src, err := s.open(app.Path)
	if err != nil {
		return err
	}
	defer src.Close()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	dst, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer dst.Close()
	var total int64
	n := 0
	return fs.WalkDir(src.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if p == "." {
			return nil
		}
		if n++; n > maxEntries {
			return errors.New("a pasta tem arquivos demais")
		}
		switch {
		case d.IsDir():
			if skipDir(d.Name()) {
				return fs.SkipDir
			}
			return dst.MkdirAll(p, 0o755)
		case d.Type()&fs.ModeSymlink != 0:
			target, err := src.Readlink(p)
			if err != nil {
				return err
			}
			return dst.Symlink(target, p)
		case d.Type().IsRegular():
			info, err := d.Info()
			if err != nil {
				return err
			}
			if total += info.Size(); total > maxExtract {
				return errors.New("a pasta tem mais de 1 GB")
			}
			return copyFile(src, dst, p, info.Mode())
		}
		return nil
	})
}

func copyFile(src, dst *os.Root, p string, mode fs.FileMode) error {
	in, err := src.Open(p)
	if err != nil {
		return err
	}
	defer in.Close()
	m := fs.FileMode(0o644)
	if mode&0o111 != 0 {
		m = 0o755
	}
	out, err := dst.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, m)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return err
}

// gitHead reads HEAD of the working copy in r without running git.
func gitHead(r *os.Root) (Version, bool) {
	head, err := r.ReadFile(".git/HEAD")
	if err != nil {
		return Version{}, false
	}
	h := strings.TrimSpace(string(head))
	var v Version
	if ref, ok := strings.CutPrefix(h, "ref: "); ok {
		v.Branch = strings.TrimPrefix(ref, "refs/heads/")
		if b, err := r.ReadFile(".git/" + ref); err == nil {
			v.Commit = strings.TrimSpace(string(b))
		} else {
			v.Commit = packedRef(r, ref)
		}
	} else {
		v.Commit = h // detached
	}
	if !shaPattern.MatchString(v.Commit) {
		return Version{}, false
	}
	v.Message = looseMessage(r, v.Commit)
	v.Label = v.Commit[:12]
	return v, true
}

func packedRef(r *os.Root, ref string) string {
	b, err := r.ReadFile(".git/packed-refs")
	if err != nil {
		return ""
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		if sha, name, ok := strings.Cut(sc.Text(), " "); ok && name == ref {
			return sha
		}
	}
	return ""
}

// looseMessage is the first line of a commit stored as a loose object
// ("" when it is packed: the message is only for people).
func looseMessage(r *os.Root, sha string) string {
	f, err := r.Open(".git/objects/" + sha[:2] + "/" + sha[2:])
	if err != nil {
		return ""
	}
	defer f.Close()
	z, err := zlib.NewReader(f)
	if err != nil {
		return ""
	}
	defer z.Close()
	b, _ := io.ReadAll(io.LimitReader(z, 64<<10))
	_, body, ok := bytes.Cut(b, []byte{0})
	if !ok || !bytes.HasPrefix(b, []byte("commit ")) {
		return ""
	}
	_, msg, _ := bytes.Cut(body, []byte("\n\n"))
	line, _, _ := strings.Cut(string(msg), "\n")
	if len(line) > 200 {
		line = line[:200]
	}
	return line
}

// Folder is one entry of the local folder picker.
type Folder struct {
	Name    string   `json:"name"`
	Path    string   `json:"path"`
	Git     bool     `json:"git"`
	Markers []string `json:"markers,omitempty"` // compose, dockerfile, node, python, go…
}

// Browse lists the subfolders of rel (relative to Root), marking those that
// look like projects.
func (s LocalSource) Browse(rel string) ([]Folder, error) {
	rel = cleanRel(rel)
	if rel != "" && !validRel(rel) {
		return nil, errors.New("invalid folder")
	}
	if s.Root == "" {
		return nil, errors.New("local folders are not enabled (apps.local_root)")
	}
	base, err := os.OpenRoot(s.Root)
	if err != nil {
		return nil, err
	}
	defer base.Close()
	name := rel
	if name == "" {
		name = "."
	}
	entries, err := fs.ReadDir(base.FS(), name)
	if err != nil {
		return nil, fmt.Errorf("folder %q not found", rel)
	}
	out := []Folder{}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || e.Name() == "lost+found" {
			continue
		}
		p := path.Join(rel, e.Name())
		f := Folder{Name: e.Name(), Path: p}
		if sub, err := fs.ReadDir(base.FS(), p); err == nil {
			names := make([]string, 0, len(sub))
			for _, x := range sub {
				names = append(names, x.Name())
			}
			f.Git = slices.Contains(names, ".git")
			f.Markers = markers(names)
		}
		out = append(out, f)
	}
	slices.SortFunc(out, func(a, b Folder) int { return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	return out, nil
}
