package apps

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// planOne runs planCompose on one service (and optional top-level JSON) of
// service "x" whose folder is root.
func planOne(t *testing.T, root, service, top string) (*composePlan, error) {
	t.Helper()
	raw := `{"services":{"a":` + service + `,"db":{"image":"postgres"}}` + top + `}`
	return planCompose([]byte(raw), testRules(root))
}

// Every attribute is checked against an allow-list: one that is not known
// to stay inside the container is refused, even if Compose added it after
// this code was written.
func TestPlanComposeAllowList(t *testing.T) {
	root := "/var/lib/sentinela-apps/apps/x"
	rel := root + "/releases/r1"
	cases := []struct {
		name, service, top string
		refused            string // "" = accepted
	}{
		{"api socket", `{"image":"x","use_api_socket":true}`, "", "use_api_socket"},
		{"provider service", `{"provider":{"type":"sh","options":{"c":"id"}}}`, "", "provider"},
		{"privileged hook", `{"image":"x","post_start":[{"command":["id"],"privileged":true}]}`, "", "post_start privileged"},
		{"plain hook", `{"image":"x","post_start":[{"command":["id"],"user":"root"}],"pre_stop":[{"command":["sh","-c","exit 0"]}]}`, "", ""},
		{"seccomp profile", `{"image":"x","security_opt":["seccomp=./allow.json"]}`, "", "security_opt"},
		{"apparmor profile", `{"image":"x","security_opt":["apparmor=other"]}`, "", "security_opt"},
		{"no new privileges", `{"image":"x","security_opt":["no-new-privileges:true"]}`, "", ""},
		{"annotations", `{"image":"x","annotations":{"run.oci.keep_original_groups":"1"}}`, "", "annotations"},
		{"unknown key", `{"image":"x","frobnicate":{"a":1}}`, "", "frobnicate"},
		{"unknown key left empty", `{"image":"x","frobnicate":null,"pid":"","privileged":false,"devices":[]}`, "", ""},
		{"real-time ulimit", `{"image":"x","ulimits":{"rtprio":99}}`, "", "ulimit rtprio"},
		{"nofile ulimit", `{"image":"x","ulimits":{"nofile":{"soft":65536,"hard":65536}}}`, "", ""},
		{"project label", `{"image":"x","labels":{"com.docker.compose.project":"sapp-other"}}`, "", "reserved"},
		{"agent label", `{"image":"x","labels":["sentinela.app=other"]}`, "", "reserved"},
		{"default bridge", `{"image":"x","network_mode":"bridge"}`, "", "network_mode"},
		{"other container", `{"image":"x","network_mode":"container:sentinela"}`, "", "network_mode"},
		{"unknown service", `{"image":"x","network_mode":"service:nope"}`, "", "network_mode"},
		{"project service", `{"image":"x","network_mode":"service:db"}`, "", ""},
		{"oom kill disabled", `{"image":"x","oom_kill_disable":true}`, "", "oom_kill_disable"},
		{"volume driver", `{"image":"x","volume_driver":"some-plugin"}`, "", "volume_driver"},
		{"storage opt", `{"image":"x","storage_opt":{"size":"1T"}}`, "", "storage_opt"},
		{"rt scheduler", `{"image":"x","cpu_rt_runtime":950000}`, "", "cpu_rt_runtime"},
		{"blkio weight", `{"image":"x","blkio_config":{"weight":1000}}`, "", "blkio_config"},
		{"external link", `{"image":"x","external_links":["sentinela"]}`, "", "external_links"},
		{"swarm job", `{"image":"x","deploy":{"mode":"replicated-job"}}`, "", "deploy.mode"},
		{"deploy extra", `{"image":"x","deploy":{"resources":{"limits":{"memory":"4g"}},"labels":{"a":"b"},"x-n":1}}`, "", ""},
		{"bind propagation", `{"image":"x","volumes":[{"type":"bind","source":"` + rel + `/m","target":"/m","bind":{"propagation":"rshared"}}]}`, "", "propagation"},
		{"image volume", `{"image":"x","volumes":[{"type":"image","source":"sentinela:local","target":"/s"}]}`, "", "type"},
		{"build ssh", `{"build":{"context":"` + rel + `","ssh":["default"]}}`, "", "build ssh"},
		{"build extra context", `{"build":{"context":"` + rel + `","additional_contexts":{"s":"` + root + `"}}}`, "", "additional_contexts"},
		{"build cache export", `{"build":{"context":"` + rel + `","cache_to":["type=local,dest=` + root + `"]}}`, "", "cache_to"},
		{"remote context", `{"build":{"context":"https://github.com/ana/x.git"}}`, "", "build context"},
		{"context outside", `{"build":{"context":"` + root + `"}}`, "", "build context"},
		{"dockerfile outside", `{"build":{"context":"` + rel + `","dockerfile":"../../secrets.enc"}}`, "", "Dockerfile"},
		{"build network", `{"build":{"context":"` + rel + `","network":"sentinela-apps"}}`, "", "build network"},
		{"build ok", `{"build":{"context":"` + rel + `/src","dockerfile":"Dockerfile","args":{"A":"1"},"network":"none"}}`, "", ""},
		{"env file outside", `{"image":"x","env_file":[{"path":"` + root + `/data/x.env","required":true}]}`, "", "env file"},
		{"extension", `{"image":"x","x-anything":{"privileged":true}}`, "", ""},
		{"top-level include", `{"image":"x"}`, `,"include":[{"path":"other.yaml"}]`, "include"},
		{"top-level models", `{"image":"x"}`, `,"models":{"m":{"model":"ai/x"}}`, "models"},
		{"secret driver", `{"image":"x"}`, `,"secrets":{"s":{"driver":"vault"}}`, "driver"},
		{"secret from env", `{"image":"x"}`, `,"secrets":{"s":{"environment":"TOKEN"}}`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := planOne(t, root, c.service, c.top)
			switch {
			case c.refused == "" && err != nil:
				t.Fatalf("refused: %v", err)
			case c.refused != "" && err == nil:
				t.Fatal("accepted")
			case c.refused != "" && !strings.Contains(err.Error(), c.refused):
				t.Fatalf("error %q does not mention %q", err, c.refused)
			}
		})
	}
}

