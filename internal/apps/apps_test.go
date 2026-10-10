package apps

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestValidate(t *testing.T) {
	l := Limits{MaxMemoryMB: 1024, MaxCPUs: 1.5, PortFirst: 20000, PortLast: 20099, Runtimes: map[string]string{"node": "node:22"}}
	ok := App{ID: "bot", Name: "Bot", Repo: "git@github.com:ana/bot.git", Mode: ModeScript, Runtime: "node", Start: "npm start",
		MemoryMB: 256, CPUs: 0.5, Ports: []Port{{Container: 3000}}}
	if err := ok.Validate(l); err != nil {
		t.Fatal(err)
	}
	if ok.Repo != "ana/bot" || ok.Ref != "main" || ok.Ports[0].Proto != "tcp" || ok.Exposure != ExposeLAN || ok.Source != SourceGitHub {
		t.Fatalf("not normalized: %+v", ok)
	}
	bad := []func(a *App){
		func(a *App) { a.ID = "Bot!" },
		func(a *App) { a.Repo = "https://gitlab.com/x" },
		func(a *App) { a.Ref = "../main" },
		func(a *App) { a.Mode = "vm" },
		func(a *App) { a.Runtime = "cobol" },
		func(a *App) { a.Start = "" },
		func(a *App) { a.Env = []EnvVar{{Key: "1BAD", Value: "x"}} },
		func(a *App) { a.Ports = []Port{{Container: 3000, Host: 80}} },
		func(a *App) { a.Exposure = ExposePublic }, // no public host
		func(a *App) { a.MemoryMB = 4096 },
		func(a *App) { a.Source, a.Path = SourceLocal, "projetos/bot" }, // local off
	}
	for i, f := range bad {
		a := ok
		a.Ports = slices.Clone(ok.Ports)
		f(&a)
		if a.Validate(l) == nil {
			t.Errorf("case %d accepted: %+v", i, a)
		}
	}
	l.Local = true
	loc := ok
	loc.Source, loc.Path = SourceLocal, "/ana/projetos/bot/"
	if err := loc.Validate(l); err != nil || loc.Path != "ana/projetos/bot" || loc.Repo != "" {
		t.Fatalf("local: %v %+v", err, loc)
	}
	// ".." cannot climb out: paths are cleaned as if rooted.
	loc.Path, loc.Dir = "ana/../../etc", "../src"
	if err := loc.Validate(l); err != nil || loc.Path != "etc" || loc.Dir != "src" {
		t.Fatalf("cleaned paths: %v %q %q", err, loc.Path, loc.Dir)
	}
}

