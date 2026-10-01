package apps

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	errNoApp    = errors.New("serviço não encontrado")
	errNotReady = errors.New("faça o primeiro deploy antes")
)

// View is a service as the dashboard sees it: settings (secrets blanked),
// live status and the links to open it.
type View struct {
	App
	Status Status `json:"status"`
	Links  []Link `json:"links"`
}

// Status is the live state of a service.
type Status struct {
	// State: never (not deployed), deploying, running, partial (compose,
	// some containers down), stopped, exited, crashloop, waiting (boot delay).
	State string `json:"state"`
	// Since: when the single container started or stopped (none for compose).
	Since     *time.Time `json:"since,omitempty"`
	ExitCode  int        `json:"exit_code,omitempty"`
	OOMKilled bool       `json:"oom_killed,omitempty"`
	Error     string     `json:"error,omitempty"`
	Job       *Job       `json:"job,omitempty"`
	// Containers of a compose service, running/total.
	Running int     `json:"running,omitempty"`
	Total   int     `json:"total,omitempty"`
	CPU     float64 `json:"cpu_pct"`
	Mem     uint64  `json:"mem_bytes"`
	MemMax  uint64  `json:"mem_limit"`
	NetRx   uint64  `json:"net_rx"`
	NetTx   uint64  `json:"net_tx"`
	Pids    uint64  `json:"pids"`
	Disk    int64   `json:"disk_bytes"`
}

// Link is an address the service answers on.
type Link struct {
	Kind    string `json:"kind"` // lan, tailnet, public, local, internal
	URL     string `json:"url"`
	Service string `json:"service,omitempty"`
}

func (a *Agent) hosts() (lan, tailnet, public string) {
	lan, tailnet, public = a.Cfg.LANHost, a.Cfg.TailnetHost, a.Cfg.PublicHost
	if lan == "" {
		lan = a.lanIP()
	}
	if tailnet == "" {
		tailnet = a.tailnetIP()
	}
	return
}

// view builds a View (callers hold a.mu).
func (a *Agent) view(app *App, r *svcState) View {
	v := View{App: app.masked(), Links: []Link{}}
	st := &v.Status
	running := false
	if r != nil {
		st.Job, st.Error, st.Disk = r.job, r.err, r.disk
		if app.Mode == ModeCompose {
			st.Total = len(r.members)
			for _, m := range r.members {
				if m.Running {
					st.Running++
				}
			}
			running = st.Running > 0
		} else if c := r.container; c != nil {
			running = c.Running
			st.ExitCode, st.OOMKilled = c.ExitCode, c.OOMKilled
			t := c.FinishedAt
			if c.Running {
				t = c.StartedAt
			}
			if !t.IsZero() {
				st.Since = &t
			}
		}
		if running {
			st.CPU, st.Mem, st.MemMax, st.NetRx, st.NetTx, st.Pids = r.cpu, r.mem.MemUsage, r.mem.MemLimit, r.mem.NetRx, r.mem.NetTx, r.mem.Pids
		}
	}
	switch {
	case r != nil && a.busyLocked(r):
		st.State = "deploying"
	case app.Deployed == nil:
		st.State = "never"
	case running && app.Desired == Running && app.Mode == ModeCompose && r.composeUp && st.Running < composeExpected(r):
		st.State = "partial"
	case running:
		st.State = "running"
	case app.Desired == Running && r != nil && a.now().Before(r.pauseTill):
		st.State = "crashloop"
	case app.Desired == Running && a.now().Before(a.startAfter):
		st.State = "waiting"
	case app.Desired == Running:
		st.State = "exited"
	default:
		st.State = "stopped"
	}
	lan, tailnet, public := a.hosts()
	for _, p := range app.Ports {
		if p.Host == 0 || p.Proto != "tcp" {
			continue
		}
		add := func(kind, host string) {
			if host != "" {
				v.Links = append(v.Links, Link{Kind: kind, URL: "http://" + net.JoinHostPort(host, strconv.Itoa(p.Host)), Service: p.Service})
			}
		}
		switch app.Exposure {
		case ExposeLocal:
			add("local", "127.0.0.1")
		case ExposeTailnet:
			add("tailnet", tailnet)
		default:
			add("lan", lan)
			add("tailnet", tailnet)
			if app.Exposure == ExposePublic {
				add("public", public)
			}
		}
		if app.Mode != ModeCompose {
			v.Links = append(v.Links, Link{Kind: "internal", URL: "http://" + net.JoinHostPort(app.ID, strconv.Itoa(p.Container))})
		}
	}
	return v
}