// apply leaves only what was checked: empty unknown attributes, Compose
// watch settings, profiles and container_name do not reach the rendered file.
func TestComposeApplyDropsWhatItDoesNotRun(t *testing.T) {
	root := "/var/lib/sentinela-apps/apps/x"
	p, err := planOne(t, root, `{"image":"x","container_name":"myapp","profiles":["web"],"develop":{"watch":[{"path":"./src","action":"sync","target":"/src"}]},
		"cpu_quota":50000,"cpu_period":100000,"frobnicate":null,"networks":{"default":null,"back":{"aliases":["a1"]}}}`, `,"networks":{"back":{}}`)
	if err != nil {
		t.Fatal(err)
	}
	out, err := p.apply(nil, composeLimits{ID: "x", MemoryMB: 128, CPUs: 0.5, BindIP: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	var doc struct{ Services map[string]map[string]any }
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	s := doc.Services["a"]
	for _, k := range []string{"container_name", "profiles", "develop", "cpu_quota", "cpu_period", "frobnicate"} {
		if _, ok := s[k]; ok {
			t.Errorf("%s reached the rendered file", k)
		}
	}
	// The container_name still resolves, as an alias on the project's own
	// networks only.
	nets := s["networks"].(map[string]any)
	for _, n := range []string{"default", "back"} {
		if !slices.Contains(strList(nets[n].(map[string]any)["aliases"]), "myapp") {
			t.Errorf("network %s: alias missing: %v", n, nets[n])
		}
	}
}

// Binds come only from the release being run or the data folder, never from
// the service folder itself, where the agent keeps its own files.
func TestPlanComposeBindRoots(t *testing.T) {
	root := "/var/lib/sentinela-apps/apps/x"
	rel := root + "/releases/r1"
	bind := func(src, opts string) string {
		return `{"image":"x","volumes":[{"type":"bind","source":"` + src + `","target":"/m"` + opts + `}]}`
	}
	cases := []struct {
		name, service, top string
		ok                 bool
	}{
		{"service folder", bind(root, ""), "", false},
		{"service folder via ..", bind(rel+"/../..", ""), "", false},
		{"all releases", bind(root+"/releases", ""), "", false},
		{"another release", bind(root+"/releases/r0", ""), "", false},
		{"protected project", bind(root+"/project", ""), "", false},
		{"agent file", bind(root+"/secrets.enc", ""), "", false},
		{"cache", bind(root+"/cache", ""), "", false},
		{"release", bind(rel, ""), "", true},
		{"release file", bind(rel+"/nginx.conf", `,"read_only":true`), "", true},
		{"data", bind(root+"/data", ""), "", true},
		{"data folder", bind(root+"/data/db", ""), "", true},
		{"secret file in the service folder", `{"image":"x"}`, `,"secrets":{"s":{"file":"` + root + `/secrets.enc"}}`, false},
		{"secret file in the release", `{"image":"x"}`, `,"secrets":{"s":{"file":"` + rel + `/s.txt"}}`, true},
		// A writable bind may not contain another host path: a container could
		// swap the inner one for a link before the daemon mounts it.
		{"writable release and a file in it", `{"image":"x","volumes":[{"type":"bind","source":"` + rel + `","target":"/app"},{"type":"bind","source":"` + rel + `/conf","target":"/c"}]}`, "", false},
		{"writable release and a secret in it", bind(rel, ""), `,"secrets":{"s":{"file":"` + rel + `/s.txt"}}`, false},
		{"read-only release and a file in it", `{"image":"x","volumes":[{"type":"bind","source":"` + rel + `","target":"/app","read_only":true},{"type":"bind","source":"` + rel + `/conf","target":"/c"}]}`, "", true},
		{"side by side", `{"image":"x","volumes":[{"type":"bind","source":"` + rel + `/a","target":"/a"},{"type":"bind","source":"` + rel + `/b","target":"/b"},{"type":"bind","source":"` + rel + `/a","target":"/a2"}]}`, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := planOne(t, root, c.service, c.top)
			if (err == nil) != c.ok {
				t.Fatalf("ok=%v, err=%v", c.ok, err)
			}
		})
	}
	// Containers still running from the previous `up` count too.
	r := testRules(root)
	r.Running = []hostBind{{Path: root + "/data", RW: true}}
	if _, err := planCompose([]byte(`{"services":{"a":`+bind(root+"/data/db", "")+`}}`), r); err == nil || !strings.Contains(err.Error(), "stop the service") {
		t.Errorf("bind inside a folder a running container writes: %v", err)
	}
	r.Running = []hostBind{{Path: root + "/data", RW: false}}
	if _, err := planCompose([]byte(`{"services":{"a":`+bind(root+"/data/db", "")+`}}`), r); err != nil {
		t.Errorf("read-only running bind: %v", err)
	}
}

// A project that binds the service folder never reaches `up`: the agent's
// files and releases stay out of every container.
func TestComposeDeployRefusesTheServiceFolder(t *testing.T) {
	a, eng, src := newTestAgent(t)
	got := deployCompose(t, a, eng, src, App{Name: "Site"}, func(string) string {
		return `{"services":{"web":{"image":"nginx","volumes":[{"type":"bind","source":"@WORK@/../..","target":"/state"}]}}}`
	})
	if got.Status.Job.Phase != "failed" || !strings.Contains(got.Status.Job.Error, "outside the project") {
		t.Fatalf("deploy: %+v", got.Status.Job)
	}
	if n := len(helperSpecs(eng, got.ID)); n != 1 {
		t.Fatalf("helper runs: %d (up must not run)", n)
	}
}

func TestPlanComposeNetworks(t *testing.T) {
	root := "/var/lib/sentinela-apps/apps/x"
	cases := []struct {
		name, service, top string
		ok                 bool
	}{
		{"normalized default", `{"image":"x"}`, `,"networks":{"default":{"name":"sapp-x_default","ipam":{},"external":false}}`, true},
		{"ipv6", `{"image":"x"}`, `,"networks":{"default":{"enable_ipv6":true}}`, false},
		{"ipv6 off", `{"image":"x"}`, `,"networks":{"default":{"enable_ipv6":false}}`, true},
		{"no ipv4", `{"image":"x"}`, `,"networks":{"default":{"enable_ipv4":false}}`, false},
		{"chosen subnet", `{"image":"x"}`, `,"networks":{"default":{"ipam":{"config":[{"subnet":"192.168.1.0/28"}]}}}`, false},
		{"ipam driver", `{"image":"x"}`, `,"networks":{"default":{"ipam":{"driver":"other"}}}`, false},
		{"bridge option", `{"image":"x"}`, `,"networks":{"default":{"driver_opts":{"com.docker.network.bridge.gateway_mode_ipv4":"routed"}}}`, false},
		{"own bridge name", `{"image":"x"}`, `,"networks":{"default":{"driver_opts":{"com.docker.network.bridge.name":"docker0"}}}`, false},
		{"mtu", `{"image":"x"}`, `,"networks":{"default":{"driver_opts":{"com.docker.network.driver.mtu":"1400"}}}`, true},
		{"internal", `{"image":"x"}`, `,"networks":{"default":{"internal":true}}`, true},
		{"static address", `{"image":"x","networks":{"default":{"ipv4_address":"172.31.250.5"}}}`, "", false},
		{"ipv6 address", `{"image":"x","networks":{"default":{"ipv6_address":"fd00::5"}}}`, "", false},
		{"mac address", `{"image":"x","mac_address":"02:42:ac:11:00:02"}`, "", false},
		{"aliases", `{"image":"x","networks":{"default":{"aliases":["web"],"priority":10}}}`, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := planOne(t, root, c.service, c.top)
			if (err == nil) != c.ok {
				t.Fatalf("ok=%v, err=%v", c.ok, err)
			}
		})
	}
	// Every network is created IPv4-only, whatever the daemon's defaults.
	p, err := planOne(t, root, `{"image":"x","networks":{"default":null,"back":null}}`, `,"networks":{"back":{}}`)
	if err != nil {
		t.Fatal(err)
	}
	out, err := p.apply(nil, composeLimits{ID: "x", MemoryMB: 128, CPUs: 0.5})
	if err != nil {
		t.Fatal(err)
	}
	var doc struct{ Networks map[string]map[string]any }
	_ = json.Unmarshal(out, &doc)
	for _, n := range []string{"default", "back"} {
		if v, ok := doc.Networks[n]["enable_ipv6"]; !ok || v != false {
			t.Errorf("network %s: enable_ipv6 = %v", n, v)
		}
	}
}