func targz(t *testing.T, entries []tar.Header, bodies map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, h := range entries {
		b := bodies[h.Name]
		h.Size = int64(len(b))
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(b))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestExtractStaysInside(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "rel")
	outside := filepath.Join(filepath.Dir(dir), "pwned")
	entries := []tar.Header{
		{Name: "ana-bot-123/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "ana-bot-123/run.sh", Typeflag: tar.TypeReg, Mode: 0o755},
		{Name: "ana-bot-123/src/a.js", Typeflag: tar.TypeReg, Mode: 0o644},
		{Name: "ana-bot-123/../../pwned", Typeflag: tar.TypeReg, Mode: 0o644},
	}
	if runtime.GOOS != "windows" { // symlinks need privileges on Windows
		entries = append(entries,
			tar.Header{Name: "ana-bot-123/link", Typeflag: tar.TypeSymlink, Linkname: "../.."},
			tar.Header{Name: "ana-bot-123/link/pwned", Typeflag: tar.TypeReg, Mode: 0o644})
	}
	data := targz(t, entries, map[string]string{"ana-bot-123/run.sh": "echo", "ana-bot-123/src/a.js": "x", "ana-bot-123/../../pwned": "no", "ana-bot-123/link/pwned": "no"})
	err := Extract(bytes.NewReader(data), dir)
	if b, rerr := os.ReadFile(filepath.Join(dir, "src", "a.js")); rerr != nil || string(b) != "x" {
		t.Fatalf("a.js: %v %q (extract err %v)", rerr, b, err)
	}
	if _, err := os.Stat(outside); err == nil {
		t.Fatal("an entry escaped the folder")
	}
	if runtime.GOOS != "windows" {
		if err == nil {
			t.Fatal("writing through an escaping symlink must fail")
		}
		if st, _ := os.Stat(filepath.Join(dir, "run.sh")); st.Mode()&0o100 == 0 {
			t.Error("executable bit lost")
		}
	}
}

func TestGitHubFetchRedirects(t *testing.T) {
	data := targz(t, []tar.Header{{Name: "o-r-1/index.html", Typeflag: tar.TypeReg, Mode: 0o644}}, map[string]string{"o-r-1/index.html": "hi"})
	var sawAuth string
	code := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		w.Write(data)
	}))
	defer code.Close()
	sha := strings.Repeat("a", 40)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/o/r/commits/main":
			w.Write([]byte(sha))
		case r.URL.Path == "/repos/o/r/commits":
			w.Write([]byte(`[{"commit":{"message":"fix: login\n\nbody"}}]`))
		case strings.HasPrefix(r.URL.Path, "/repos/o/r/tarball/"):
			http.Redirect(w, r, code.URL+"/x.tgz", http.StatusFound)
		case r.URL.Path == "/repos/o/evil/tarball/"+sha:
			http.Redirect(w, r, "http://169.254.169.254/latest", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()
	g := NewGitHub(func() string { return "tok" })
	g.API = api.URL
	cu, _ := url.Parse(code.URL)
	g.AllowedRedirects = []string{cu.Hostname()}
	got, msg, err := g.Commit(context.Background(), "o/r", "main")
	if err != nil || got != sha || msg != "fix: login" {
		t.Fatalf("commit %q %q %v", got, msg, err)
	}
	dir := filepath.Join(t.TempDir(), "s")
	if err := g.Fetch(context.Background(), "o/r", sha, dir); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "index.html")); string(b) != "hi" {
		t.Fatalf("index.html %q", b)
	}
	if sawAuth != "" {
		// Same host in this test (127.0.0.1), so Go keeps the header; on
		// github.com the redirect goes to another host and drops it.
		t.Log("authorization forwarded to the test host")
	}
	g.AllowedRedirects = []string{"codeload.github.com"}
	if err := g.Fetch(context.Background(), "o/evil", sha, filepath.Join(t.TempDir(), "e")); err == nil {
		t.Fatal("redirect to a foreign host followed")
	}
}