// composeExpected: containers that should be running (one-off jobs that
// finished cleanly are not counted as missing).
func composeExpected(r *svcState) int {
	n := 0
	for _, m := range r.members {
		if m.Running || slices.Contains(r.crashed, m.Name) {
			n++
		}
	}
	return n
}

// Views lists every service.
func (a *Agent) Views() []View {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]View, 0, len(a.apps))
	for _, app := range a.apps {
		out = append(out, a.view(app, a.run[app.ID]))
	}
	return out
}

func (a *Agent) viewOf(id string) (View, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	app := a.find(id)
	if app == nil {
		return View{}, errNoApp
	}
	return a.view(app, a.run[id]), nil
}

func (a *Agent) limits() Limits {
	return Limits{MaxMemoryMB: a.Cfg.MaxMemoryMB, MaxCPUs: a.Cfg.MaxCPUs, PortFirst: a.Cfg.PortFirst,
		PortLast: a.Cfg.PortLast, Runtimes: a.Cfg.Runtimes, Public: a.Cfg.PublicHost != "", Local: a.Sources[SourceLocal] != nil}
}

var slugStrip = regexp.MustCompile(`[^a-z0-9]+`)

// slug makes an id from a name: "Meu Bot!" -> "meu-bot".
func slug(name string) string {
	s := strings.Trim(slugStrip.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if len(s) > 26 {
		s = strings.TrimRight(s[:26], "-")
	}
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		s = "app-" + s
	}
	return strings.TrimRight(s, "-")
}

// assignPorts gives each port without a host port the lowest free one of
// the range, keeping the host port a port had before and ports other
// services hold (callers hold a.mu).
func (a *Agent) assignPorts(app *App, prev []Port) error {
	used := map[int]bool{}
	for _, o := range a.apps {
		if o.ID == app.ID {
			continue
		}
		for _, p := range o.Ports {
			used[p.Host] = true
		}
	}
	old := map[string]int{}
	for _, p := range prev {
		old[p.key()] = p.Host
	}
	for i := range app.Ports {
		p := &app.Ports[i]
		if p.Host == 0 {
			p.Host = old[p.key()]
		}
		if p.Host != 0 && used[p.Host] {
			return fmt.Errorf("a porta %d do servidor já é de outro serviço", p.Host)
		}
		if p.Host != 0 {
			used[p.Host] = true
		}
	}
	for i := range app.Ports {
		p := &app.Ports[i]
		if p.Host != 0 {
			continue
		}
		for h := a.Cfg.PortFirst; h <= a.Cfg.PortLast; h++ {
			if !used[h] && a.portFree(h, p.Proto) {
				p.Host = h
				used[h] = true
				break
			}
		}
		if p.Host == 0 {
			return errors.New("não há portas livres na faixa dos serviços")
		}
	}
	return nil
}