// Volumes and networks are this project's: a name of another service's
// object (or a predefined network) is refused.
func TestPlanComposeForeignNames(t *testing.T) {
	root := "/var/lib/sentinela-apps/apps/x"
	cases := []struct {
		name, top string
		ok        bool
	}{
		{"other service's volume", `,"volumes":{"db":{"name":"sapp-other_db"}}`, false},
		{"other service's network", `,"networks":{"default":{"name":"sapp-other_default"}}`, false},
		{"prefix of another id", `,"volumes":{"db":{"name":"sapp-x-y_db"}}`, false},
		{"host network", `,"networks":{"n":{"name":"host"}}`, false},
		{"default bridge", `,"networks":{"n":{"name":"bridge"}}`, false},
		{"NVR network", `,"networks":{"n":{"name":"deploy_default"}}`, false},
		{"own volume", `,"volumes":{"db":{"name":"sapp-x_db"}}`, true},
		{"own renamed volume", `,"volumes":{"db":{"name":"sapp-x_pgdata"}}`, true},
		{"external volume", `,"volumes":{"db":{"external":true,"name":"sapp-x_db"}}`, false},
		{"unnamed", `,"volumes":{"db":{}},"networks":{"back":{}}`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := planOne(t, root, `{"image":"x"}`, c.top)
			if (err == nil) != c.ok {
				t.Fatalf("ok=%v, err=%v", c.ok, err)
			}
		})
	}
}

func TestPlanComposeRefusesReplicas(t *testing.T) {
	root := "/var/lib/sentinela-apps/apps/x"
	for svc, ok := range map[string]bool{
		`{"image":"x","deploy":{"replicas":4}}`:                     false,
		`{"image":"x","scale":3}`:                                   false,
		`{"image":"x","deploy":{"replicas":1,"mode":"replicated"}}`: true,
		`{"image":"x","scale":1}`:                                   true,
		`{"image":"x","deploy":{"replicas":0}}`:                     true,
	} {
		if _, err := planOne(t, root, svc, ""); (err == nil) != ok {
			t.Errorf("%s: ok=%v, err=%v", svc, ok, err)
		}
	}
}

// The memory budget counts the containers of the plan being started, also
// on a first deploy (when there is no previous deployment to count).
func TestComposeBudgetCountsThePlan(t *testing.T) {
	services := func(n int) string {
		var parts []string
		for i := range n {
			parts = append(parts, `"s`+string(rune('a'+i))+`":{"image":"x"}`)
		}
		return `{"services":{` + strings.Join(parts, ",") + `}}`
	}
	for _, c := range []struct {
		n  int
		ok bool
	}{{4, true}, {5, false}} { // 256 MB each, 1024 MB in total
		a, eng, src := newTestAgent(t)
		got := deployCompose(t, a, eng, src, App{Name: "Site"}, func(string) string { return services(c.n) })
		if done := got.Status.Job.Phase == "done"; done != c.ok {
			t.Fatalf("%d services: %+v", c.n, got.Status.Job)
		}
		if !c.ok && !strings.Contains(got.Status.Job.Error, "total limit") {
			t.Fatalf("%d services: %s", c.n, got.Status.Job.Error)
		}
	}
}

