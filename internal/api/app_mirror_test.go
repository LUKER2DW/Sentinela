package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LUKER2DW/sentinela/internal/apk/apktest"
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
	cert := []byte("release certificate")
	pin := sha256.Sum256(cert)
	srv.Cfg.AppUpdates.SignerSHA256 = []string{hex.EncodeToString(pin[:])}
	apk := apktest.Signed(t, cert)
	h := sha256.Sum256(apk)
	sum := hex.EncodeToString(h[:])
	old := githubAPI
	t.Cleanup(func() { githubAPI = old })

	// A tampered APK (hash mismatch) is never offered.
	githubAPI = fakeGitHub(t, apk, 10200, strings.Repeat("0", 64)).URL
	if _, err := srv.mirrorAppOnce(context.Background(), "o/r"); err == nil || srv.appRelease() != nil {
		t.Fatalf("tampered APK: err=%v offered=%v", err, srv.appRelease())
	}
	// Signed with another key, however consistent the release: never offered.
	foreign := apktest.Signed(t, []byte("someone else's certificate"))
	fh := sha256.Sum256(foreign)
	githubAPI = fakeGitHub(t, foreign, 10200, hex.EncodeToString(fh[:])).URL
	if _, err := srv.mirrorAppOnce(context.Background(), "o/r"); err == nil || !strings.Contains(err.Error(), "signer_sha256") || srv.appRelease() != nil {
		t.Fatalf("foreign signer: err=%v offered=%v", err, srv.appRelease())
	}
	// Not an APK with a v2/v3 signature at all.
	plain := []byte(strings.Repeat("APK!", 1000))
	ph := sha256.Sum256(plain)
	githubAPI = fakeGitHub(t, plain, 10200, hex.EncodeToString(ph[:])).URL
	if _, err := srv.mirrorAppOnce(context.Background(), "o/r"); err == nil || srv.appRelease() != nil {
		t.Fatalf("unsigned: err=%v offered=%v", err, srv.appRelease())
	}

	githubAPI = fakeGitHub(t, apk, 10200, sum).URL
	rel, err := srv.mirrorAppOnce(context.Background(), "o/r")
	if err != nil || rel == nil || rel.VersionCode != 10200 {
		t.Fatalf("mirror: %v %+v", err, rel)
	}
	if got := srv.appRelease(); got == nil || got.SHA256 != sum || got.Notes != "Sentinela v1.2.0" || got.Signer != srv.Cfg.AppUpdates.SignerSHA256[0] {
		t.Fatalf("offered: %+v", got)
	}
	// Same version again: nothing to do.
	if rel, err := srv.mirrorAppOnce(context.Background(), "o/r"); err != nil || rel != nil {
		t.Fatalf("second run: %v %+v", err, rel)
	}
	// No pin for the repository: nothing is downloaded.
	srv.Cfg.AppUpdates.SignerSHA256 = nil
	if _, err := srv.mirrorAppOnce(context.Background(), "o/r"); err == nil || !strings.Contains(err.Error(), "signer_sha256 is not set") {
		t.Fatalf("no pin: %v", err)
	}
}

// An APK left on offer without a recorded signer (an older mirror, a hand
// publication) gets its signer recorded when it is pinned, and stops being
// offered when it is not.
func TestMirrorChecksOfferedSigner(t *testing.T) {
	_, srv := newTestServer(t)
	cert := []byte("release certificate")
	pin := sha256.Sum256(cert)
	pins := []string{hex.EncodeToString(pin[:])}
	publish := func(apk []byte) {
		t.Helper()
		h := sha256.Sum256(apk)
		dir := srv.appDir()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "sentinela.apk"), apk, 0o644); err != nil {
			t.Fatal(err)
		}
		js := fmt.Sprintf(`{"version_code":10100,"version_name":"1.1.0","size":%d,"sha256":"%s"}`, len(apk), hex.EncodeToString(h[:]))
		if err := os.WriteFile(filepath.Join(dir, "app.json"), []byte(js), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	publish(apktest.Signed(t, cert))
	if err := srv.checkOfferedSigner(pins); err != nil {
		t.Fatal(err)
	}
	if got := srv.appRelease(); got == nil || got.Signer != pins[0] {
		t.Fatalf("pinned APK on offer: %+v", got)
	}
	publish(apktest.Signed(t, []byte("other")))
	if err := srv.checkOfferedSigner(pins); err == nil || srv.appRelease() != nil {
		t.Fatalf("foreign APK on offer: err=%v offered=%+v", err, srv.appRelease())
	}
}