// Create adds a service (stopped until its first deploy).
func (a *Agent) Create(in App) (View, error) {
	in.ID = strings.TrimSpace(in.ID)
	if in.ID == "" {
		in.ID = slug(in.Name)
	}
	if in.MemoryMB == 0 {
		in.MemoryMB = a.Cfg.MemoryMB
	}
	if in.CPUs == 0 {
		in.CPUs = a.Cfg.CPUs
	}
	in.Desired, in.Deployed, in.NeedsDeploy = Stopped, nil, false
	if err := in.Validate(a.limits()); err != nil {
		return View{}, err
	}
	for _, e := range in.Env {
		if e.Secret && e.Value == "" {
			return View{}, fmt.Errorf("a variável %s não tem valor", e.Key)
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.find(in.ID) != nil {
		return View{}, fmt.Errorf("já existe um serviço com o id %q", in.ID)
	}
	if len(a.apps) >= a.Cfg.MaxApps {
		return View{}, fmt.Errorf("limite de %d serviços atingido", a.Cfg.MaxApps)
	}
	if err := a.assignPorts(&in, nil); err != nil {
		return View{}, err
	}
	in.CreatedAt = a.now().UTC()
	in.UpdatedAt = in.CreatedAt
	app := in
	a.apps = append(a.apps, &app)
	if err := a.saveLocked(); err != nil {
		return View{}, err
	}
	return a.view(&app, a.rt(app.ID)), nil
}

// Update changes a service's settings. Source changes wait for the next
// deploy; run settings (env, ports, limits, command) are applied to the
// running service right away.
func (a *Agent) Update(id string, in App) (View, error) {
	in.ID = id
	a.mu.Lock()
	cur := a.find(id)
	if cur == nil {
		a.mu.Unlock()
		return View{}, errNoApp
	}
	mergeSecrets(in.Env, cur.Env)
	in.Desired, in.Deployed, in.CreatedAt = cur.Desired, cur.Deployed, cur.CreatedAt
	if in.Mode == ModeCompose && cur.Mode == ModeCompose {
		in.Ports = cur.Ports // they come from the Compose file
	}
	if err := in.Validate(a.limits()); err != nil {
		a.mu.Unlock()
		return View{}, err
	}
	if err := a.assignPorts(&in, cur.Ports); err != nil {
		a.mu.Unlock()
		return View{}, err
	}
	in.NeedsDeploy = cur.NeedsDeploy || cur.Deployed != nil && sourceChanged(cur, &in)
	runChanged := !slices.Equal(cur.Env, in.Env) || !slices.Equal(cur.Ports, in.Ports) || cur.Exposure != in.Exposure ||
		cur.MemoryMB != in.MemoryMB || cur.CPUs != in.CPUs || cur.Start != in.Start || cur.Root != in.Root
	in.UpdatedAt = a.now().UTC()
	*cur = in
	err := a.saveLocked()
	v := a.view(cur, a.run[id])
	a.mu.Unlock()
	if err != nil {
		return View{}, err
	}
	if runChanged && !in.NeedsDeploy && in.Deployed != nil && in.Desired == Running {
		if err := a.Enqueue(id, "recreate"); err != nil && !errors.Is(err, errBusy) {
			return v, err
		}
	}
	return a.viewOf(id)
}

// Delete stops and removes a service with its files, volumes and images.
func (a *Agent) Delete(ctx context.Context, id string) error {
	app := a.snapshot(id)
	if app == nil {
		return errNoApp
	}
	a.mu.Lock()
	busy := a.busyLocked(a.rt(id))
	a.mu.Unlock()
	if busy {
		return errBusy
	}
	if app.Mode == ModeCompose {
		_, err := a.composeCLI(ctx, app, "", []string{"down", "--volumes", "--remove-orphans", "--rmi", "local"}, func(string, ...any) {})
		if err != nil {
			return fmt.Errorf("docker compose down: %w", err)
		}
	}
	if err := a.Eng.Remove(ctx, containerName(id)); err != nil {
		return err
	}
	for _, h := range []string{"-setup", "-compose"} {
		_ = a.Eng.Remove(ctx, containerName(id)+h)
	}
	if d := app.Deployed; d != nil && strings.HasPrefix(d.Image, imagePrefix) {
		_ = a.Eng.RemoveImage(ctx, d.Image)
	}
	a.mu.Lock()
	a.apps = slices.DeleteFunc(a.apps, func(x *App) bool { return x.ID == id })
	delete(a.run, id)
	err := a.saveLocked()
	a.mu.Unlock()
	if rerr := os.RemoveAll(a.appDir(id)); err == nil {
		err = rerr
	}
	return err
}

// SetDesired starts or stops a service now (and remembers it).
func (a *Agent) SetDesired(ctx context.Context, id, desired string) error {
	a.mu.Lock()
	app := a.find(id)
	if app == nil {
		a.mu.Unlock()
		return errNoApp
	}
	if app.Deployed == nil {
		a.mu.Unlock()
		return errNotReady
	}
	app.Desired = desired
	mode := app.Mode
	r := a.rt(id)
	r.pauseTill, r.restarts, r.err, r.composeUp = time.Time{}, nil, "", false
	err := a.saveLocked()
	a.mu.Unlock()
	if err != nil {
		return err
	}
	if desired == Stopped {
		if mode == ModeCompose {
			cs, err := a.projectContainers(ctx, id)
			if err != nil {
				return err
			}
			for _, c := range cs {
				if c.Running {
					if err := a.Eng.Stop(ctx, c.Name, stopGrace); err != nil {
						return err
					}
				}
			}
		} else if err := a.Eng.Stop(ctx, containerName(id), stopGrace); err != nil {
			return err
		}
		a.refresh(ctx, id, mode)
		return nil
	}
	a.reconcileOne(ctx, id)
	a.refresh(ctx, id, mode)
	return nil
}

// refresh re-reads a service's containers so the answer to an action shows
// its effect, not the state of the last supervision tick.
func (a *Agent) refresh(ctx context.Context, id, mode string) {
	if mode == ModeCompose {
		if cs, err := a.projectContainers(ctx, id); err == nil {
			a.mu.Lock()
			a.rt(id).members = cs
			a.mu.Unlock()
		}
		return
	}
	if st, err := a.Eng.Inspect(ctx, containerName(id)); err == nil {
		a.mu.Lock()
		a.rt(id).container = st
		a.mu.Unlock()
	}
}

// Restart stops and starts a service (recreating what is missing).
func (a *Agent) Restart(ctx context.Context, id string) error {
	app := a.snapshot(id)
	if app == nil {
		return errNoApp
	}
	if app.Deployed == nil {
		return errNotReady
	}
	if err := a.SetDesired(ctx, id, Stopped); err != nil {
		return err
	}
	return a.SetDesired(ctx, id, Running)
}

// Check compares the deployed version with the source now.
func (a *Agent) Check(ctx context.Context, id string) (map[string]any, error) {
	app := a.snapshot(id)
	if app == nil {
		return nil, errNoApp
	}
	src, err := a.source(app)
	if err != nil {
		return nil, err
	}
	v, err := src.Resolve(ctx, app)
	if err != nil {
		return nil, err
	}
	deployed := ""
	if app.Deployed != nil {
		deployed = app.Deployed.Version
	}
	return map[string]any{"latest": v.Label, "commit": v.Commit, "message": v.Message, "branch": v.Branch,
		"deployed": deployed, "update": v.Label != deployed}, nil
}

// Inspect looks at a source before a service exists: what it is, how to
// run it, which version it is at. GitHub sources are downloaded to a
// temporary folder for this.
func (a *Agent) Inspect(ctx context.Context, in App) (Detection, error) {
	in.ID, in.Name, in.Mode, in.Runtime, in.Start = "inspect", "inspect", ModeScript, "shell", "true"
	in.MemoryMB, in.CPUs = a.Cfg.MemoryMB, a.Cfg.CPUs
	if err := in.Validate(a.limits()); err != nil {
		return Detection{}, err
	}
	src, err := a.source(&in)
	if err != nil {
		return Detection{}, err
	}
	v, err := src.Resolve(ctx, &in)
	if err != nil {
		return Detection{}, err
	}
	tmp := filepath.Join(a.Cfg.StateDir, "tmp", "inspect-"+randHex())
	defer os.RemoveAll(tmp)
	if err := src.Snapshot(ctx, &in, v, tmp); err != nil {
		return Detection{}, err
	}
	d, err := detectInSnapshot(tmp, in.Dir)
	if err != nil {
		return Detection{}, err
	}
	d.Version = &v
	return d, nil
}

func randHex() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// SetGitHubToken stores (or with "" removes) the token used for private
// repositories. It is never returned.
func (a *Agent) SetGitHubToken(tok string) error {
	tok = strings.TrimSpace(tok)
	if len(tok) > 255 || strings.ContainsAny(tok, " \r\n\t") {
		return errors.New("token inválido")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ghToken = tok
	if tok == "" {
		if err := os.Remove(a.tokenFile()); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	return os.WriteFile(a.tokenFile(), []byte(tok+"\n"), 0o600)
}

// GitHubToken is the stored token ("" = public repositories only).
func (a *Agent) GitHubToken() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.ghToken
}

// LogLine is one line of a service's output.
type LogLine struct {
	Stream  int    `json:"s"`
	Text    string `json:"t"`
	Service string `json:"svc,omitempty"`
}

// Logs returns the last lines of a service's output (every container of a
// compose service, merged by time).
func (a *Agent) Logs(ctx context.Context, id string, tail int) ([]LogLine, error) {
	app := a.snapshot(id)
	if app == nil {
		return nil, errNoApp
	}
	type named struct{ name, svc string }
	var targets []named
	if app.Mode == ModeCompose {
		cs, err := a.projectContainers(ctx, id)
		if err != nil {
			return nil, err
		}
		for _, c := range cs {
			targets = append(targets, named{c.Name, c.Service})
		}
	} else {
		targets = []named{{containerName(id), ""}}
	}
	lines := []LogLine{}
	for _, t := range targets {
		err := a.Eng.Logs(ctx, t.name, LogOptions{Tail: tail, Timestamps: true}, func(s int, text string) {
			lines = append(lines, LogLine{Stream: s, Text: text, Service: t.svc})
		})
		if err != nil && !errors.Is(err, errNotFound) {
			return nil, err
		}
	}
	if len(targets) > 1 {
		// Lines start with an RFC 3339 timestamp: they sort as text.
		sort.SliceStable(lines, func(i, j int) bool { return lines[i].Text < lines[j].Text })
		if len(lines) > tail {
			lines = lines[len(lines)-tail:]
		}
	}
	return lines, nil
}

// ---------- HTTP ----------

// Handler is the agent's API. Every request must come from loopback with
// the shared token (the API holds it; browsers never reach the agent).
func (a *Agent) Handler(token string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/info", a.hInfo)
	mux.HandleFunc("GET /v1/apps", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, http.StatusOK, a.Views()) })
	mux.HandleFunc("POST /v1/apps", a.hCreate)
	mux.HandleFunc("POST /v1/inspect", a.hInspect)
	mux.HandleFunc("GET /v1/local", a.hLocal)
	mux.HandleFunc("GET /v1/apps/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, err := a.viewOf(r.PathValue("id"))
		reply(w, v, err)
	})
	mux.HandleFunc("PUT /v1/apps/{id}", a.hUpdate)
	mux.HandleFunc("DELETE /v1/apps/{id}", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
		defer cancel()
		reply(w, map[string]bool{"ok": true}, a.Delete(ctx, r.PathValue("id")))
	})
	mux.HandleFunc("POST /v1/apps/{id}/{action}", a.hAction)
	mux.HandleFunc("GET /v1/apps/{id}/logs", a.hLogs)
	mux.HandleFunc("GET /v1/apps/{id}/deploy-log", a.hDeployLog)
	mux.HandleFunc("PUT /v1/github-token", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Token string `json:"token"`
		}
		if !readBody(w, r, &body) {
			return
		}
		reply(w, map[string]bool{"ok": true}, a.SetGitHubToken(body.Token))
	})
	mux.HandleFunc("DELETE /v1/github-token", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]bool{"ok": true}, a.SetGitHubToken(""))
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		ip := net.ParseIP(host)
		tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if token == "" || ip == nil || !ip.IsLoopback() || !ok || subtle.ConstantTimeCompare([]byte(tok), []byte(token)) != 1 {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func (a *Agent) hInfo(w http.ResponseWriter, r *http.Request) {
	lan, tailnet, public := a.hosts()
	a.mu.Lock()
	used := 0
	for _, app := range a.apps {
		if app.Desired == Running && app.Deployed != nil {
			used += memBudget(app)
		}
	}
	n := len(a.apps)
	waiting := a.now().Before(a.startAfter)
	a.mu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	ver, err := a.Eng.Version(ctx)
	derr := ""
	if err != nil {
		derr = "o Docker não respondeu"
	}
	local := ""
	if a.Sources[SourceLocal] != nil {
		local = a.Cfg.LocalRoot
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"docker": ver, "docker_error": derr,
		"hosts":      map[string]string{"lan": lan, "tailnet": tailnet, "public": public},
		"port_first": a.Cfg.PortFirst, "port_last": a.Cfg.PortLast,
		"runtimes":   a.Cfg.Runtimes,
		"local_root": local,
		"limits": map[string]any{"memory_mb": a.Cfg.MemoryMB, "max_memory_mb": a.Cfg.MaxMemoryMB, "total_memory_mb": a.Cfg.TotalMemoryMB,
			"cpus": a.Cfg.CPUs, "max_cpus": a.Cfg.MaxCPUs, "build_cpus": a.Cfg.BuildCPUs, "build_memory_mb": a.Cfg.BuildMemoryMB, "max_apps": a.Cfg.MaxApps},
		"used_memory_mb": used, "apps": n, "mem_free_mb": a.memFreeMB(),
		"github_token": a.GitHubToken() != "", "boot_waiting": waiting,
	})
}