// `up` runs with the agent's own folder as the Compose project directory and
// an empty env file of its own, so no `.env` a service writes into its
// release decides a value; manager variables reach Compose under names the
// helper never withholds.
func TestComposeUpIgnoresProjectDotEnv(t *testing.T) {
	a, eng, src := newTestAgent(t)
	var appDir string
	got := deployCompose(t, a, eng, src, App{Name: "Site", Env: []EnvVar{{Key: "XDG_Q", Value: "/data/zz", Secret: true}}}, func(id string) string {
		appDir = filepath.ToSlash(a.appDir(id))
		return `{"services":{"web":{"image":"nginx","volumes":[{"type":"bind","source":"` + appDir + `/data/zz","target":"/z"}]}}}`
	})
	if got.Status.Job.Phase != "done" {
		t.Fatalf("deploy: %+v", got.Status.Job)
	}
	specs := helperSpecs(eng, got.ID)
	if len(specs) != 2 {
		t.Fatalf("helper runs: %d", len(specs))
	}
	dir := a.composeDir(got.ID)
	envFile := filepath.Join(dir, "empty.env")
	for i, sp := range specs {
		if j := slices.Index(sp.Cmd, "--env-file"); j < 0 || sp.Cmd[j+1] != envFile {
			t.Errorf("run %d does not use the agent's env file: %v", i, sp.Cmd)
		}
	}
	up := specs[1]
	if j := slices.Index(up.Cmd, "--project-directory"); up.WorkingDir != dir || j < 0 || up.Cmd[j+1] != dir {
		t.Fatalf("up runs in %s with %v", up.WorkingDir, up.Cmd)
	}
	if b, err := os.ReadFile(envFile); err != nil || len(b) != 0 {
		t.Fatalf("env file: %q %v", b, err)
	}
	rendered, err := os.ReadFile(filepath.Join(dir, renderedCompose))
	if err != nil {
		t.Fatal(err)
	}
	ref := "${" + composeVar("XDG_Q") + "}"
	// The bind source holds the value of a reserved name: its reference must
	// resolve from the helper's environment, never from a project `.env`.
	if strings.Contains(string(rendered), "${XDG_Q}") || !strings.Contains(string(rendered), appDir+ref) {
		t.Fatalf("rendered file references %s", rendered)
	}
	if !slices.Contains(up.Env, composeVar("XDG_Q")+"=/data/zz") || slices.ContainsFunc(up.Env, func(kv string) bool { return strings.HasPrefix(kv, "XDG_Q=") }) {
		t.Fatalf("up environment %v", up.Env)
	}
}

// A build can only produce the service's own image name: never a tag the
// helper, the NVR or another service runs.
func TestComposeBuildsAreNamespaced(t *testing.T) {
	root := "/var/lib/sentinela-apps/apps/x"
	rel := root + "/releases/r1"
	if _, err := planOne(t, root, `{"build":{"context":"`+rel+`","tags":["sentinela:local"]}}`, ""); err == nil || !strings.Contains(err.Error(), "tags") {
		t.Fatalf("build tags: %v", err)
	}
	raw := `{"services":{"web":{"image":"docker:29-cli","build":{"context":"` + rel + `"}},"worker":{"image":"docker:29-cli"},"db":{"image":"postgres"}}}`
	p, err := planCompose([]byte(raw), testRules(root))
	if err != nil {
		t.Fatal(err)
	}
	out, err := p.apply(nil, composeLimits{ID: "x", MemoryMB: 128, CPUs: 0.5})
	if err != nil {
		t.Fatal(err)
	}
	var doc struct{ Services map[string]map[string]any }
	_ = json.Unmarshal(out, &doc)
	for _, s := range []string{"web", "worker"} {
		if doc.Services[s]["image"] != "sentinela-app/x:compose-web" || doc.Services[s]["pull_policy"] != "never" {
			t.Errorf("%s: image %v pull %v", s, doc.Services[s]["image"], doc.Services[s]["pull_policy"])
		}
	}
	if doc.Services["db"]["image"] != "postgres" || doc.Services["db"]["pull_policy"] != nil {
		t.Errorf("db changed: %v", doc.Services["db"])
	}
	// The same image spelled differently is the same image: a service that
	// would run "myapp" must not pull library/myapp from Docker Hub.
	raw = `{"services":{"web":{"image":"myapp:latest","build":{"context":"` + rel + `"}},"worker":{"image":"myapp"},"cron":{"image":"docker.io/library/myapp"},"hub":{"image":"ana/myapp"}}}`
	if p, err = planCompose([]byte(raw), testRules(root)); err != nil {
		t.Fatal(err)
	}
	if out, err = p.apply(nil, composeLimits{ID: "x", MemoryMB: 64, CPUs: 0.5}); err != nil {
		t.Fatal(err)
	}
	doc.Services = nil
	_ = json.Unmarshal(out, &doc)
	for s, want := range map[string]string{"web": "sentinela-app/x:compose-web", "worker": "sentinela-app/x:compose-web", "cron": "sentinela-app/x:compose-web", "hub": "ana/myapp"} {
		if doc.Services[s]["image"] != want {
			t.Errorf("%s: image %v, want %s", s, doc.Services[s]["image"], want)
		}
	}
	long := strings.Repeat("s", 130)
	if img := composeImage("x", long); !strings.HasPrefix(img, "sentinela-app/x:compose-") || len(img) > len("sentinela-app/x:")+128 {
		t.Errorf("long service name: %s", img)
	}
}

