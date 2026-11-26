package apps

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestHelperReservedNames(t *testing.T) {
	for name, want := range map[string]bool{
		"PATH": true, "path": true, "HOME": true, "SHELL": true, "IFS": true, "ENV": true, "BASH_ENV": true,
		"LD_PRELOAD": true, "LD_LIBRARY_PATH": true, "DOCKER_HOST": true, "DOCKER_CONFIG": true, "DOCKER_CONTEXT": true,
		"COMPOSE_FILE": true, "COMPOSE_PROJECT_NAME": true, "COMPOSE_ENV_FILES": true, "BUILDX_BUILDER": true,
		"XDG_CONFIG_HOME": true, "SSL_CERT_FILE": true, "GODEBUG": true,
		"COMPOSE_PROFILES": false, "API_URL": false, "TOKEN": false, "PORT": false, "SENTINELA_APP": false,
		"HTTP_PROXY": false, "PATHOLOGY": false, "GOOGLE_API_KEY": false,
	} {
		if got := helperReserved(name); got != want {
			t.Errorf("helperReserved(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestComposeHelperEnv(t *testing.T) {
	out, skipped := composeHelperEnv([]string{"SENTINELA_APP=x", "PATH=/tmp/evil", "LD_PRELOAD=/tmp/x.so", "DOCKER_HOST=tcp://a:1",
		"DOCKER_CONFIG=/tmp/evil", "COMPOSE_FILE=/x", "API_URL=http://a", "COMPOSE_PROFILES=web", "HOME=/root", "PATH=/again"})
	want := []string{"PATH=" + composeHelperPath, "HOME=/tmp", "DOCKER_CONFIG=/tmp/.docker", "SENTINELA_APP=x", "API_URL=http://a", "COMPOSE_PROFILES=web"}
	if !slices.Equal(out, want) {
		t.Fatalf("env %v, want %v", out, want)
	}
	if !slices.Equal(skipped, []string{"PATH", "LD_PRELOAD", "DOCKER_HOST", "DOCKER_CONFIG", "COMPOSE_FILE", "HOME"}) {
		t.Fatalf("skipped %v", skipped)
	}
	for _, s := range skipped {
		for _, kv := range out[3:] {
			if strings.HasPrefix(kv, s+"=") {
				t.Errorf("%s leaked into %v", s, out)
			}
		}
	}
}

// helperSpecs returns the specs of the Compose helper containers created.
func helperSpecs(eng *fakeEngine, id string) []Spec {
	eng.mu.Lock()
	defer eng.mu.Unlock()
	var out []Spec
	for _, c := range eng.created {
		if c.name == "sapp-"+id+"-compose" {
			out = append(out, c.spec)
		}
	}
	return out
}

func deployCompose(t *testing.T, a *Agent, eng *fakeEngine, src *fakeSource, in App, config func(id string) string) ServiceView {
	t.Helper()
	src.files = map[string]string{"compose.yaml": "services: {}"}
	in.Source, in.Repo, in.Mode = SourceGitHub, "ana/site", ModeCompose
	v, err := a.Create(in)
	if err != nil {
		t.Fatal(err)
	}
	eng.composeConfig = config(v.ID)
	eng.members = []Summary{{Name: "sapp-" + v.ID + "-web-1", App: v.ID, Service: "web", Running: true}}
	a.Enqueue(v.ID, "deploy")
	runQueued(t, a)
	got, _ := a.viewOf(v.ID)
	return got
}

// ServiceView is what the tests read back of a deployed service.
type ServiceView = View

func TestComposeHelperGetsOnlyAFixedEnvironment(t *testing.T) {
	a, eng, src := newTestAgent(t)
	got := deployCompose(t, a, eng, src, App{Name: "Site", Env: []EnvVar{
		{Key: "PATH", Value: "/srv/evil"}, {Key: "LD_PRELOAD", Value: "/srv/x.so"}, {Key: "DOCKER_HOST", Value: "tcp://203.0.113.9:2375"},
		{Key: "COMPOSE_FILE", Value: "/srv/c.yml"}, {Key: "HOME", Value: "/srv"}, {Key: "TOKEN", Value: "s3cret", Secret: true}, {Key: "API_URL", Value: "http://api"},
	}}, func(string) string {
		return `{"services":{"web":{"image":"nginx"}}}`
	})
	if got.Status.Job.Phase != "done" {
		t.Fatalf("deploy: %+v", got.Status.Job)
	}
	specs := helperSpecs(eng, got.ID)
	if len(specs) != 2 {
		t.Fatalf("helper runs: %d", len(specs))
	}
	for _, sp := range specs {
		if sp.Cmd[0] != composeDockerBin || !strings.HasPrefix(sp.Cmd[0], "/") {
			t.Errorf("docker is not run by absolute path: %v", sp.Cmd)
		}
		for _, kv := range sp.Env {
			k, v, _ := strings.Cut(kv, "=")
			switch k {
			case "PATH":
				if v != composeHelperPath {
					t.Errorf("PATH %q reached the helper", v)
				}
			case "HOME":
				if v != "/tmp" {
					t.Errorf("HOME %q reached the helper", v)
				}
			case "DOCKER_CONFIG":
				if v != "/tmp/.docker" {
					t.Errorf("DOCKER_CONFIG %q", v)
				}
			case "LD_PRELOAD", "DOCKER_HOST", "COMPOSE_FILE":
				t.Errorf("%s reached the helper", k)
			}
		}
		ro := a.composeDir(got.ID) + ":" + a.composeDir(got.ID) + ":ro"
		if !slices.Contains(sp.Binds, ro) {
			t.Errorf("rendered folder not mounted read-only: %v", sp.Binds)
		}
		for _, b := range sp.Binds {
			if b != a.Cfg.Docker+":/var/run/docker.sock" && !strings.HasSuffix(b, ":ro") {
				t.Errorf("helper gets a writable folder: %s", b)
			}
			if strings.HasPrefix(b, a.appDir(got.ID)+":") {
				t.Errorf("helper sees the whole service folder: %s", b)
			}
		}
	}
	// `config` interpolates the source file with the service's variables.
	for _, want := range []string{"TOKEN=s3cret", "API_URL=http://api"} {
		if !slices.Contains(specs[0].Env, want) {
			t.Errorf("interpolation variable %q missing from %v", strings.Split(want, "=")[0], specs[0].Env)
		}
	}
	// `up` only resolves the references the agent wrote, under its own names.
	want := []string{"PATH=" + composeHelperPath, "HOME=/tmp", "DOCKER_CONFIG=/tmp/.docker", composeVar("TOKEN") + "=s3cret"}
	if !slices.Equal(specs[1].Env, want) {
		t.Errorf("up environment %v, want %v", specs[1].Env, want)
	}
}

func TestRenderedComposeLivesOutsideAnyBindableFolder(t *testing.T) {
	a, eng, src := newTestAgent(t)
	got := deployCompose(t, a, eng, src, App{Name: "Site"}, func(string) string {
		return `{"services":{"web":{"image":"nginx"}}}`
	})
	rendered := filepath.Join(a.composeDir(got.ID), renderedCompose)
	if _, err := os.Stat(rendered); err != nil {
		t.Fatal(err)
	}
	if rel, err := filepath.Rel(a.appDir(got.ID), rendered); err == nil && !strings.HasPrefix(rel, "..") {
		t.Fatalf("rendered file %s is inside the service folder", rendered)
	}
	// Nothing planCompose accepts as a bind source can contain it.
	if _, err := planCompose([]byte(`{"services":{"a":{"image":"x","volumes":[{"type":"bind","source":"`+filepath.ToSlash(a.composeDir(got.ID))+`","target":"/c"}]}}}`),
		composeRules{ID: got.ID, Release: a.releaseDir(got.ID, got.Deployed.Release), Data: filepath.Join(a.appDir(got.ID), "data")}); err == nil {
		t.Fatal("a bind of the rendered folder was accepted")
	}
	if _, err := os.Stat(filepath.Join(a.workDir(&got.App, got.Deployed), ".sentinela-compose.json")); err == nil {
		t.Fatal("rendered file left in the release")
	}
	up := helperSpecs(eng, got.ID)[1]
	i := slices.Index(up.Cmd, "-f")
	if i < 0 || up.Cmd[i+1] != rendered || !slices.Contains(up.Cmd, "--project-directory") {
		t.Fatalf("up does not read the rendered file: %v", up.Cmd)
	}
	// Removing the service removes the rendered folder too.
	if err := a.Delete(context.Background(), got.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(a.composeDir(got.ID)); err == nil {
		t.Fatal("rendered folder kept after delete")
	}
}

func TestPrepareBindDirs(t *testing.T) {
	root := t.TempDir()
	mk := func(p string) { _ = os.MkdirAll(filepath.Join(root, filepath.FromSlash(p)), 0o755) }
	mk("data")
	if err := os.WriteFile(filepath.Join(root, "afile"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := os.Symlink(t.TempDir(), filepath.Join(root, "data", "link"))
	cases := []struct {
		name    string
		src     string
		wantErr bool
		wantDir bool
	}{
		{"missing nested", "data/a/b/c", false, true},
		{"existing", "data", false, true},
		{"existing file as the last part", "afile", false, false},
		{"through a file", "afile/x", true, false},
		{"outside", "../elsewhere", true, false},
		{"through a link", "data/link/x", true, false},
		{"a link itself", "data/link", true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if link != nil && strings.Contains(c.name, "link") {
				t.Skip("symlinks unavailable:", link)
			}
			err := prepareBindDirs(root, []string{filepath.Join(root, filepath.FromSlash(c.src))})
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, want error %v", err, c.wantErr)
			}
			if c.wantDir {
				st, err := os.Lstat(filepath.Join(root, filepath.FromSlash(c.src)))
				if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
					t.Fatalf("not a real folder: %v %v", st, err)
				}
			}
		})
	}
}

func TestPlanComposeBindDirsAndBuilds(t *testing.T) {
	root := "/var/lib/sentinela-apps/apps/x"
	raw := `{"services":{
	 "a":{"build":{"context":"` + root + `/releases/r1/src"},"volumes":[
	   {"type":"bind","source":"` + root + `/data/one","target":"/1","bind":{"create_host_path":true}},
	   {"type":"bind","source":"` + root + `/data/two","target":"/2","bind":{"create_host_path":false}},
	   {"type":"bind","source":"` + root + `/data/three","target":"/3"}]},
	 "b":{"image":"x"}}}`
	p, err := planCompose([]byte(raw), testRules(root))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(p.bindDirs, []string{root + "/data/one", root + "/data/three"}) {
		t.Errorf("bindDirs %v", p.bindDirs)
	}
	if len(p.hostPaths) != 3 {
		t.Errorf("hostPaths %v", p.hostPaths)
	}
	if !slices.Equal(p.builds, []string{"a"}) {
		t.Errorf("builds %v", p.builds)
	}
}

func TestComposeCreatesMissingBindFolderAsARealFolder(t *testing.T) {
	a, eng, src := newTestAgent(t)
	got := deployCompose(t, a, eng, src, App{Name: "Site"}, func(id string) string {
		return `{"services":{"web":{"image":"nginx","volumes":[{"type":"bind","source":"` + filepath.ToSlash(filepath.Join(a.appDir(id), "data", "uploads", "tmp")) + `","target":"/u"}]}}}`
	})
	if got.Status.Job.Phase != "done" {
		t.Fatalf("deploy: %+v", got.Status.Job)
	}
	st, err := os.Lstat(filepath.Join(a.appDir(got.ID), "data", "uploads", "tmp"))
	if err != nil || !st.IsDir() {
		t.Fatalf("bind folder not created: %v", err)
	}
}

// A link planted after the first check and before `up` (here, while the
// agent resolves the bind address) is met by the second check.
func TestComposeRechecksBindFoldersJustBeforeUp(t *testing.T) {
	a, eng, src := newTestAgent(t)
	outside := t.TempDir()
	var id string
	var planted error
	a.tailnetIP = func() string {
		dir := filepath.Join(a.appDir(id), "data", "swap")
		if err := os.RemoveAll(dir); err != nil {
			planted = err
		} else {
			planted = os.Symlink(outside, dir)
		}
		return "100.64.0.9"
	}
	src.files = map[string]string{"compose.yaml": "services: {}"}
	v, err := a.Create(App{Name: "Site", Source: SourceGitHub, Repo: "ana/site", Mode: ModeCompose, Exposure: ExposeTailnet})
	if err != nil {
		t.Fatal(err)
	}
	id = v.ID
	eng.composeConfig = `{"services":{"web":{"image":"nginx","volumes":[{"type":"bind","source":"` + filepath.ToSlash(filepath.Join(a.appDir(id), "data", "swap")) + `","target":"/s"}]}}}`
	a.Enqueue(id, "deploy")
	runQueued(t, a)
	if planted != nil {
		t.Skip("symlinks unavailable:", planted)
	}
	got, _ := a.viewOf(id)
	if got.Status.Job.Phase != "failed" || !strings.Contains(got.Status.Job.Error, "symbolic link") {
		t.Fatalf("link planted before up not refused: %+v", got.Status.Job)
	}
	if n := len(helperSpecs(eng, id)); n != 1 { // config only: up never ran
		t.Fatalf("helper runs: %d", n)
	}
}

func TestBuildsOutsideTheFirewallAreMarked(t *testing.T) {
	t.Run("buildkit retry", func(t *testing.T) {
		a, eng, src := newTestAgent(t)
		eng.buildKitOnly = true
		src.files = map[string]string{"Dockerfile": "FROM node\nRUN --mount=type=cache,target=/x true\n"}
		v, err := a.Create(App{Name: "Img", Source: SourceGitHub, Repo: "ana/img", Mode: ModeDockerfile, Ports: []Port{{Container: 8080}}, UnfilteredBuilds: true})
		if err != nil {
			t.Fatal(err)
		}
		a.Enqueue(v.ID, "deploy")
		runQueued(t, a)
		got, _ := a.viewOf(v.ID)
		if got.Status.Job.Phase != "done" || got.Deployed == nil || !got.Deployed.BuildOutsideFirewall {
			t.Fatalf("deploy %+v deployed %+v", got.Status.Job, got.Deployed)
		}
		if len(eng.builds) != 2 || eng.builds[0].Network != networkName || !eng.builds[1].BuildKit || eng.builds[1].Network != "" {
			t.Fatalf("builds %+v", eng.builds)
		}
		var warned bool
		for _, e := range got.Status.Events {
			warned = warned || e.Kind == "warning" && strings.Contains(e.Message, "outside the sn-*")
		}
		if !warned {
			t.Fatalf("no warning event: %+v", got.Status.Events)
		}
		raw, _ := json.Marshal(got.Deployed)
		if !strings.Contains(string(raw), `"build_outside_firewall":true`) {
			t.Fatalf("not in the deployment JSON: %s", raw)
		}
	})
	t.Run("classic build stays on sn-apps", func(t *testing.T) {
		a, eng, src := newTestAgent(t)
		src.files = map[string]string{"Dockerfile": "FROM node\n"}
		v, err := a.Create(App{Name: "Img", Source: SourceGitHub, Repo: "ana/img", Mode: ModeDockerfile, Ports: []Port{{Container: 8080}}})
		if err != nil {
			t.Fatal(err)
		}
		a.Enqueue(v.ID, "deploy")
		runQueued(t, a)
		got, _ := a.viewOf(v.ID)
		if got.Deployed == nil || got.Deployed.BuildOutsideFirewall || len(eng.builds) != 1 || eng.builds[0].Network != networkName {
			t.Fatalf("deployed %+v builds %+v", got.Deployed, eng.builds)
		}
	})
	t.Run("compose build", func(t *testing.T) {
		a, eng, src := newTestAgent(t)
		got := deployCompose(t, a, eng, src, App{Name: "Site", UnfilteredBuilds: true}, func(string) string {
			return `{"services":{"web":{"build":{"context":"@WORK@"}}}}`
		})
		if got.Deployed == nil || !got.Deployed.BuildOutsideFirewall {
			t.Fatalf("compose build not marked: %+v", got.Deployed)
		}
	})
	t.Run("compose image only", func(t *testing.T) {
		a, eng, src := newTestAgent(t)
		got := deployCompose(t, a, eng, src, App{Name: "Site"}, func(string) string { return `{"services":{"web":{"image":"nginx"}}}` })
		if got.Deployed == nil || got.Deployed.BuildOutsideFirewall {
			t.Fatalf("marked without a build: %+v", got.Deployed)
		}
	})
}

func TestDetectRefusesLinksAndBigFiles(t *testing.T) {
	mkdir := func(t *testing.T, files map[string]string) string {
		dir := t.TempDir()
		for n, body := range files {
			if err := os.WriteFile(filepath.Join(dir, n), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}
	pad := strings.Repeat("a", maxDetectFile)
	cases := []struct {
		name      string
		prepare   func(t *testing.T) string
		wantStart string
	}{
		{"normal", func(t *testing.T) string {
			return mkdir(t, map[string]string{"package.json": `{"scripts":{"start":"x"}}`})
		}, "npm start"},
		{"over 1 MiB is not read", func(t *testing.T) string {
			return mkdir(t, map[string]string{"package.json": `{"scripts":{"start":"x"},"pad":"` + pad + `"}`})
		}, "node index.js"},
		{"exactly the cap is read", func(t *testing.T) string {
			body := `{"scripts":{"start":"x"},"pad":""}`
			return mkdir(t, map[string]string{"package.json": strings.Replace(body, `""`, `"`+strings.Repeat("a", maxDetectFile-len(body))+`"`, 1)})
		}, "npm start"},
		{"link to a file outside", func(t *testing.T) string {
			out := mkdir(t, map[string]string{"real.json": `{"scripts":{"start":"x"}}`})
			dir := mkdir(t, nil)
			if err := os.Symlink(filepath.Join(out, "real.json"), filepath.Join(dir, "package.json")); err != nil {
				t.Skip("symlinks unavailable:", err)
			}
			return dir
		}, "node index.js"},
		{"link to a file inside", func(t *testing.T) string {
			dir := mkdir(t, map[string]string{"real.json": `{"scripts":{"start":"x"}}`})
			if err := os.Symlink("real.json", filepath.Join(dir, "package.json")); err != nil {
				t.Skip("symlinks unavailable:", err)
			}
			return dir
		}, "node index.js"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := Detect(c.prepare(t))
			if d.Runtime != "node" || d.Start != c.wantStart {
				t.Fatalf("runtime %q start %q, want node %q", d.Runtime, d.Start, c.wantStart)
			}
		})
	}
	if d := Detect(filepath.Join(t.TempDir(), "missing")); d.Mode != ModeScript || d.Runtime != "shell" {
		t.Fatalf("missing folder: %+v", d)
	}
}

func TestReadCapped(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	for name, size := range map[string]int{"small": 10, "exact": 100, "big": 101} {
		if err := os.WriteFile(filepath.Join(dir, name), make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "folder"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		want error
		n    int
	}{{"small", nil, 10}, {"exact", nil, 100}, {"big", errTooLarge, 0}, {"folder", errNotRegular, 0}} {
		b, err := readCapped(root, c.name, 100)
		if err != c.want || len(b) != c.n {
			t.Errorf("%s: %d bytes, %v; want %d, %v", c.name, len(b), err, c.n, c.want)
		}
	}
	if _, err := readCapped(root, "missing", 100); err == nil {
		t.Error("missing file read")
	}
}

func TestGitFilesAreCapped(t *testing.T) {
	sha := strings.Repeat("a", 40)
	for _, c := range []struct {
		name  string
		files map[string]string
		ok    bool
	}{
		{"loose ref", map[string]string{".git/HEAD": "ref: refs/heads/main\n", ".git/refs/heads/main": sha + "\n"}, true},
		{"packed ref", map[string]string{".git/HEAD": "ref: refs/heads/main\n", ".git/packed-refs": "# pack-refs\n" + sha + " refs/heads/main\n"}, true},
		{"detached", map[string]string{".git/HEAD": sha + "\n"}, true},
		{"huge HEAD", map[string]string{".git/HEAD": sha + strings.Repeat("\n", maxGitSmall)}, false},
		{"huge ref file", map[string]string{".git/HEAD": "ref: refs/heads/main\n", ".git/refs/heads/main": strings.Repeat("a", maxGitSmall+1)}, false},
		{"huge packed-refs", map[string]string{".git/HEAD": "ref: refs/heads/main\n", ".git/packed-refs": strings.Repeat("#", maxPackedRefs) + "\n" + sha + " refs/heads/main\n"}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			for n, body := range c.files {
				p := filepath.Join(dir, filepath.FromSlash(n))
				_ = os.MkdirAll(filepath.Dir(p), 0o755)
				if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			root, err := os.OpenRoot(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			if v, ok := gitHead(root); ok != c.ok || ok && v.Commit != sha {
				t.Fatalf("gitHead = %+v, %v; want ok %v", v, ok, c.ok)
			}
		})
	}
}

func TestBrowseIsBounded(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < maxBrowse+60; i++ {
		if err := os.Mkdir(filepath.Join(root, fmt.Sprintf("p%05d", i)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	small := t.TempDir()
	for _, n := range []string{"alpha", "beta"} {
		_ = os.Mkdir(filepath.Join(small, n), 0o755)
	}
	_ = os.WriteFile(filepath.Join(small, "alpha", "package.json"), []byte("{}"), 0o644)
	for _, c := range []struct {
		name      string
		root      string
		wantMax   int
		truncated bool
	}{{"huge folder", root, maxBrowse, true}, {"small folder", small, 2, false}} {
		t.Run(c.name, func(t *testing.T) {
			dirs, truncated, err := (LocalSource{Root: c.root}).BrowseLimited("")
			if err != nil {
				t.Fatal(err)
			}
			if len(dirs) > c.wantMax || truncated != c.truncated || len(dirs) == 0 {
				t.Fatalf("%d folders, truncated %v", len(dirs), truncated)
			}
			if c.root == small && !slices.Equal(dirs[0].Markers, []string{"node"}) {
				t.Fatalf("markers %+v", dirs)
			}
		})
	}
	// The picker route reports the cut.
	a, _, _ := newTestAgent(t)
	a.Sources[SourceLocal] = LocalSource{Root: root}
	w := appRequest(t, a, http.MethodGet, "/v1/local", nil)
	var body struct {
		Truncated bool `json:"truncated"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || !body.Truncated {
		t.Fatalf("route: %d %s", w.Code, w.Body.String())
	}
}

func TestInspectConcurrencyAndStaleTemp(t *testing.T) {
	a, _, _ := newTestAgent(t)
	stale := filepath.Join(a.Cfg.StateDir, "tmp", "inspect-deadbeef")
	if err := os.MkdirAll(filepath.Join(stale, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(a.Cfg.StateDir, "tmp", "other")
	if err := os.MkdirAll(keep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := a.Load(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); err == nil {
		t.Error("stale inspection folder not swept at start")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Error("an unrelated folder was removed")
	}
	// With every slot taken, one more inspection is refused at once and no
	// snapshot is read.
	for i := 0; i < maxInspect; i++ {
		a.inspectSem <- struct{}{}
	}
	if _, err := a.Inspect(context.Background(), App{Source: SourceGitHub, Repo: "ana/x", Ref: "main"}); err == nil || !strings.Contains(err.Error(), "inspections are running") {
		t.Fatalf("inspection beyond the cap: %v", err)
	}
	<-a.inspectSem
	if _, err := a.Inspect(context.Background(), App{Source: SourceGitHub, Repo: "ana/x", Ref: "main"}); err != nil && strings.Contains(err.Error(), "inspections are running") {
		t.Fatalf("free slot refused: %v", err)
	}
}

func TestDirSizeBounded(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"a", "a/b", "c"} {
		_ = os.MkdirAll(filepath.Join(root, d), 0o755)
	}
	n := 0
	for _, d := range []string{".", "a", "a/b", "c"} {
		for i := 0; i < 3*diskBatch/2; i++ { // more than one batch per folder
			n++
			if err := os.WriteFile(filepath.Join(root, d, fmt.Sprintf("f%d", i)), []byte("0123456789"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, c := range []struct {
		name     string
		limit    time.Duration
		max      int
		complete bool
		total    int64
	}{
		{"all", time.Minute, 1_000_000, true, int64(n) * 10},
		{"entry cap", time.Minute, 100, false, -1},
		{"deadline", -time.Second, 1_000_000, false, -1},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, complete := dirSizeBounded(root, c.limit, c.max)
			if complete != c.complete || c.total >= 0 && got != c.total || c.total < 0 && got > int64(c.max)*10 {
				t.Fatalf("%d bytes, complete %v", got, complete)
			}
		})
	}
	if got := dirSize(root, time.Minute); got != int64(n)*10 {
		t.Fatalf("dirSize %d", got)
	}
	if got, complete := dirSizeBounded(filepath.Join(root, "missing"), time.Minute, 10); got != 0 || !complete {
		t.Fatalf("missing folder: %d %v", got, complete)
	}
}

func TestFirstDiskSampleIsDeferred(t *testing.T) {
	a, _, _ := newTestAgent(t)
	base := time.Now()
	a.now = func() time.Time { return base }
	r := a.rt("later")
	if a.now().Sub(r.diskAt) > diskEvery {
		t.Fatal("disk walk due the moment a service is first seen")
	}
	a.now = func() time.Time { return base.Add(firstDiskDelay + time.Second) }
	if a.now().Sub(r.diskAt) <= diskEvery {
		t.Fatal("first disk walk never becomes due")
	}
}

func TestFileExplorerListingIsBounded(t *testing.T) {
	a, _, _ := newTestAgent(t)
	v, err := a.Create(nodeApp())
	if err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(a.appDir(v.ID), "data")
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxListing+10; i++ {
		if err := os.WriteFile(filepath.Join(data, fmt.Sprintf("f%05d", i)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	w := appRequest(t, a, http.MethodGet, "/v1/apps/"+v.ID+"/files?scope=data", nil)
	var body struct {
		Entries   []projectEntry `json:"entries"`
		Truncated bool           `json:"truncated"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String()[:min(200, w.Body.Len())])
	}
	if len(body.Entries) != maxListing || !body.Truncated {
		t.Fatalf("%d entries, truncated %v", len(body.Entries), body.Truncated)
	}
	// A file path is not a listing.
	w = appRequest(t, a, http.MethodGet, "/v1/apps/"+v.ID+"/files?scope=data&path=f00001", nil)
	if w.Code == 200 {
		t.Fatalf("listing of a file accepted: %s", w.Body.String())
	}
}
