package apps

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
)

func TestVaultKeyAndAuthentication(t *testing.T) {
	a, _, _ := newTestAgent(t)
	v, err := a.Create(nodeApp())
	if err != nil {
		t.Fatal(err)
	}
	encrypted, _ := os.ReadFile(a.vaultFile(v.ID))
	if bytes.Contains(encrypted, []byte("s3cret")) {
		t.Fatal("vault contains plaintext")
	}
	state, _ := os.ReadFile(a.stateFile())
	if bytes.Contains(state, []byte("s3cret")) {
		t.Fatal("state contains plaintext")
	}
	if _, err := a.readVault(a.vaultFile(v.ID), "different-service"); err == nil {
		t.Fatal("vault not bound to its service")
	}
	if err := a.Load(); err != nil {
		t.Fatal(err)
	}
	if a.snapshot(v.ID).Env[0].Value != "s3cret" {
		t.Fatal("secret did not survive reload")
	}
	_ = os.Remove(a.Cfg.StateDir + "/secrets.key")
	if a.Load() == nil {
		t.Fatal("missing key silently regenerated")
	}
	if _, err := os.Stat(a.Cfg.StateDir + "/secrets.key"); !os.IsNotExist(err) {
		t.Fatal("key regenerated for existing vault")
	}
}