// A deploy removes the images of services its release no longer builds, and
// deleting a service removes every image its Compose builds produced, also
// those of services an earlier release had.
func TestComposeDeleteRemovesBuiltImages(t *testing.T) {
	a, eng, src := newTestAgent(t)
	got := deployCompose(t, a, eng, src, App{Name: "Site", UnfilteredBuilds: true}, func(string) string {
		return `{"services":{"web":{"build":{"context":"@WORK@"}},"api":{"build":{"context":"@WORK@"}}}}`
	})
	if got.Status.Job.Phase != "done" {
		t.Fatalf("deploy: %+v", got.Status.Job)
	}
	web, api := composeImage(got.ID, "web"), composeImage(got.ID, "api")
	eng.mu.Lock()
	eng.images[web], eng.images[api], eng.images["sentinela-app/other:compose-web"] = true, true, true
	eng.composeConfig = `{"services":{"web":{"build":{"context":"@WORK@"}}}}`
	eng.mu.Unlock()
	a.Enqueue(got.ID, "deploy") // the next release drops api
	runQueued(t, a)
	if v, _ := a.viewOf(got.ID); v.Status.Job.Phase != "done" {
		t.Fatalf("second deploy: %+v", v.Status.Job)
	}
	if eng.exact("rmi "+api) != 1 || eng.exact("rmi "+web) != 0 {
		t.Fatalf("after dropping api: %v", eng.calls)
	}
	eng.mu.Lock()
	eng.images[api] = true // say it was still in use then
	eng.mu.Unlock()
	if err := a.Delete(context.Background(), got.ID); err != nil {
		t.Fatal(err)
	}
	if eng.exact("rmi "+web) != 1 || eng.exact("rmi "+api) != 2 || eng.called("rmi sentinela-app/other:") != 0 {
		t.Fatalf("delete: %v", eng.calls)
	}
}

// Image references are compared the way Docker resolves them.
func TestNormalizeRef(t *testing.T) {
	for in, want := range map[string]string{
		"myapp":                         "docker.io/library/myapp:latest",
		"myapp:latest":                  "docker.io/library/myapp:latest",
		"docker.io/library/myapp":       "docker.io/library/myapp:latest",
		"index.docker.io/library/myapp": "docker.io/library/myapp:latest",
		"ana/myapp:1.2":                 "docker.io/ana/myapp:1.2",
		"ghcr.io/ana/myapp":             "ghcr.io/ana/myapp:latest",
		"localhost:5000/myapp":          "localhost:5000/myapp:latest",
		"localhost/myapp:2":             "localhost/myapp:2",
		"myapp@sha256:0123":             "docker.io/library/myapp@sha256:0123",
		"registry.example:5000/a/b:tag": "registry.example:5000/a/b:tag",
		"Upper/myapp":                   "Upper/myapp:latest",
	} {
		if got := normalizeRef(in); got != want {
			t.Errorf("%s: %s, want %s", in, got, want)
		}
	}
}

// Attribute values of an unexpected type are refused, never read as unset.
func TestPlanComposeValueTypes(t *testing.T) {
	root := "/var/lib/sentinela-apps/apps/x"
	rel := root + "/releases/r1"
	cases := []struct {
		name, service, top string
		refused            string
	}{
		{"network_mode not text", `{"image":"x","network_mode":{"host":true}}`, "", "network_mode must be text"},
		{"ipc not text", `{"image":"x","ipc":["host"]}`, "", "ipc must be text"},
		{"cgroup not text", `{"image":"x","cgroup":true}`, "", "cgroup must be text"},
		{"hooks not a list", `{"image":"x","post_start":{"command":["id"],"privileged":true}}`, "", "post_start must be a list"},
		{"ports not a list", `{"image":"x","ports":{"target":80,"published":"80"}}`, "", "ports must be a list"},
		{"port not a mapping", `{"image":"x","ports":["80:80"]}`, "", "invalid port"},
		{"port option", `{"image":"x","ports":[{"target":80,"published":"8080","host_ip":"0.0.0.0","frob":1}]}`, "", "port option frob"},
		{"labels not a list", `{"image":"x","labels":"com.docker.compose.project=x"}`, "", "labels must be"},
		{"label not text", `{"image":"x","labels":[{"com.docker.compose.project":"x"}]}`, "", "labels must be text"},
		{"volume without type", `{"image":"x","volumes":[{"source":"/","target":"/host"}]}`, "", "has no type"},
		{"volume empty type", `{"image":"x","volumes":[{"type":"","source":"/","target":"/host"}]}`, "", "has no type"},
		{"volume type not text", `{"image":"x","volumes":[{"type":["bind"],"source":"/","target":"/host"}]}`, "", "volume type must be text"},
		{"read_only not a flag", `{"image":"x","volumes":[{"type":"bind","source":"` + rel + `","target":"/a","read_only":"yes"}]}`, "", "read_only must be true or false"},
		{"cap_add not text", `{"image":"x","cap_add":[["SYS_ADMIN"]]}`, "", "cap_add must be a list of text"},
		{"security_opt not a list", `{"image":"x","security_opt":"seccomp=unconfined"}`, "", "security_opt must be a list"},
		{"scale not a number", `{"image":"x","scale":"9"}`, "", "scale must be a number"},
		{"replicas not a number", `{"image":"x","deploy":{"replicas":"9"}}`, "", "deploy.replicas must be a number"},
		{"build network not text", `{"build":{"context":"` + rel + `","network":{"host":1}}}`, "", "build network must be text"},
		{"image not text", `{"image":{"ref":"x"}}`, "", "image must be text"},
		{"external network not a flag", `{"image":"x"}`, `,"networks":{"n":{"external":"true"}}`, "external must be true or false"},
		{"external volume not a flag", `{"image":"x"}`, `,"volumes":{"v":{"external":{"name":"sapp-other_v"}}}`, "external must be true or false"},
		{"external secret not a flag", `{"image":"x"}`, `,"secrets":{"s":{"external":1}}`, "external must be true or false"},
		{"network name not text", `{"image":"x"}`, `,"networks":{"n":{"name":["host"]}}`, "name must be text"},
		{"networks not a mapping", `{"image":"x"}`, `,"networks":["host"]`, "networks must be a mapping"},
		{"secret file not text", `{"image":"x"}`, `,"secrets":{"s":{"file":["/etc/shadow"]}}`, "file must be text"},
		{"env_file entry", `{"image":"x","env_file":[{"path":"` + rel + `/a.env","frob":1}]}`, "", "env_file frob"},
		{"plain types", `{"image":"x","network_mode":"none","ipc":"private","cgroup":"private","ports":[{"target":80,"published":"8080","protocol":"tcp","mode":"ingress","app_protocol":"http"}],
			"volumes":[{"type":"bind","source":"` + rel + `","target":"/a","read_only":true}],"scale":1,"labels":["a=b"],"post_start":[{"command":["id"]}]}`, `,"networks":{"n":{"external":false}}`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := planOne(t, root, c.service, c.top)
			switch {
			case c.refused == "" && err != nil:
				t.Fatalf("refused: %v", err)
			case c.refused != "" && err == nil:
				t.Fatal("accepted")
			case c.refused != "" && !strings.Contains(err.Error(), c.refused):
				t.Fatalf("error %q does not mention %q", err, c.refused)
			}
		})
	}
}

