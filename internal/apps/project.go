package apps

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

var uploadPattern = regexp.MustCompile(`^[0-9a-f]{24}$`)

// projectSource snapshots an owned project; its working files, not its old
// git HEAD, define the version after edits through the dashboard.
type projectSource struct{ Dir string }

func (s projectSource) Resolve(ctx context.Context, _ *App) (Version, error) {
	return (LocalSource{Root: s.Dir, Working: true}).Resolve(ctx, &App{Path: "."})
}

func (s projectSource) Snapshot(ctx context.Context, _ *App, v Version, dir string) error {
	return (LocalSource{Root: s.Dir}).Snapshot(ctx, &App{Path: "."}, v, dir)
}

// importProject transfers local/ZIP sources to the service's protected
// system-disk directory. No API process sees or changes the source files.
func (a *Agent) importProject(ctx context.Context, app *App, logf func(string, ...any)) error {
	if app.Managed || app.Source == SourceGitHub {
		return nil
	}
	a.fileMu.Lock()
	defer a.fileMu.Unlock()
	project := filepath.Join(a.appDir(app.ID), "project")
	if _, err := os.Stat(project); err == nil {
		return errors.New("a protected project already exists for this service")
	}
	var rootDir, rel string
	if app.Source == SourceZip {
		rootDir, rel = filepath.Join(a.Cfg.StateDir, "uploads"), app.Path
	} else {
		rootDir, rel = a.Cfg.LocalRoot, app.Path
	}
	base, err := os.OpenRoot(rootDir)
	if err != nil {
		return fmt.Errorf("open the project source: %w", err)
	}
	defer base.Close()
	// Reject links even if they point inside local_root: transferring a link
	// must never delete the project it points to or another Cloud folder.
	parts := strings.Split(rel, "/")
	for i := range parts {
		st, err := base.Lstat(strings.Join(parts[:i+1], "/"))
		if err != nil || st.Mode()&fs.ModeSymlink != 0 {
			return errors.New("the source must be a real folder inside the local root")
		}
	}
	src, err := base.OpenRoot(rel)
	if err != nil {
		return fmt.Errorf("open the project folder: %w", err)
	}
	partial := project + ".partial"
	defer os.RemoveAll(partial)
	if err := copyProject(ctx, src, partial); err != nil {
		src.Close()
		return err
	}
	src.Close()
	if app.Source == SourceZip {
		v, err := a.readVault(a.uploadVaultFile(app.Path), "upload:"+app.Path)
		if err != nil {
			return err
		}
		a.mu.Lock()
		next := mergeVault(a.vaults[app.ID], v.Vars, v.Files, false)
		if len(next.Vars) > maxEnv {
			a.mu.Unlock()
			return errors.New("limite de 64 secrets atingido")
		}
		err = a.storeVaultLocked(app.ID, next)
		a.mu.Unlock()
		if err != nil {
			return err
		}
	}
	if err := a.absorbEnv(ctx, app.ID, partial); err != nil {
		return err
	}
	if err := os.Rename(partial, project); err != nil {
		return fmt.Errorf("store the protected project: %w", err)
	}
	a.mu.Lock()
	live := a.find(app.ID)
	if live == nil {
		a.mu.Unlock()
		return errNoApp
	}
	live.Managed = true
	if err := a.saveLocked(); err != nil {
		live.Managed = false
		a.mu.Unlock()
		_ = os.RemoveAll(project)
		return err
	}
	a.mu.Unlock()
	if err := base.RemoveAll(rel); err != nil {
		logf("Warning: protected project saved, but the source folder could not be removed; remove it from Cloud")
	} else {
		logf("Project moved to the protected Services area")
		if app.Source == SourceZip {
			_ = os.Remove(a.uploadVaultFile(app.Path))
		}
	}
	return nil
}

// copyProject preserves the selected folder (including .git and data) before
// removing its original. All regular-file writes are private and bounded.
func copyProject(ctx context.Context, src *os.Root, dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	dst, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer dst.Close()
	var total int64
	count := 0
	return fs.WalkDir(src.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if p == "." {
			return nil
		}
		count++
		if count > maxEntries {
			return errors.New("the project has too many files")
		}
		if d.IsDir() {
			return dst.MkdirAll(p, 0o700)
		}
		if d.Type()&fs.ModeSymlink != 0 {
			target, err := src.Readlink(p)
			joined := path.Clean(path.Join(path.Dir(p), strings.ReplaceAll(target, "\\", "/")))
			if err != nil || filepath.IsAbs(target) || strings.Contains(target, ":") || joined == ".." || strings.HasPrefix(joined, "../") {
				return errors.New("the project contains a link outside its folder")
			}
			return dst.Symlink(target, p)
		}
		if !d.Type().IsRegular() {
			return errors.New("the project contains a special file")
		}
		st, err := d.Info()
		if err != nil {
			return err
		}
		total += st.Size()
		if total > maxExtract {
			return errors.New("the project is larger than 1 GB")
		}
		if err := copyFile(src, dst, p, st.Mode()); err != nil {
			return err
		}
		return dst.Chmod(p, 0o600|st.Mode()&0o111)
	})
}

// zipPath rejects paths rather than cleaning away traversal attempts.
func zipPath(name string) (string, error) {
	name = strings.ReplaceAll(name, "\\", "/")
	if strings.HasPrefix(name, "/") || strings.ContainsAny(name, ":\x00") {
		return "", errors.New("ZIP contains an invalid path")
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return "", errors.New("ZIP contains a path outside the project")
		}
	}
	return strings.TrimSuffix(path.Clean(name), "/"), nil
}

