package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fakeGitHub(t *testing.T, apk []byte, code int, sum string) *httptest.Server {
	t.Helper()
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/o/r/releases/latest":
			fmt.Fprintf(w, `{"tag_name":"v1.2.0","assets":[{"name":"sentinela.apk","browser_download_url":"%[1]s/dl/apk"},{"name":"app.json","browser_download_url":"%[1]s/dl/json"}]}`, ts.URL)
		case "/dl/json":
			fmt.Fprintf(w, `{"version_code":%d,"version_name":"1.2.0","size":%d,"sha256":"%s"}`, code, len(apk), sum)
		case "/dl/apk":
			_, _ = w.Write(apk)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return ts
}

func TestMirrorAppReleases(t *testing.T) {
	_, srv := newTestServer(t)
	apk := []byte(strings.Repeat("APK!", 1000))
	h := sha256.Sum256(apk)
	sum := hex.EncodeToString(h[:])
	old := githubAPI
	t.Cleanup(func() { githubAPI = old })

	// A tampered APK (hash mismatch) is never offered.
	githubAPI = fakeGitHub(t, apk, 10200, strings.Repeat("0", 64)).URL
	if _, err := srv.mirrorAppOnce(context.Background(), "o/r"); err == nil || srv.appRelease() != nil {
		t.Fatalf("tampered APK: err=%v offered=%v", err, srv.appRelease())
	}

	githubAPI = fakeGitHub(t, apk, 10200, sum).URL
	rel, err := srv.mirrorAppOnce(context.Background(), "o/r")
	if err != nil || rel == nil || rel.VersionCode != 10200 {
		t.Fatalf("mirror: %v %+v", err, rel)
	}
	if got := srv.appRelease(); got == nil || got.SHA256 != sum || got.Notes != "Sentinela v1.2.0" {
		t.Fatalf("offered: %+v", got)
	}
	// Same version again: nothing to do.
	if rel, err := srv.mirrorAppOnce(context.Background(), "o/r"); err != nil || rel != nil {
		t.Fatalf("second run: %v %+v", err, rel)
	}
}
