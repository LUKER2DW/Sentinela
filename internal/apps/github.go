package apps

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// GitHub fetches repository snapshots: the commit a ref points to, and that
// commit as a tarball. Only the GitHub API and its codeload redirect are
// ever contacted (no git binary, no arbitrary URLs).
type GitHub struct {
	API   string // https://api.github.com
	Token func() string
	HC    *http.Client
	// AllowedRedirects are the hosts a tarball may be redirected to.
	AllowedRedirects []string
}

// NewGitHub returns a client for github.com.
func NewGitHub(token func() string) *GitHub {
	g := &GitHub{API: "https://api.github.com", Token: token, AllowedRedirects: []string{"codeload.github.com"}}
	g.HC = &http.Client{Timeout: 10 * time.Minute, CheckRedirect: g.checkRedirect}
	return g
}

func (g *GitHub) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) > 3 {
		return errors.New("github: too many redirects")
	}
	for _, h := range g.AllowedRedirects {
		if req.URL.Hostname() == h {
			return nil
		}
	}
	return fmt.Errorf("github: refusing redirect to %s", req.URL.Hostname())
}

func (g *GitHub) get(ctx context.Context, p, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.API+p, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "sentinela-apps")
	if t := g.Token(); t != "" {
		// Go drops this header on the redirect to codeload (another host).
		req.Header.Set("Authorization", "Bearer "+t)
	}
	resp, err := g.HC.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		var e struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 8<<10)).Decode(&e)
		switch resp.StatusCode {
		case http.StatusNotFound:
			return nil, fmt.Errorf("github: repository or branch not found (private? set a token): %s", e.Message)
		case http.StatusUnauthorized, http.StatusForbidden:
			return nil, fmt.Errorf("github: access refused (token or rate limit): %s", e.Message)
		}
		return nil, fmt.Errorf("github: HTTP %d: %s", resp.StatusCode, e.Message)
	}
	return resp, nil
}

var shaPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// Commit resolves ref to a commit id and its first message line.
func (g *GitHub) Commit(ctx context.Context, repo, ref string) (sha, message string, err error) {
	resp, err := g.get(ctx, "/repos/"+repo+"/commits/"+url.PathEscape(ref), "application/vnd.github.sha")
	if err != nil {
		return "", "", err
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 128))
	resp.Body.Close()
	sha = strings.TrimSpace(string(b))
	if err != nil || !shaPattern.MatchString(sha) {
		return "", "", errors.New("github: unexpected commit id")
	}
	// The message is for people; failing to read it is not an error.
	if resp, err := g.get(ctx, "/repos/"+repo+"/commits?per_page=1&sha="+sha, "application/vnd.github+json"); err == nil {
		var list []struct {
			Commit struct {
				Message string `json:"message"`
			} `json:"commit"`
		}
		if json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&list) == nil && len(list) > 0 {
			message, _, _ = strings.Cut(list[0].Commit.Message, "\n")
			if len(message) > 200 {
				message = message[:200]
			}
		}
		resp.Body.Close()
	}
	return sha, message, nil
}

// Extraction limits for one snapshot.
const (
	maxDownload = 300 << 20
	maxExtract  = 1 << 30
	maxEntries  = 100_000
)

// Fetch downloads the commit's tarball into dir (which must not exist),
// without the archive's top folder.
func (g *GitHub) Fetch(ctx context.Context, repo, sha, dir string) error {
	resp, err := g.get(ctx, "/repos/"+repo+"/tarball/"+sha, "application/vnd.github+json")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return Extract(io.LimitReader(resp.Body, maxDownload), dir)
}

// Extract unpacks a gzip tarball into dir, dropping the first path element.
// Every write goes through os.Root: an entry (or a symlink planted by an
// earlier entry) cannot reach outside dir. Hard links and devices are
// skipped; file modes keep only the executable bit.
func Extract(r io.Reader, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("source archive: %w", err)
	}
	tr := tar.NewReader(gz)
	var total int64
	for n := 0; ; n++ {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("source archive: %w", err)
		}
		if n > maxEntries {
			return errors.New("source archive: too many files")
		}
		name, ok := stripTop(h.Name)
		if !ok {
			continue
		}
		switch h.Typeflag {
		case tar.TypeDir:
			err = root.MkdirAll(name, 0o755)
		case tar.TypeReg:
			total += h.Size
			if total > maxExtract {
				return errors.New("source archive: larger than 1 GB")
			}
			err = writeEntry(root, name, h, tr)
		case tar.TypeSymlink:
			if err = root.MkdirAll(path.Dir(name), 0o755); err == nil {
				err = root.Symlink(h.Linkname, name)
			}
		}
		if err != nil && !errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("source archive: %s: %w", name, err)
		}
	}
}

// stripTop drops the "owner-repo-sha/" folder and rejects unsafe names.
func stripTop(name string) (string, bool) {
	name = strings.TrimPrefix(path.Clean("/"+name), "/")
	_, rest, ok := strings.Cut(name, "/")
	if !ok || rest == "" || strings.HasPrefix(rest, "../") || rest == ".." || strings.ContainsRune(rest, 0) {
		return "", false
	}
	return rest, true
}

func writeEntry(root *os.Root, name string, h *tar.Header, r io.Reader) error {
	if err := root.MkdirAll(path.Dir(name), 0o755); err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if h.Mode&0o111 != 0 {
		mode = 0o755
	}
	f, err := root.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	_, err = io.CopyN(f, r, h.Size)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// TarDir writes dir as an uncompressed tar (a Docker build context).
// Symlinks are kept as links; nothing outside dir is read.
func TarDir(dir string, w io.Writer) error {
	tw := tar.NewWriter(w)
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil || rel == "." {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		link := ""
		if info.Mode()&fs.ModeSymlink != 0 {
			if link, err = os.Readlink(p); err != nil {
				return err
			}
		} else if !info.Mode().IsRegular() && !info.IsDir() {
			return nil
		}
		h, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		h.Name = filepath.ToSlash(rel)
		h.Uid, h.Gid, h.Uname, h.Gname = 0, 0, "", ""
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			f, err := os.Open(p)
			if err != nil {
				return err
			}
			_, err = io.Copy(tw, f)
			f.Close()
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	return tw.Close()
}