// Extensions (x-*) are dropped from the rendered file: Compose reads some of
// them itself (a secret's x-#value is its content).
func TestComposeApplyDropsExtensions(t *testing.T) {
	root := "/var/lib/sentinela-apps/apps/x"
	p, err := planOne(t, root, `{"image":"x","x-a":1,"deploy":{"x-b":1},"networks":{"default":{"x-c":1}},
		"volumes":[{"type":"volume","source":"v","target":"/v","x-d":1}],"ports":[{"target":80,"published":"8080","x-e":1}]}`,
		`,"x-top":{"privileged":true},"volumes":{"v":{"x-f":1}},"networks":{"default":{"x-g":1}},"secrets":{"s":{"file":"`+root+`/data/s","x-#value":"content"}}`)
	if err != nil {
		t.Fatal(err)
	}
	out, err := p.apply([]Port{{Service: "a", Container: 80, Proto: "tcp", Host: 20001}}, composeLimits{ID: "x", MemoryMB: 128, CPUs: 0.5, BindIP: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), `"x-`) {
		t.Fatalf("extension rendered: %s", out)
	}
}

// Builds outside the sn-* firewall need the owner's consent, asked before
// anything is built.
func TestUnfilteredBuildsNeedConsent(t *testing.T) {
	t.Run("compose build", func(t *testing.T) {
		a, eng, src := newTestAgent(t)
		got := deployCompose(t, a, eng, src, App{Name: "Site"}, func(string) string {
			return `{"services":{"web":{"build":{"context":"@WORK@"}}}}`
		})
		if got.Status.Job.Phase != "failed" || !strings.Contains(got.Status.Job.Error, "Allow builds outside the firewall") {
			t.Fatalf("deploy: %+v", got.Status.Job)
		}
		if n := len(helperSpecs(eng, got.ID)); n != 1 || got.Deployed != nil {
			t.Fatalf("helper runs %d, deployed %+v", n, got.Deployed)
		}
	})
	t.Run("buildkit retry", func(t *testing.T) {
		a, eng, src := newTestAgent(t)
		eng.buildKitOnly = true
		src.files = map[string]string{"Dockerfile": "FROM node\nRUN --mount=type=cache,target=/x true\n"}
		v, err := a.Create(App{Name: "Img", Source: SourceGitHub, Repo: "ana/img", Mode: ModeDockerfile, Ports: []Port{{Container: 8080}}})
		if err != nil {
			t.Fatal(err)
		}
		a.Enqueue(v.ID, "deploy")
		runQueued(t, a)
		got, _ := a.viewOf(v.ID)
		if got.Status.Job.Phase != "failed" || !strings.Contains(got.Status.Job.Error, "BuildKit") || len(eng.builds) != 1 || eng.builds[0].BuildKit {
			t.Fatalf("deploy %+v builds %+v", got.Status.Job, eng.builds)
		}
	})
	t.Run("image only needs nothing", func(t *testing.T) {
		a, eng, src := newTestAgent(t)
		got := deployCompose(t, a, eng, src, App{Name: "Site"}, func(string) string { return `{"services":{"web":{"image":"nginx"}}}` })
		if got.Status.Job.Phase != "done" {
			t.Fatalf("deploy: %+v", got.Status.Job)
		}
	})
}

// renderedDoc reads the Compose file the agent last rendered for id.
func renderedDoc(t *testing.T, a *Agent, id string) map[string]map[string]map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(a.composeDir(id), renderedCompose))
	if err != nil {
		t.Fatal(err)
	}
	doc := map[string]map[string]map[string]any{}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"services", "secrets", "configs"} {
		if raw[k] != nil {
			m := map[string]map[string]any{}
			if err := json.Unmarshal(raw[k], &m); err != nil {
				t.Fatal(err)
			}
			doc[k] = m
		}
	}
	return doc
}

// lastEvent is the kind of the newest event of a service.
func lastEvent(v View) string {
	if len(v.Status.Events) == 0 {
		return ""
	}
	return v.Status.Events[len(v.Status.Events)-1].Kind
}

