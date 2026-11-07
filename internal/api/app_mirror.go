package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Android app updates from the project's GitHub releases (ADR-0022). Every
// few hours the server reads the latest release of app_updates.github and,
// when its app.json is newer than the APK it offers, downloads the APK,
// checks its size and SHA-256 against app.json, and offers it through
// /api/app/latest like a hand-published one. Phones keep updating from
// their own house; Android itself refuses an APK signed with another key.

const (
	mirrorFirst    = 2 * time.Minute
	mirrorEvery    = 6 * time.Hour
	maxAPKBytes    = 200 << 20
	maxAppJSON     = 64 << 10
	maxReleaseJSON = 1 << 20
)

// githubAPI is the API root; tests point it at a fake server.
var githubAPI = "https://api.github.com"

type ghRelease struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
		Size int64  `json:"size"`
	} `json:"assets"`
}

// MirrorAppReleases runs until ctx ends; off when app_updates.github is "none".
func (s *Server) MirrorAppReleases(ctx context.Context) {
	repo := s.Cfg.AppUpdates.GitHub
	if repo == "" || repo == "none" {
		return
	}
	wait := mirrorFirst
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = mirrorEvery
		if rel, err := s.mirrorAppOnce(ctx, repo); err != nil {
			s.Log.Warn("app update check failed", "repo", repo, "err", err)
		} else if rel != nil {
			s.Log.Info("new Android app offered", "version", rel.VersionName, "code", rel.VersionCode)
		}
	}
}

func (s *Server) mirrorClient() *http.Client {
	return &http.Client{
		Timeout: 10 * time.Minute,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if via[0].URL.Scheme == "https" && req.URL.Scheme != "https" {
				return errors.New("redirect to a non-HTTPS URL")
			}
			if len(via) > 5 {
				return errors.New("too many redirects")
			}
			return nil
		},
	}
}

func (s *Server) mirrorGet(ctx context.Context, url string, limit int64) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Sentinela/"+Version)
	req.Header.Set("Accept", "application/vnd.github+json, application/octet-stream")
	resp, err := s.mirrorClient().Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return struct {
		io.Reader
		io.Closer
	}{io.LimitReader(resp.Body, limit), resp.Body}, nil
}

// mirrorAppOnce returns the newly offered release, or nil when there was
// nothing newer.
func (s *Server) mirrorAppOnce(ctx context.Context, repo string) (*AppRelease, error) {
	body, err := s.mirrorGet(ctx, githubAPI+"/repos/"+repo+"/releases/latest", maxReleaseJSON)
	if err != nil {
		return nil, err
	}
	var gh ghRelease
	err = json.NewDecoder(body).Decode(&gh)
	body.Close()
	if err != nil {
		return nil, fmt.Errorf("release: %w", err)
	}
	var apkURL, jsonURL string
	for _, a := range gh.Assets {
		switch a.Name {
		case "sentinela.apk":
			apkURL = a.URL
		case "app.json":
			jsonURL = a.URL
		}
	}
	if apkURL == "" || jsonURL == "" {
		return nil, nil // a release without the app
	}
	body, err = s.mirrorGet(ctx, jsonURL, maxAppJSON)
	if err != nil {
		return nil, err
	}
	var rel AppRelease
	err = json.NewDecoder(body).Decode(&rel)
	body.Close()
	if err != nil || rel.VersionCode <= 0 || !hexSHA256.MatchString(rel.SHA256) || rel.Size <= 0 || rel.Size > maxAPKBytes {
		return nil, fmt.Errorf("app.json of %s is invalid", gh.TagName)
	}
	if cur := s.appRelease(); cur != nil && cur.VersionCode >= rel.VersionCode {
		return nil, nil
	}
	if rel.Notes == "" {
		rel.Notes = "Sentinela " + gh.TagName
	}

	dir := s.appDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	body, err = s.mirrorGet(ctx, apkURL, rel.Size+1)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	tmp, err := os.CreateTemp(dir, ".sentinela-*.apk")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), body)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}
	if n != rel.Size || hex.EncodeToString(h.Sum(nil)) != rel.SHA256 {
		return nil, fmt.Errorf("APK of %s does not match its app.json (size or SHA-256)", gh.TagName)
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return nil, err
	}
	// The APK first, the manifest last: appRelease offers a release only when
	// both agree, so a half-done update is never offered.
	if err := os.Rename(tmp.Name(), filepath.Join(dir, "sentinela.apk")); err != nil {
		return nil, err
	}
	js, _ := json.Marshal(rel)
	jtmp := filepath.Join(dir, ".app.json.tmp")
	if err := os.WriteFile(jtmp, js, 0o644); err != nil {
		return nil, err
	}
	if err := os.Rename(jtmp, filepath.Join(dir, "app.json")); err != nil {
		return nil, err
	}
	return &rel, nil
}
