package apps

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func appRequest(t *testing.T, a *Agent, method, url string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, url, bytes.NewReader(body))
	r.RemoteAddr = "127.0.0.1:12345"
	r.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()
	a.Handler("test-token").ServeHTTP(w, r)
	return w
}

func projectZIP(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	for name, body := range entries {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestZIPRejectsUnsafeEntries(t *testing.T) {
	for _, name := range []string{"../escape", "project/../../escape", "/absolute", "C:/Windows/file", "project/..\\escape"} {
		t.Run(name, func(t *testing.T) {
			b := projectZIP(t, map[string]string{name: "no"})
			if err := extractZip(context.Background(), bytes.NewReader(b), int64(len(b)), filepath.Join(t.TempDir(), "out")); err == nil {
				t.Fatal("unsafe ZIP accepted")
			}
		})
	}
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	h := &zip.FileHeader{Name: "project/link"}
	h.SetMode(os.ModeSymlink | 0o777)
	w, _ := z.CreateHeader(h)
	_, _ = w.Write([]byte("../../outside"))
	_ = z.Close()
	if err := extractZip(context.Background(), bytes.NewReader(buf.Bytes()), int64(buf.Len()), t.TempDir()); err == nil {
		t.Fatal("ZIP symlink accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b := projectZIP(t, map[string]string{"index.js": "ok"})
	if extractZip(ctx, bytes.NewReader(b), int64(len(b)), t.TempDir()) == nil {
		t.Fatal("cancelled extraction succeeded")
	}
}

func TestZIPImportEncryptsAndDeletesEnvironmentFiles(t *testing.T) {
	a, eng, _ := newTestAgent(t)
	b := projectZIP(t, map[string]string{"bot/package.json": `{"scripts":{"start":"node index.js"}}`, "bot/index.js": "setInterval(()=>{},1000)", "bot/.env": "DISCORD_TOKEN=zip-token-private\nTIMEZONE=America/Sao_Paulo\n", "bot/.env.production": "OTHER_KEY=private-secondary\n", "bot/.env.example": "DISCORD_TOKEN=example", "bot/node_modules/ignored.js": "ignored"})
	w := appRequest(t, a, http.MethodPost, "/v1/uploads", b)
	if w.Code != http.StatusCreated {
		t.Fatalf("upload: %d %s", w.Code, w.Body.String())
	}
	var up struct{ Path string }
	_ = json.Unmarshal(w.Body.Bytes(), &up)
	uploaded := filepath.Join(a.Cfg.StateDir, "uploads", up.Path)
	for _, name := range []string{".env", ".env.production", "node_modules/ignored.js"} {
		if _, err := os.Stat(filepath.Join(uploaded, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("file not removed: %s %v", name, err)
		}
	}
	encrypted, err := os.ReadFile(a.uploadVaultFile(up.Path))
	if err != nil || bytes.Contains(encrypted, []byte("zip-token-private")) {
		t.Fatal("temporary secrets were not encrypted")
	}
	d, err := a.Inspect(context.Background(), App{Source: SourceZip, Path: up.Path})
	if err != nil || len(d.EnvKeys) != 3 {
		t.Fatalf("inspect keys: %v %+v", err, d.EnvKeys)
	}
	in := nodeApp()
	in.Source, in.Path, in.Env = SourceZip, up.Path, nil
	v, err := a.Create(in)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Enqueue(v.ID, "deploy"); err != nil {
		t.Fatal(err)
	}
	runQueued(t, a)
	svc, _ := a.viewOf(v.ID)
	if !svc.Managed || svc.Status.State != "running" {
		t.Fatalf("deploy: %+v", svc.Status)
	}
	if _, err := os.Stat(uploaded); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("ZIP source was not moved")
	}
	spec := eng.containers[containerName(v.ID)].spec
	if !strings.Contains(strings.Join(spec.Env, "\n"), "DISCORD_TOKEN=zip-token-private") {
		t.Fatal("vault was not injected")
	}
	for _, dir := range []string{filepath.Join(a.appDir(v.ID), "project"), a.workDir(a.snapshot(v.ID), svc.Deployed)} {
		if _, err := os.Stat(filepath.Join(dir, ".env")); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("plaintext env in project/release")
		}
	}
	w = appRequest(t, a, http.MethodGet, "/v1/apps/"+v.ID+"/secrets", nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), "zip-token-private") || !strings.Contains(w.Body.String(), "DISCORD_TOKEN") {
		t.Fatalf("masked manager: %d %s", w.Code, w.Body.String())
	}
	state, _ := os.ReadFile(a.stateFile())
	if bytes.Contains(state, []byte("zip-token-private")) {
		t.Fatal("plaintext secret in state")
	}
}

func TestLocalMovePreservesSourceUntilSecretsAreSecured(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		t.Run(map[bool]string{false: "valid", true: "invalid-env"}[invalid], func(t *testing.T) {
			a, _, _ := newTestAgent(t)
			a.Cfg.LocalRoot = t.TempDir()
			a.Sources[SourceLocal] = LocalSource{Root: a.Cfg.LocalRoot}
			source := filepath.Join(a.Cfg.LocalRoot, "bot")
			_ = os.MkdirAll(source, 0o700)
			env := "TOKEN=folder-private\n"
			if invalid {
				env = "this is not dotenv\n"
			}
			_ = os.WriteFile(filepath.Join(source, ".env"), []byte(env), 0o600)
			_ = os.WriteFile(filepath.Join(source, "index.js"), []byte("original"), 0o600)
			in := nodeApp()
			in.Source, in.Path, in.Env = SourceLocal, "bot", nil
			v, err := a.Create(in)
			if err != nil {
				t.Fatal(err)
			}
			_ = a.Enqueue(v.ID, "deploy")
			runQueued(t, a)
			svc, _ := a.viewOf(v.ID)
			if invalid {
				if svc.Managed || svc.Status.Job.Phase != "failed" {
					t.Fatal("invalid import committed")
				}
				if b, err := os.ReadFile(filepath.Join(source, "index.js")); err != nil || string(b) != "original" {
					t.Fatal("original lost on import failure")
				}
				return
			}
			if !svc.Managed {
				t.Fatal("folder not managed")
			}
			if _, err := os.Stat(source); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("original folder not moved")
			}
			if b, err := os.ReadFile(filepath.Join(a.appDir(v.ID), "project", "index.js")); err != nil || string(b) != "original" {
				t.Fatal("protected files lost")
			}
		})
	}
}