// After an update, the first `up --no-build` of a service an earlier agent
// deployed runs its built images under their new names: the agent names the
// image ID a container of the project runs, never what an old name points to;
// without such a container the service waits for a deploy, and says so.
func TestComposeUpgradeAdoptsBuiltImages(t *testing.T) {
	const built = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	cfg := func(string) string {
		return `{"services":{"web":{"image":"myapp","build":{"context":"@WORK@"}},"worker":{"image":"myapp:latest"}}}`
	}
	// upgraded deploys a build, then makes it look like a deployment of an
	// earlier agent: no image under the new names, no `up` since the start,
	// containers stopped by a reboot, and no consent to builds yet.
	upgraded := func(t *testing.T) (*Agent, *fakeEngine, string) {
		t.Helper()
		a, eng, src := newTestAgent(t)
		got := deployCompose(t, a, eng, src, App{Name: "Site", UnfilteredBuilds: true}, cfg)
		if got.Status.Job.Phase != "done" {
			t.Fatalf("deploy: %+v", got.Status.Job)
		}
		a.mu.Lock()
		live := a.find(got.ID)
		live.Deployed.ComposeImages, live.UnfilteredBuilds = false, false
		a.rt(got.ID).composeUp = false
		a.mu.Unlock()
		eng.mu.Lock()
		eng.members = []Summary{{Name: "sapp-site-web-1", App: "site", Service: "web"}, {Name: "sapp-site-worker-1", App: "site", Service: "worker"}}
		eng.memberImages = map[string]string{"sapp-site-web-1": built, "sapp-site-worker-1": built}
		eng.mu.Unlock()
		return a, eng, got.ID
	}
	t.Run("names the image a container runs", func(t *testing.T) {
		a, eng, id := upgraded(t)
		before := len(helperSpecs(eng, id))
		a.Reconcile(context.Background())
		specs := helperSpecs(eng, id)
		if len(specs) != before+2 || !slices.Contains(specs[len(specs)-1].Cmd, "--no-build") {
			t.Fatalf("helper runs %d (before %d): %v", len(specs), before, specs[len(specs)-1].Cmd)
		}
		if eng.tags[composeImage(id, "web")] != built || len(eng.tags) != 1 {
			t.Fatalf("tags %v", eng.tags)
		}
		eng.mu.Lock()
		tag := slices.Index(eng.calls, "tag "+built+" "+composeImage(id, "web"))
		up := slices.Index(eng.calls[tag+1:], "create sapp-"+id+"-compose")
		eng.mu.Unlock()
		if tag < 0 || up < 0 {
			t.Fatalf("the image is not named before `up`: %v", eng.calls)
		}
		v, _ := a.viewOf(id)
		if v.Desired != Running || v.NeedsDeploy || v.Status.Error != "" {
			t.Fatalf("after the upgrade: desired %s, needs deploy %v, error %q", v.Desired, v.NeedsDeploy, v.Status.Error)
		}
		doc := renderedDoc(t, a, id)
		if doc["services"]["worker"]["image"] != composeImage(id, "web") {
			t.Fatalf("worker runs %v", doc["services"]["worker"]["image"])
		}
	})
	t.Run("no container runs it", func(t *testing.T) {
		a, eng, id := upgraded(t)
		eng.mu.Lock()
		eng.members = nil
		eng.mu.Unlock()
		before := len(helperSpecs(eng, id))
		a.Reconcile(context.Background())
		if n := len(helperSpecs(eng, id)); n != before+1 {
			t.Fatalf("helper runs %d (before %d): `up` must not run", n, before)
		}
		v, _ := a.viewOf(id)
		if v.Desired != Stopped || !v.NeedsDeploy || lastEvent(v) != "blocked" ||
			!strings.Contains(v.Status.Error, "deploy the service again") || !strings.Contains(v.Status.Error, "Allow builds outside the firewall") {
			t.Fatalf("desired %s, needs deploy %v, event %q, error %q", v.Desired, v.NeedsDeploy, lastEvent(v), v.Status.Error)
		}
		// It stays stopped: no retry every 15 s.
		a.Reconcile(context.Background())
		if n := len(helperSpecs(eng, id)); n != before+1 {
			t.Fatalf("retried: %d helper runs", n)
		}
	})
	t.Run("own build without a container", func(t *testing.T) {
		a, eng, id := upgraded(t)
		a.mu.Lock()
		a.find(id).Deployed.ComposeImages = true
		a.mu.Unlock()
		eng.mu.Lock()
		eng.members = nil
		eng.images[composeImage(id, "web")] = true
		eng.mu.Unlock()
		a.Reconcile(context.Background())
		v, _ := a.viewOf(id)
		if v.Desired != Running || v.Status.Error != "" || len(eng.tags) != 0 {
			t.Fatalf("desired %s, error %q, tags %v", v.Desired, v.Status.Error, eng.tags)
		}
	})
}

// A deployed project the current checks refuse is stopped when its
// containers come from an `up` no run of this agent accepted (an earlier
// version's), with the reason on the service; a refused settings change of a
// project this agent started leaves it running.
func TestComposeRefusedDeploymentIsStopped(t *testing.T) {
	a, eng, src := newTestAgent(t)
	got := deployCompose(t, a, eng, src, App{Name: "Site"}, func(string) string { return `{"services":{"web":{"image":"nginx"}}}` })
	if got.Status.Job.Phase != "done" {
		t.Fatalf("deploy: %+v", got.Status.Job)
	}
	id, member := got.ID, "sapp-"+got.ID+"-web-1"
	// What an earlier agent accepted: a bind of the whole service folder.
	eng.mu.Lock()
	eng.composeConfig = `{"services":{"web":{"image":"nginx","volumes":[{"type":"bind","source":"@WORK@/../..","target":"/state"}]}}}`
	eng.mu.Unlock()

	a.Enqueue(id, "recreate") // this agent's own `up` started what runs
	runQueued(t, a)
	v, _ := a.viewOf(id)
	if v.Status.Job.Phase != "failed" || eng.exact("stop "+member) != 0 || v.Desired != Running || v.NeedsDeploy {
		t.Fatalf("refused recreate: job %+v, stops %d, desired %s", v.Status.Job, eng.exact("stop "+member), v.Desired)
	}

	a.mu.Lock()
	a.rt(id).composeUp = false // the agent restarted (updated)
	a.mu.Unlock()
	before := len(helperSpecs(eng, id))
	a.Reconcile(context.Background())
	v, _ = a.viewOf(id)
	if eng.exact("stop "+member) != 1 || len(helperSpecs(eng, id)) != before+1 {
		t.Fatalf("stops %d, helper runs %d (before %d)", eng.exact("stop "+member), len(helperSpecs(eng, id)), before)
	}
	if v.Desired != Stopped || !v.NeedsDeploy || lastEvent(v) != "blocked" ||
		!strings.HasPrefix(v.Status.Error, "Stopped until the next deploy") || !strings.Contains(v.Status.Error, "outside the project") {
		t.Fatalf("desired %s, needs deploy %v, event %q, error %q", v.Desired, v.NeedsDeploy, lastEvent(v), v.Status.Error)
	}

	// The same through a settings change, before any `up` of this agent.
	a.mu.Lock()
	a.find(id).Desired = Running
	eng.mu.Lock()
	eng.members[0].Running = true
	eng.mu.Unlock()
	a.mu.Unlock()
	a.Enqueue(id, "recreate")
	runQueued(t, a)
	v, _ = a.viewOf(id)
	if eng.exact("stop "+member) != 2 || v.Desired != Stopped || !strings.HasPrefix(v.Status.Job.Error, "stopped until the next deploy") {
		t.Fatalf("recreate: stops %d, desired %s, job %+v", eng.exact("stop "+member), v.Desired, v.Status.Job)
	}
}