// createBody is a new service; Deploy queues the first deploy at once.
type createBody struct {
	App
	Deploy bool `json:"deploy"`
}

func (a *Agent) hCreate(w http.ResponseWriter, r *http.Request) {
	var body createBody
	if !readBody(w, r, &body) {
		return
	}
	v, err := a.Create(body.App)
	if err == nil && body.Deploy {
		err = a.Enqueue(v.ID, "deploy")
		v, _ = a.viewOf(v.ID)
	}
	if err != nil {
		reply(w, nil, err)
		return
	}
	writeJSON(w, http.StatusCreated, v)
}

func (a *Agent) hUpdate(w http.ResponseWriter, r *http.Request) {
	var in App
	if !readBody(w, r, &in) {
		return
	}
	v, err := a.Update(r.PathValue("id"), in)
	reply(w, v, err)
}

func (a *Agent) hInspect(w http.ResponseWriter, r *http.Request) {
	var in App
	if !readBody(w, r, &in) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	d, err := a.Inspect(ctx, in)
	reply(w, d, err)
}

func (a *Agent) hLocal(w http.ResponseWriter, r *http.Request) {
	src, ok := a.Sources[SourceLocal].(LocalSource)
	if !ok {
		reply(w, nil, errors.New("pastas locais não estão ativadas (apps.local_root)"))
		return
	}
	p := r.URL.Query().Get("path")
	dirs, err := src.Browse(p)
	reply(w, map[string]any{"root": src.Root, "path": cleanRel(p), "folders": dirs}, err)
}