func TestExplorerRejectsTraversalAndStaleWrites(t *testing.T) {
	a, _, _ := newTestAgent(t)
	v, err := a.Create(nodeApp())
	if err != nil {
		t.Fatal(err)
	}
	a.find(v.ID).Managed = true
	dir := filepath.Join(a.appDir(v.ID), "project")
	_ = os.MkdirAll(dir, 0o700)
	_ = os.WriteFile(filepath.Join(dir, "empty.txt"), nil, 0o600)
	for _, p := range []string{"../secrets.enc", ".git/config", "/etc/passwd", ".sentinela-edit-x"} {
		w := appRequest(t, a, http.MethodGet, "/v1/apps/"+v.ID+"/files/text?path="+p, nil)
		if w.Code == 200 {
			t.Fatalf("unsafe path accepted: %s", p)
		}
	}
	w := appRequest(t, a, http.MethodPut, "/v1/apps/"+v.ID+"/files/text?path=empty.txt", []byte(`{"kind":"text","revision":"","content":"overwrite"}`))
	if w.Code != 409 {
		t.Fatalf("empty file overwritten without revision: %d", w.Code)
	}
	w = appRequest(t, a, http.MethodGet, "/v1/apps/"+v.ID+"/files/text?path=empty.txt", nil)
	var doc fileDocument
	_ = json.Unmarshal(w.Body.Bytes(), &doc)
	doc.Content = "edited"
	body, _ := json.Marshal(doc)
	w = appRequest(t, a, http.MethodPut, "/v1/apps/"+v.ID+"/files/text?path=empty.txt", body)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = appRequest(t, a, http.MethodPut, "/v1/apps/"+v.ID+"/files/text?path=empty.txt", body)
	if w.Code != 409 {
		t.Fatal("stale edit accepted")
	}
	w = appRequest(t, a, http.MethodPut, "/v1/apps/"+v.ID+"/files/upload?path=.env.local", []byte("API_KEY=explorer-private\n"))
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dir, ".env.local")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("explorer left a plaintext .env")
	}
	if !strings.Contains(strings.Join(a.env(a.snapshot(v.ID), true), "\n"), "API_KEY=explorer-private") {
		t.Fatal("explorer env not imported")
	}
}