func skipZipPath(name string) bool {
	for _, part := range strings.Split(name, "/") {
		if skipDir(part) || part == "__MACOSX" || part == ".DS_Store" {
			return true
		}
	}
	return false
}

func extractZip(ctx context.Context, file io.ReaderAt, size int64, dir string) error {
	z, err := zip.NewReader(file, size)
	if err != nil {
		return errors.New("invalid or corrupt ZIP file")
	}
	if len(z.File) > maxEntries {
		return errors.New("ZIP contains too many files")
	}
	// GitHub archives and desktop folder ZIPs usually have one wrapper.
	top := ""
	wrapped := true
	for _, f := range z.File {
		n, err := zipPath(f.Name)
		if err != nil {
			return err
		}
		if skipZipPath(n) || n == "." {
			continue
		}
		first, _, slash := strings.Cut(n, "/")
		if !slash && !f.FileInfo().IsDir() {
			wrapped = false
		}
		if top == "" {
			top = first
		} else if top != first {
			wrapped = false
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	var total int64
	seen := map[string]bool{}
	files := 0
	for _, f := range z.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, _ := zipPath(f.Name)
		if skipZipPath(n) || n == "." {
			continue
		}
		if wrapped {
			_, n, _ = strings.Cut(n, "/")
		}
		if n == "" {
			continue
		}
		if f.FileInfo().IsDir() {
			if err := root.MkdirAll(n, 0o700); err != nil {
				return err
			}
			continue
		}
		if !f.Mode().IsRegular() || seen[n] || f.UncompressedSize64 > uint64(maxExtract-total) {
			return errors.New("ZIP contains links, duplicate or special files, or is over 1 GB")
		}
		seen[n] = true
		if err := root.MkdirAll(path.Dir(n), 0o700); err != nil {
			return err
		}
		in, err := f.Open()
		if err != nil {
			return errors.New("could not open a file in the ZIP")
		}
		out, err := root.OpenFile(n, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600|f.Mode()&0o111)
		if err != nil {
			in.Close()
			return err
		}
		nbytes, copyErr := io.Copy(out, io.LimitReader(&contextReader{ctx: ctx, r: in}, maxExtract-total+1))
		in.Close()
		closeErr := out.Close()
		total += nbytes
		if copyErr != nil || closeErr != nil || total > maxExtract {
			return errors.New("ZIP corrompido, cancelado ou maior que 1 GB")
		}
		files++
	}
	if files == 0 {
		return errors.New("the ZIP has no project files")
	}
	return nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

func (a *Agent) hUpload(w http.ResponseWriter, r *http.Request) {
	a.fileMu.Lock()
	defer a.fileMu.Unlock()
	dir := filepath.Join(a.Cfg.StateDir, "uploads")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		reply(w, nil, err)
		return
	}
	entries, _ := os.ReadDir(dir)
	count := 0
	for _, e := range entries {
		if !e.IsDir() || !uploadPattern.MatchString(e.Name()) {
			continue
		}
		a.mu.Lock()
		used := slices.ContainsFunc(a.apps, func(app *App) bool { return app.Source == SourceZip && !app.Managed && app.Path == e.Name() })
		a.mu.Unlock()
		st, err := e.Info()
		if !used && err == nil && time.Since(st.ModTime()) > 24*time.Hour {
			_ = os.RemoveAll(filepath.Join(dir, e.Name()))
			_ = os.Remove(a.uploadVaultFile(e.Name()))
		} else {
			count++
		}
	}
	if count >= 5 {
		reply(w, nil, errors.New("there are 5 ZIP projects waiting to be imported; create the services or remove the pending uploads"))
		return
	}
	id := randHex() + randHex()
	tmp, err := os.CreateTemp(dir, ".upload-")
	if err != nil {
		reply(w, nil, err)
		return
	}
	defer func() { tmp.Close(); _ = os.Remove(tmp.Name()) }()
	n, err := io.Copy(tmp, http.MaxBytesReader(w, r.Body, maxDownload))
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "envio incompleto ou ZIP maior que 300 MB"})
		return
	}
	project := filepath.Join(dir, id)
	if err := extractZip(r.Context(), tmp, n, project); err != nil {
		_ = os.RemoveAll(project)
		reply(w, nil, err)
		return
	}
	v, err := collectEnv(r.Context(), project)
	if err == nil {
		err = a.writeVault(a.uploadVaultFile(id), "upload:"+id, v)
	}
	if err == nil {
		err = removeEnvFiles(project, v.Files)
	}
	if err != nil {
		_ = os.RemoveAll(project)
		_ = os.Remove(a.uploadVaultFile(id))
		reply(w, nil, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"path": id, "bytes": n, "secrets": len(v.Vars)})
}

func (a *Agent) hDropUpload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("upload")
	if !uploadPattern.MatchString(id) {
		reply(w, nil, errors.New("invalid upload"))
		return
	}
	a.fileMu.Lock()
	defer a.fileMu.Unlock()
	a.mu.Lock()
	used := false
	for _, app := range a.apps {
		used = used || app.Source == SourceZip && !app.Managed && app.Path == id
	}
	a.mu.Unlock()
	if used {
		reply(w, nil, errBusy)
		return
	}
	err := os.RemoveAll(filepath.Join(a.Cfg.StateDir, "uploads", id))
	if err == nil {
		if e := os.Remove(a.uploadVaultFile(id)); e != nil && !errors.Is(e, os.ErrNotExist) {
			err = e
		}
	}
	reply(w, map[string]bool{"ok": true}, err)
}