func (a *Agent) hAction(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	var err error
	switch r.PathValue("action") {
	case "deploy":
		err = a.Enqueue(id, "deploy")
	case "start":
		err = a.SetDesired(ctx, id, Running)
	case "stop":
		err = a.SetDesired(ctx, id, Stopped)
	case "restart":
		err = a.Restart(ctx, id)
	case "check":
		res, err := a.Check(ctx, id)
		reply(w, res, err)
		return
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "ação desconhecida"})
		return
	}
	if err != nil {
		reply(w, nil, err)
		return
	}
	v, err := a.viewOf(id)
	reply(w, v, err)
}

func (a *Agent) hLogs(w http.ResponseWriter, r *http.Request) {
	tail, _ := strconv.Atoi(r.URL.Query().Get("tail"))
	if tail <= 0 || tail > 2000 {
		tail = 300
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	lines, err := a.Logs(ctx, r.PathValue("id"), tail)
	reply(w, map[string]any{"lines": lines}, err)
}

func (a *Agent) hDeployLog(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	a.mu.Lock()
	app, rt := a.find(id), a.run[id]
	var job *Job
	if rt != nil && rt.job != nil {
		j := *rt.job
		job = &j
	}
	a.mu.Unlock()
	if app == nil {
		reply(w, nil, errNoApp)
		return
	}
	var lines []string
	if rt != nil {
		lines = rt.log.lines()
	}
	if len(lines) == 0 { // agent restarted: the last saved log
		if b, err := os.ReadFile(filepath.Join(a.appDir(id), "deploy.log")); err == nil {
			lines = strings.Split(strings.TrimRight(string(b), "\n"), "\n")
		}
	}
	if lines == nil {
		lines = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": job, "lines": lines})
}

func readBody(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body: " + err.Error()})
		return false
	}
	return true
}

// reply writes v, or err with a status that matches it.
func reply(w http.ResponseWriter, v any, err error) {
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, v)
	case errors.Is(err, errNoApp):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.Is(err, errBusy):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