// Top-level secrets and configs that read a variable (the *_FILE pattern of
// database images, build secrets) get it at `up`, under an agent name, from
// the vault and from plain variables alike; values never enter the file.
func TestComposeObjectsReadVariablesAtUp(t *testing.T) {
	a, eng, src := newTestAgent(t)
	got := deployCompose(t, a, eng, src, App{Name: "Site", Env: []EnvVar{
		{Key: "DB_PASSWORD", Value: "pw-s3cret", Secret: true}, {Key: "SITE_CONF", Value: "listen 80;"}, {Key: "PATH", Value: "/srv/bin"},
	}}, func(string) string {
		return `{"services":{"db":{"image":"postgres","secrets":[{"source":"db_pw"}],"configs":[{"source":"site"}],"environment":{"POSTGRES_PASSWORD_FILE":"/run/secrets/db_pw"}}},
		"secrets":{"db_pw":{"name":"sapp-site_db_pw","environment":"DB_PASSWORD"},"path":{"environment":"PATH"},"unset":{"environment":"NOPE"}},
		"configs":{"site":{"name":"sapp-site_site","environment":"SITE_CONF"}}}`
	})
	if got.Status.Job.Phase != "done" {
		t.Fatalf("deploy: %+v", got.Status.Job)
	}
	doc := renderedDoc(t, a, got.ID)
	for top, items := range map[string]map[string]string{"secrets": {"db_pw": "DB_PASSWORD", "path": "PATH", "unset": "NOPE"}, "configs": {"site": "SITE_CONF"}} {
		for item, key := range items {
			if doc[top][item]["environment"] != composeObjectVar(key) {
				t.Errorf("%s %s reads %v", top, item, doc[top][item]["environment"])
			}
		}
	}
	b, _ := os.ReadFile(filepath.Join(a.composeDir(got.ID), renderedCompose))
	if strings.Contains(string(b), "pw-s3cret") {
		t.Fatal("secret value in the rendered file")
	}
	specs := helperSpecs(eng, got.ID)
	up := specs[len(specs)-1].Env
	for _, kv := range []string{composeObjectVar("DB_PASSWORD") + "=pw-s3cret", composeObjectVar("SITE_CONF") + "=listen 80;", composeObjectVar("PATH") + "=/srv/bin"} {
		if !slices.Contains(up, kv) {
			t.Errorf("up environment lacks %s: %v", kv, up)
		}
	}
	if slices.ContainsFunc(up, func(kv string) bool {
		return strings.HasPrefix(kv, composeObjectVar("NOPE")+"=") || strings.HasPrefix(kv, "DB_PASSWORD=") || kv == "PATH=/srv/bin"
	}) {
		t.Fatalf("up environment: %v", up)
	}
}

// The image rename is decided on the file as checked: a vault value that
// also appears in an image name (a tag, say) cannot hide that two services
// run the same image.
func TestComposeRenameSurvivesVaultReferences(t *testing.T) {
	a, eng, src := newTestAgent(t)
	got := deployCompose(t, a, eng, src, App{Name: "Site", UnfilteredBuilds: true, Env: []EnvVar{{Key: "TAG_TOKEN", Value: "latest", Secret: true}}}, func(string) string {
		return `{"services":{"web":{"image":"myapp:latest","build":{"context":"@WORK@"}},"worker":{"image":"myapp"}}}`
	})
	if got.Status.Job.Phase != "done" {
		t.Fatalf("deploy: %+v", got.Status.Job)
	}
	if img := renderedDoc(t, a, got.ID)["services"]["worker"]["image"]; img != composeImage(got.ID, "web") {
		t.Fatalf("worker runs %v", img)
	}
}

// `config` reads an input file the agent wrote into its own folder, never one
// in the release (which a service may bind writable), and resolves relative
// paths from the Compose file's folder.
func TestComposeInputStaysInTheAgentFolder(t *testing.T) {
	a, eng, src := newTestAgent(t)
	got := deployCompose(t, a, eng, src, App{Name: "Site"}, func(string) string { return `{"services":{"web":{"image":"nginx"}}}` })
	if got.Status.Job.Phase != "done" {
		t.Fatalf("deploy: %+v", got.Status.Job)
	}
	cfg := helperSpecs(eng, got.ID)[0]
	input := filepath.Join(a.composeDir(got.ID), composeInputFile)
	f, d := slices.Index(cfg.Cmd, "-f"), slices.Index(cfg.Cmd, "--project-directory")
	if f < 0 || cfg.Cmd[f+1] != input || d < 0 || cfg.Cmd[d+1] != cfg.WorkingDir || !slices.Contains(cfg.Cmd, "config") {
		t.Fatalf("config runs %v in %s", cfg.Cmd, cfg.WorkingDir)
	}
	if _, err := os.Stat(input); !os.IsNotExist(err) {
		t.Fatalf("input left behind: %v", err)
	}
	entries, _ := os.ReadDir(cfg.WorkingDir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".sentinela") {
			t.Fatalf("agent file in the release: %s", e.Name())
		}
	}
}