func TestManagerRotationDeletionAndStaleRevision(t *testing.T) {
	a, eng, _ := newTestAgent(t)
	v, err := a.Create(nodeApp())
	if err != nil {
		t.Fatal(err)
	}
	_ = a.Enqueue(v.ID, "deploy")
	runQueued(t, a)
	read := func() struct {
		Env      []EnvVar
		Revision string
	} {
		w := appRequest(t, a, http.MethodGet, "/v1/apps/"+v.ID+"/secrets", nil)
		var got struct {
			Env      []EnvVar
			Revision string
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		for _, e := range got.Env {
			if e.Value != "" {
				t.Fatal("manager disclosed secret")
			}
		}
		return got
	}
	doc := read()
	doc.Env[0].Value = "rotated-private"
	body, _ := json.Marshal(doc)
	w := appRequest(t, a, http.MethodPut, "/v1/apps/"+v.ID+"/secrets", body)
	if w.Code != 200 {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	runQueued(t, a)
	if !slices.Contains(eng.containers[containerName(v.ID)].spec.Env, "TOKEN=rotated-private") {
		t.Fatal("new secret not injected")
	}
	w = appRequest(t, a, http.MethodPut, "/v1/apps/"+v.ID+"/secrets", body)
	if w.Code != 409 {
		t.Fatal("stale manager write accepted")
	}
	if text := a.safeText(v.ID, "s3cret rotated-private"); strings.Contains(text, "s3cret") || strings.Contains(text, "rotated-private") {
		t.Fatal("old/new log values not redacted")
	}
	doc = read()
	doc.Env = nil
	body, _ = json.Marshal(doc)
	w = appRequest(t, a, http.MethodPut, "/v1/apps/"+v.ID+"/secrets", body)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	runQueued(t, a)
	for _, e := range eng.containers[containerName(v.ID)].spec.Env {
		if strings.HasPrefix(e, "TOKEN=") {
			t.Fatal("deleted secret resurrected")
		}
	}
	if err := a.Load(); err != nil {
		t.Fatal(err)
	}
	if len(a.vaults[v.ID].Vars) != 0 || len(a.snapshot(v.ID).Env) != 0 {
		t.Fatal("deletion not persisted")
	}
}

func TestFailedUpdateRestoresAndKeepsDiagnostics(t *testing.T) {
	a, eng, src := newTestAgent(t)
	v, err := a.Create(nodeApp())
	if err != nil {
		t.Fatal(err)
	}
	_ = a.Enqueue(v.ID, "deploy")
	runQueued(t, a)
	before := a.snapshot(v.ID).Deployed
	src.sha = "abcdef0123456789abcdef0123456789abcdef0123"
	eng.crashVersion = src.sha[:12]
	eng.crashExit = 132
	eng.output = "stderr private s3cret"
	_ = a.Enqueue(v.ID, "deploy")
	runQueued(t, a)
	after, _ := a.viewOf(v.ID)
	if after.Deployed.Release != before.Release || !eng.containers[containerName(v.ID)].running {
		t.Fatal("last working release not restored")
	}
	if after.Status.Job.Phase != "failed" || after.Status.LastExit == nil || after.Status.LastExit.Code != 132 || !strings.Contains(after.Status.Job.Error, "SIGILL") {
		t.Fatalf("missing failure diagnostics: %+v", after.Status)
	}
	if err := a.Load(); err != nil {
		t.Fatal(err)
	}
	after, _ = a.viewOf(v.ID)
	if after.Status.Job.Phase != "failed" || after.Status.LastExit.Code != 132 {
		t.Fatal("failure not persisted")
	}
	w := appRequest(t, a, http.MethodGet, "/v1/apps/"+v.ID+"/deploy-log", nil)
	if strings.Contains(w.Body.String(), "s3cret") {
		t.Fatal("secret leaked in deploy log")
	}
	lines, err := a.Logs(context.Background(), v.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range lines {
		if strings.Contains(l.Text, "s3cret") {
			t.Fatal("secret leaked in runtime log")
		}
	}
}

func TestDotEnvLiteralParsing(t *testing.T) {
	env, err := parseDotEnv([]byte("export TOKEN=abc # comment\nMULTI=\"a\\nb\"\nLITERAL='$(unsafe) ${TOKEN}'\nTOKEN=last\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(env) != 3 || env[0].Value != "last" || env[1].Value != "a\nb" || env[2].Value != "$(unsafe) ${TOKEN}" {
		t.Fatalf("unexpected parsed env: %+v", env)
	}
	for _, b := range []string{"BROKEN", "TOKEN='unclosed", "TOKEN=abc\x00bad"} {
		if _, err := parseDotEnv([]byte(b)); err == nil {
			t.Fatal("invalid env accepted")
		}
	}
}

func TestComposeSecretsStayOutOfRenderedFiles(t *testing.T) {
	a, _, _ := newTestAgent(t)
	v, err := a.Create(nodeApp())
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	_ = os.MkdirAll(work+"/deploy", 0o700)
	_ = os.WriteFile(work+"/deploy/compose.yaml", []byte("services:\n  bot:\n    image: node:22\n    env_file: ../.env\n    volumes: [../src:/app]\n"), 0o600)
	a.mu.Lock()
	vault := a.vaults[v.ID]
	vault.Files = []string{".env"}
	err = a.storeVaultLocked(v.ID, vault)
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	input, err := a.composeInput(a.snapshot(v.ID), work, "deploy/compose.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if input != "deploy/.sentinela-input.yaml" {
		t.Fatalf("relative paths changed: %s", input)
	}
	b, _ := os.ReadFile(work + "/" + input)
	if bytes.Contains(b, []byte("env_file")) || bytes.Contains(b, []byte("s3cret")) {
		t.Fatal("imported env reference/value left in Compose input")
	}
	doc := map[string]any{"environment": map[string]any{"TOKEN": "s3cret", "URL": "https://example.test/s3cret"}, "command": "echo $$keep $SHELL"}
	protectComposeValues(doc, []EnvVar{{Key: "TOKEN", Value: "s3cret", Secret: true}})
	out, _ := json.Marshal(doc)
	if bytes.Contains(out, []byte("s3cret")) || !bytes.Contains(out, []byte("${TOKEN}")) {
		t.Fatal("Compose output disclosed manager value")
	}
	if doc["command"] != "echo $$$$keep $$SHELL" {
		t.Fatal("literal shell dollars not escaped")
	}
}

func TestConfigMaskHandlesEscapedAndOverlappingSecrets(t *testing.T) {
	text := `{"password":"a\"b","token":"long-secret","port":"1"}`
	masked, markers := maskConfig(text, []string{`a"b`, "long-secret", "1"})
	if strings.Contains(masked, `a\"b`) || strings.Contains(masked, "long-secret") {
		t.Fatal("escaped secret disclosed")
	}
	var pairs []string
	for k, v := range markers {
		pairs = append(pairs, k, v)
	}
	if strings.NewReplacer(pairs...).Replace(masked) != text {
		t.Fatal("markers were overwritten by a short secret")
	}
	a, _, _ := newTestAgent(t)
	v, err := a.Create(nodeApp())
	if err != nil {
		t.Fatal(err)
	}
	if err := a.importEnvDocument(v.ID, ".env", []EnvVar{{Key: "QUOTED_TOKEN", Value: `a"b`}}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(a.safeText(v.ID, text), `a\"b`) {
		t.Fatal("escaped secret in log")
	}
}