func TestPlanComposeRefusesHostAccess(t *testing.T) {
	root := "/var/lib/sentinela-apps/apps/x"
	cases := map[string]string{
		"privileged":  `{"services":{"a":{"image":"x","privileged":true}}}`,
		"host net":    `{"services":{"a":{"image":"x","network_mode":"host"}}}`,
		"pid host":    `{"services":{"a":{"image":"x","pid":"host"}}}`,
		"socket":      `{"services":{"a":{"image":"x","volumes":[{"type":"bind","source":"/var/run/docker.sock","target":"/s"}]}}}`,
		"etc":         `{"services":{"a":{"image":"x","volumes":[{"type":"bind","source":"/etc","target":"/e"}]}}}`,
		"cap":         `{"services":{"a":{"image":"x","cap_add":["SYS_ADMIN"]}}}`,
		"devices":     `{"services":{"a":{"image":"x","devices":[{"source":"/dev/sda","target":"/dev/sda"}]}}}`,
		"ext net":     `{"services":{"a":{"image":"x"}},"networks":{"lan":{"external":true}}}`,
		"macvlan":     `{"services":{"a":{"image":"x"}},"networks":{"lan":{"driver":"macvlan"}}}`,
		"vol opts":    `{"services":{"a":{"image":"x"}},"volumes":{"d":{"driver_opts":{"type":"none","o":"bind","device":"/"}}}}`,
		"unconfined":  `{"services":{"a":{"image":"x","security_opt":["seccomp:unconfined"]}}}`,
		"build host":  `{"services":{"a":{"build":{"context":"/var/lib/sentinela-apps/apps/x","network":"host"}}}}`,
		"port range":  `{"services":{"a":{"image":"x","ports":[{"target":80,"published":"8000-8010"}]}}}`,
		"no services": `{"services":{}}`,
	}
	for name, raw := range cases {
		if _, err := planCompose([]byte(raw), []string{root}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestPlanComposeRewrites(t *testing.T) {
	root := "/var/lib/sentinela-apps/apps/x"
	raw := `{"name":"whatever","services":{
	  "web":{"image":"nginx","ports":[{"target":80,"published":"8080","protocol":"tcp","host_ip":"0.0.0.0","mode":"ingress"},{"target":9000}],
	         "volumes":[{"type":"bind","source":"` + root + `/releases/r1/conf","target":"/etc/nginx/conf.d"},{"type":"volume","source":"data","target":"/d"}],
	         "deploy":{"resources":{"limits":{"memory":"4g"}}},"labels":["a=b"],"restart":"always"},
	  "db":{"image":"postgres","cap_add":["CHOWN"],"networks":{"default":null,"back":null}}},
	 "networks":{"back":{}},"volumes":{"data":{}}}`
	p, err := planCompose([]byte(raw), []string{root})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.ports) != 1 || p.ports[0].port != (Port{Service: "web", Container: 80, Proto: "tcp"}) {
		t.Fatalf("ports %+v", p.ports)
	}
	out, err := p.apply([]Port{{Service: "web", Container: 80, Proto: "tcp", Host: 20003}}, composeLimits{ID: "x", MemoryMB: 256, CPUs: 0.5, BindIP: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Name     string
		Services map[string]map[string]any
		Networks map[string]map[string]any
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	web := doc.Services["web"]
	port := web["ports"].([]any)[0].(map[string]any)
	if doc.Name != "sapp-x" || port["published"] != "20003" || port["host_ip"] != "127.0.0.1" || len(web["ports"].([]any)) != 1 {
		t.Fatalf("ports not rewritten: %v %v", doc.Name, web["ports"])
	}
	if web["mem_limit"] != "256m" || web["restart"] != "no" || web["cpu_shares"] != float64(256) {
		t.Fatalf("limits not applied: %v", web)
	}
	if _, ok := web["deploy"].(map[string]any)["resources"]; ok {
		t.Fatal("deploy.resources kept (would conflict with mem_limit)")
	}
	if l := web["labels"].(map[string]any); l["a"] != "b" || l[labelApp] != "x" {
		t.Fatalf("labels %v", l)
	}
	if !slices.Contains(strList(web["security_opt"]), "no-new-privileges:true") {
		t.Fatal("no-new-privileges missing")
	}
	for name, n := range doc.Networks {
		opts := n["driver_opts"].(map[string]any)
		br, _ := opts["com.docker.network.bridge.name"].(string)
		if !strings.HasPrefix(br, "sn-") || len(br) > 15 {
			t.Fatalf("network %s bridge %q", name, br)
		}
	}
	if _, ok := doc.Networks["default"]; !ok {
		t.Fatal("default network not pinned to a sn- bridge")
	}
}

func TestDetect(t *testing.T) {
	cases := []struct {
		files   map[string]string
		mode    string
		runtime string
		setup   string
		start   string
	}{
		{map[string]string{"compose.yaml": "services:\n  web:\n    image: nginx\n    ports: [\"8080:80\"]\n    privileged: true\n", "Dockerfile": "FROM x"}, ModeCompose, "", "", ""},
		{map[string]string{"Dockerfile": "FROM node\nEXPOSE 5000\n"}, ModeDockerfile, "", "", ""},
		{map[string]string{"package.json": `{"scripts":{"build":"vite build","start":"node s"}}`, "package-lock.json": "{}"}, ModeScript, "node", "npm ci && npm run build", "npm start"},
		{map[string]string{"requirements.txt": "fastapi\nuvicorn\n", "main.py": ""}, ModeScript, "python", "", ".venv/bin/uvicorn main:app --host 0.0.0.0 --port $PORT"},
		{map[string]string{"go.mod": "module x"}, ModeScript, "go", "go build -o .bin/app .", "./.bin/app"},
		{map[string]string{"index.html": "<h1>"}, ModeScript, "static", "", ""},
		{map[string]string{"README.md": "?"}, ModeScript, "shell", "", ""},
	}
	for i, c := range cases {
		dir := t.TempDir()
		for n, b := range c.files {
			os.WriteFile(filepath.Join(dir, n), []byte(b), 0o644)
		}
		d := Detect(dir)
		if d.Mode != c.mode || d.Runtime != c.runtime || c.setup != "" && d.Setup != c.setup || c.start != "" && d.Start != c.start {
			t.Errorf("case %d: %+v", i, d)
		}
		if i == 0 && (len(d.Services) != 1 || d.Services[0].Ports[0] != 80 || len(d.Notes) == 0) {
			t.Errorf("compose summary: %+v", d)
		}
		if i == 1 && d.Port != 5000 {
			t.Errorf("EXPOSE: %d", d.Port)
		}
	}
}

func TestDeployScriptService(t *testing.T) {
	a, eng, _ := newTestAgent(t)
	v, err := a.Create(nodeApp())
	if err != nil {
		t.Fatal(err)
	}
	if v.ID != "meu-bot" || v.Ports[0].Host != 20000 || v.Env[0].Value != "" {
		t.Fatalf("created %+v", v.App)
	}
	if err := a.Enqueue(v.ID, "deploy"); err != nil {
		t.Fatal(err)
	}
	if err := a.Enqueue(v.ID, "deploy"); !errors.Is(err, errBusy) {
		t.Fatalf("second deploy: %v", err)
	}
	runQueued(t, a)
	got, _ := a.viewOf(v.ID)
	if got.Deployed == nil || got.Desired != Running || got.Status.State != "running" || got.Status.Job.Phase != "done" {
		t.Fatalf("after deploy: %+v %+v", got.App, got.Status)
	}
	if eng.called("pull node:22-bookworm-slim") != 1 || eng.called("create sapp-meu-bot-setup") != 1 || eng.called("rm sapp-meu-bot-setup") == 0 {
		t.Fatalf("calls %v", eng.calls)
	}
	c := eng.containers["sapp-meu-bot"]
	s := c.spec
	if s.User != "1000:1000" || s.Memory != 256<<20 || s.CPUShares != 256 || s.Network != networkName ||
		s.Ports["3000/tcp"] != (HostAddr{IP: "0.0.0.0", Port: 20000}) || !slices.Contains(s.Env, "TOKEN=s3cret") || !slices.Contains(s.Env, "PORT=3000") {
		t.Fatalf("spec %+v", s)
	}
	if !strings.HasSuffix(filepath.ToSlash(strings.Split(s.Binds[0], ":/app")[0]), "/apps/meu-bot/releases/"+got.Deployed.Release) {
		t.Fatalf("binds %v", s.Binds)
	}
	// A second deploy keeps only the new release.
	time.Sleep(1100 * time.Millisecond) // release names carry the second
	a.Enqueue(v.ID, "deploy")
	runQueued(t, a)
	entries, _ := os.ReadDir(filepath.Join(a.appDir(v.ID), "releases"))
	if len(entries) != 1 {
		t.Fatalf("releases left: %d", len(entries))
	}
	// Links: LAN and tailnet (not public), plus the internal name.
	kinds := []string{}
	for _, l := range got.Links {
		kinds = append(kinds, l.Kind)
	}
	if !slices.Equal(kinds, []string{"lan", "tailnet", "internal"}) || got.Links[0].URL != "http://192.168.1.10:20000" {
		t.Fatalf("links %+v", got.Links)
	}
}

func TestFailedSetupKeepsOldVersion(t *testing.T) {
	a, eng, src := newTestAgent(t)
	v, _ := a.Create(nodeApp())
	a.Enqueue(v.ID, "deploy")
	runQueued(t, a)
	first, _ := a.viewOf(v.ID)
	eng.setupExit = 1
	src.sha = "fedcba9876543210fedcba9876543210fedcba98"
	time.Sleep(1100 * time.Millisecond)
	a.Enqueue(v.ID, "deploy")
	runQueued(t, a)
	now, _ := a.viewOf(v.ID)
	if now.Deployed.Version != first.Deployed.Version || now.Status.Job.Phase != "failed" || !strings.Contains(now.Status.Job.Error, "código 1") {
		t.Fatalf("after failed deploy: %+v / %+v", now.Deployed, now.Status.Job)
	}
	entries, _ := os.ReadDir(filepath.Join(a.appDir(v.ID), "releases"))
	if len(entries) != 1 || entries[0].Name() != first.Deployed.Release {
		t.Fatalf("releases %v", entries)
	}
}

func TestRebootRule(t *testing.T) {
	a, _, _ := newTestAgent(t)
	keep := nodeApp()
	keep.Name, keep.Autostart = "Keep", true
	drop := nodeApp()
	drop.Name = "Drop"
	for _, in := range []App{keep, drop} {
		v, _ := a.Create(in)
		a.Enqueue(v.ID, "deploy")
		runQueued(t, a)
	}
	// Same state file, new boot, host up for 30 s (delay 120 s).
	b := NewAgent(a.Cfg, newFakeEngine(), a.Sources, quiet)
	b.bootID = func() string { return "boot-2" }
	b.uptime = func() time.Duration { return 30 * time.Second }
	b.memFreeMB = func() int { return 2000 }
	if err := b.Load(); err != nil {
		t.Fatal(err)
	}
	if b.find("keep").Desired != Running || b.find("drop").Desired != Stopped {
		t.Fatalf("keep=%s drop=%s", b.find("keep").Desired, b.find("drop").Desired)
	}
	if d := b.startAfter.Sub(b.now()); d < 80*time.Second || d > 91*time.Second {
		t.Fatalf("boot delay %s", d)
	}
	b.Reconcile(context.Background())
	if v, _ := b.viewOf("keep"); v.Status.State != "waiting" {
		t.Fatalf("state during the boot delay: %s", v.Status.State)
	}
}

func TestCrashLoopPauses(t *testing.T) {
	a, eng, _ := newTestAgent(t)
	v, _ := a.Create(nodeApp())
	a.Enqueue(v.ID, "deploy")
	runQueued(t, a)
	eng.crash = true
	eng.containers["sapp-meu-bot"].running = false
	for i := 0; i < crashLimit+3; i++ {
		a.Reconcile(context.Background())
	}
	got, _ := a.viewOf(v.ID)
	// One start at deploy, then crashLimit restarts before the pause.
	if got.Status.State != "crashloop" || eng.exact("start sapp-meu-bot") != crashLimit+1 {
		t.Fatalf("state %s, starts %d", got.Status.State, eng.exact("start sapp-meu-bot"))
	}
	// Starting it by hand clears the pause.
	eng.crash = false
	if err := a.SetDesired(context.Background(), v.ID, Running); err != nil {
		t.Fatal(err)
	}
	if got, _ := a.viewOf(v.ID); got.Status.State != "running" {
		t.Fatalf("after manual start: %s", got.Status.State)
	}
}

func TestUpdateKeepsSecretsAndRecreates(t *testing.T) {
	a, eng, _ := newTestAgent(t)
	v, _ := a.Create(nodeApp())
	a.Enqueue(v.ID, "deploy")
	runQueued(t, a)
	in := v.App
	in.Env = []EnvVar{{Key: "TOKEN", Secret: true}, {Key: "MODE", Value: "prod"}}
	got, err := a.Update(v.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if a.find(v.ID).Env[0].Value != "s3cret" || got.NeedsDeploy {
		t.Fatalf("secret lost or deploy demanded: %+v", a.find(v.ID).Env)
	}
	creates := eng.called("create sapp-meu-bot")
	runQueued(t, a)
	if eng.called("create sapp-meu-bot") != creates+1 || !slices.Contains(eng.containers["sapp-meu-bot"].spec.Env, "MODE=prod") {
		t.Fatal("env change not applied to the container")
	}
	in.Ref = "dev"
	got, _ = a.Update(v.ID, in)
	if !got.NeedsDeploy {
		t.Fatal("branch change must ask for a deploy")
	}
}

func TestPortsAreUniqueAndKept(t *testing.T) {
	a, _, _ := newTestAgent(t)
	busy := map[int]bool{20001: true}
	a.portFree = func(p int, _ string) bool { return !busy[p] }
	one, _ := a.Create(nodeApp())
	two := nodeApp()
	two.Name = "Two"
	two.Ports = []Port{{Container: 3000}, {Container: 9229}}
	v2, err := a.Create(two)
	if err != nil {
		t.Fatal(err)
	}
	if one.Ports[0].Host != 20000 || v2.Ports[0].Host != 20002 || v2.Ports[1].Host != 20003 {
		t.Fatalf("ports %v %v", one.Ports, v2.Ports)
	}
	in := v2.App
	in.Ports = []Port{{Container: 9229}, {Container: 8080}}
	got, _ := a.Update(v2.ID, in)
	if got.Ports[0].Host != 20003 || got.Ports[1].Host != 20002 {
		t.Fatalf("kept ports %v", got.Ports)
	}
	in.Ports = []Port{{Container: 1, Host: 20000}}
	if _, err := a.Update(v2.ID, in); err == nil {
		t.Fatal("took another service's port")
	}
}

func TestComposeDeploy(t *testing.T) {
	a, eng, src := newTestAgent(t)
	src.files = map[string]string{"compose.yaml": "services: {}"}
	eng.composeConfig = `{"services":{"web":{"image":"nginx","ports":[{"target":80,"published":"8080","protocol":"tcp"}]},"db":{"image":"postgres"}}}`
	eng.members = []Summary{{Name: "sapp-site-web-1", Service: "web", Running: true}, {Name: "sapp-site-db-1", Service: "db", Running: true}}
	in := App{Name: "Site", Source: SourceGitHub, Repo: "ana/site", Mode: ModeCompose, Exposure: ExposePublic}
	v, err := a.Create(in)
	if err != nil {
		t.Fatal(err)
	}
	a.Enqueue(v.ID, "deploy")
	runQueued(t, a)
	got, _ := a.viewOf(v.ID)
	if got.Status.Job.Phase != "done" || len(got.Ports) != 1 || got.Ports[0].Host != 20000 || got.Ports[0].Service != "web" {
		t.Fatalf("compose deploy: %+v %+v", got.Status.Job, got.Ports)
	}
	if !slices.Equal(got.Deployed.Services, []string{"db", "web"}) || got.Status.State != "running" || got.Status.Running != 2 {
		t.Fatalf("services %v state %s", got.Deployed.Services, got.Status.State)
	}
	rendered, err := os.ReadFile(filepath.Join(a.workDir(&got.App, got.Deployed), renderedCompose))
	if err != nil || !strings.Contains(string(rendered), `"published": "20000"`) {
		t.Fatalf("rendered: %v %s", err, rendered)
	}
	if eng.called("create sapp-site-compose") != 2 { // config, then up
		t.Fatalf("compose runs %v", eng.calls)
	}
	// Internet-exposed ports reach the firewall list.
	a.Reconcile(context.Background())
	if b, _ := os.ReadFile(filepath.Join(a.Cfg.StateDir, "public-ports")); strings.TrimSpace(string(b)) != "20000/tcp" {
		t.Fatalf("public-ports %q", b)
	}
	// A crashed member is started again; stop stops every member.
	eng.members[1].Running = false
	a.Reconcile(context.Background())
	if eng.called("start sapp-site-db-1") != 1 {
		t.Fatal("crashed compose member not restarted")
	}
	if err := a.SetDesired(context.Background(), v.ID, Stopped); err != nil {
		t.Fatal(err)
	}
	if eng.called("stop sapp-site-web-1") != 1 {
		t.Fatal("stop did not reach the members")
	}
}

func TestAgentHandlerAuth(t *testing.T) {
	a, _, _ := newTestAgent(t)
	h := a.Handler("secret-token")
	for _, c := range []struct {
		remote, auth string
		want         int
	}{
		{"127.0.0.1:5000", "Bearer secret-token", 200},
		{"127.0.0.1:5000", "Bearer wrong", 403},
		{"127.0.0.1:5000", "", 403},
		{"192.168.1.20:5000", "Bearer secret-token", 403},
	} {
		req := httptest.NewRequest("GET", "/v1/apps", nil)
		req.RemoteAddr = c.remote
		if c.auth != "" {
			req.Header.Set("Authorization", c.auth)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != c.want {
			t.Errorf("%s %q: %d, want %d", c.remote, c.auth, rec.Code, c.want)
		}
	}
}

func TestLocalSourceReadsGit(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "ana", "bot")
	sha := "0123456789abcdef0123456789abcdef01234567"
	write := func(p, body string) {
		full := filepath.Join(proj, filepath.FromSlash(p))
		os.MkdirAll(filepath.Dir(full), 0o755)
		os.WriteFile(full, []byte(body), 0o644)
	}
	write(".git/HEAD", "ref: refs/heads/main\n")
	write(".git/refs/heads/main", sha+"\n")
	var obj bytes.Buffer
	z := zlib.NewWriter(&obj)
	z.Write([]byte("commit 60\x00tree abc\nauthor a <a@b> 0 +0000\n\nAdd the bot\n\nDetails.\n"))
	z.Close()
	write(".git/objects/01/"+sha[2:], obj.String())
	write("package.json", `{}`)
	write("node_modules/x/index.js", "skip me")
	src := LocalSource{Root: root}
	app := &App{Path: "ana/bot"}
	v, err := src.Resolve(context.Background(), app)
	if err != nil || v.Commit != sha || v.Branch != "main" || v.Message != "Add the bot" || v.Label != sha[:12] {
		t.Fatalf("version %+v %v", v, err)
	}
	out := filepath.Join(t.TempDir(), "snap")
	if err := src.Snapshot(context.Background(), app, v, out); err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]bool{"package.json": true, ".git": false, "node_modules": false} {
		if _, err := os.Stat(filepath.Join(out, p)); (err == nil) != want {
			t.Errorf("%s copied=%v", p, err == nil)
		}
	}
	folders, err := src.Browse("ana")
	if err != nil || len(folders) != 1 || !folders[0].Git || !slices.Contains(folders[0].Markers, "node") {
		t.Fatalf("browse %+v %v", folders, err)
	}
	if top, err := src.Browse("../.."); err != nil || len(top) != 1 || top[0].Name != "ana" {
		t.Fatalf("browse of ../.. must stay at the root: %+v %v", top, err)
	}
	if _, err := (LocalSource{Root: root}).Resolve(context.Background(), &App{Path: "ana/../../x"}); err == nil {
		t.Fatal("resolve escaped the root")
	}
}

func TestStopAnswersStopped(t *testing.T) {
	a, _, _ := newTestAgent(t)
	v, _ := a.Create(nodeApp())
	a.Enqueue(v.ID, "deploy")
	runQueued(t, a)
	if err := a.SetDesired(context.Background(), v.ID, Stopped); err != nil {
		t.Fatal(err)
	}
	if got, _ := a.viewOf(v.ID); got.Status.State != "stopped" {
		t.Fatalf("right after stop: %s", got.Status.State)
	}
}
