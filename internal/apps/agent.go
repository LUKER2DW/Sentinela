package apps

import (
	"bufio"
	"bytes"
	"context"
	"crypto/cipher"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/LUKER2DW/sentinela/internal/config"
)

// Names on the Docker side.
const (
	networkName  = "sentinela-apps"
	bridgeName   = "sn-apps"
	imagePrefix  = "sentinela-app/"
	labelApp     = "sentinela.app"
	setupTimeout = 20 * time.Minute
	stopGrace    = 10 * time.Second
	// A service that stops this often in crashWindow waits crashPause.
	crashLimit  = 5
	crashWindow = 10 * time.Minute
	crashPause  = 5 * time.Minute
	// minFreeMB: builds and starts are refused below this much free RAM,
	// so a service can never push the recorder into swap.
	minFreeMB = 400
)

// Agent owns the services: their state file, the Docker containers and a
// queue that runs one deploy at a time.
type Agent struct {
	Cfg     config.Apps
	Eng     Engine
	Sources map[string]Source
	Log     *slog.Logger

	// Host facts, injectable for tests.
	now        func() time.Time
	memFreeMB  func() int
	bootID     func() string
	uptime     func() time.Duration
	portFree   func(port int, proto string) bool
	tailnetIP  func() string
	lanIP      func() string
	settleTime time.Duration
	fileMu     sync.Mutex // source imports, uploads and editor mutations

	mu          sync.Mutex
	apps        []*App
	bootSeq     string
	run         map[string]*svcState
	ghToken     string
	vaultCipher cipher.AEAD
	vaults      map[string]secretVault
	jobs        chan string // app ids
	// startAfter: no service starts before this (host boot delay).
	startAfter time.Time
	lastPublic string
}

// svcState holds live container data and the persisted diagnostic history.
type svcState struct {
	job       *Job
	queued    string // "deploy" or "recreate" while waiting in the queue
	container *ContainerState
	// Compose: containers seen, and whether `up` ran since the agent started.
	members      []Summary
	crashed      []string // members that exited with an error or OOM
	composeUp    bool
	samples      map[string]*Sample
	cpu          float64
	mem          Sample
	disk         int64
	diskAt       time.Time
	restarts     []time.Time
	pauseTill    time.Time
	err          string
	log          *ring
	events       []ServiceEvent
	lastExit     *ServiceExit
	restartCount int
	lastStops    map[string]time.Time
	secrets      []string // includes the candidate release while a deploy runs
}

// Job is the current or last deploy of a service.
type Job struct {
	Kind     string    `json:"kind"`  // deploy, recreate
	Phase    string    `json:"phase"` // queued, fetch, build, start, done, failed
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished,omitempty"`
	Version  string    `json:"version,omitempty"`
	Error    string    `json:"error,omitempty"`
}

// NewAgent prepares an agent; call Load, then Run.
func NewAgent(cfg config.Apps, eng Engine, sources map[string]Source, log *slog.Logger) *Agent {
	return &Agent{Cfg: cfg, Eng: eng, Sources: sources, Log: log, now: time.Now, memFreeMB: memAvailableMB,
		bootID: hostBootID, uptime: hostUptime, portFree: portFree, tailnetIP: tailnetIPv4, lanIP: lanIPv4, settleTime: 10 * time.Second,
		run: map[string]*svcState{}, jobs: make(chan string, 64), vaults: map[string]secretVault{}}
}

func (a *Agent) stateFile() string { return filepath.Join(a.Cfg.StateDir, "state.json") }

// appDir is the state folder of one app. Ids are checked when an app is
// created and looked up in the registry before use; the pattern is checked
// again here so a bad id can never name a path outside the apps folder.
func (a *Agent) appDir(id string) string {
	if !idPattern.MatchString(id) {
		id = "invalid"
	}
	return filepath.Join(a.Cfg.StateDir, "apps", id)
}
func (a *Agent) tokenFile() string   { return filepath.Join(a.Cfg.StateDir, "github-token") }
func containerName(id string) string { return "sapp-" + id }

func (a *Agent) releaseDir(id, rel string) string {
	// A release folder is one path element (a commit or a timestamp label).
	return filepath.Join(a.appDir(id), "releases", filepath.Base(rel))
}

// workDir is where a deployment's code (at the configured subfolder) is.
func (a *Agent) workDir(app *App, dep *Deployment) string {
	d := a.releaseDir(app.ID, dep.Release)
	if app.Dir != "" {
		d = filepath.Join(d, filepath.FromSlash(app.Dir))
	}
	return d
}

func (a *Agent) source(app *App) (Source, error) {
	if app.Managed {
		return projectSource{Dir: filepath.Join(a.appDir(app.ID), "project")}, nil
	}
	if app.Source == SourceZip {
		return projectSource{Dir: filepath.Join(a.Cfg.StateDir, "uploads", app.Path)}, nil
	}
	if s := a.Sources[app.Source]; s != nil {
		return s, nil
	}
	return nil, fmt.Errorf("source %q unavailable", app.Source)
}

type stateFile struct {
	BootID string `json:"boot_id"`
	Apps   []*App `json:"apps"`
}

// Load reads the state file and applies the reboot rule: after a host
// boot, services without autostart stay stopped, and none starts before
// the boot delay so the NVR comes up first.
func (a *Agent) Load() error {
	if err := os.MkdirAll(filepath.Join(a.Cfg.StateDir, "apps"), 0o750); err != nil {
		return err
	}
	if err := a.openVaultKey(); err != nil {
		return err
	}
	var sf stateFile
	if b, err := os.ReadFile(a.stateFile()); err == nil {
		if err := json.Unmarshal(b, &sf); err != nil {
			return fmt.Errorf("apps state: %w", err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if b, err := os.ReadFile(a.tokenFile()); err == nil {
		a.ghToken = strings.TrimSpace(string(b))
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.apps = sf.Apps
	for _, app := range a.apps {
		v, err := a.readVault(a.vaultFile(app.ID), app.ID)
		if err != nil {
			return err
		}
		// Migrate older plain-text secret entries on the first upgraded start.
		var legacy []EnvVar
		for _, e := range app.Env {
			if e.Secret || sensitiveKey(e.Key) {
				e.Secret = true
				if e.Value != "" {
					legacy = append(legacy, e)
				}
			}
		}
		v = mergeVault(v, legacy, nil, false)
		if err := a.storeVaultLocked(app.ID, v); err != nil {
			return err
		}
		for i := range app.Env {
			if sensitiveKey(app.Env[i].Key) {
				app.Env[i].Secret = true
			}
			if app.Env[i].Secret {
				for _, e := range v.Vars {
					if e.Key == app.Env[i].Key {
						app.Env[i].Value = e.Value
					}
				}
			}
		}
		if app.Deployed != nil {
			app.Deployed.FileEnv = slices.Clone(v.Vars)
		}
	}
	boot := a.bootID()
	if sf.BootID != "" && boot != "" && sf.BootID != boot {
		for _, app := range a.apps {
			if !app.Autostart && app.Desired == Running {
				app.Desired = Stopped
				a.Log.Info("apps: left stopped after reboot (no autostart)", "app", app.ID)
			}
		}
	}
	a.bootSeq = boot
	if up := a.uptime(); up > 0 {
		a.startAfter = a.now().Add(time.Duration(a.Cfg.BootDelaySeconds)*time.Second - up)
	}
	for _, app := range a.apps {
		a.loadDiagnostics(app.ID)
	}
	return a.saveLocked()
}

// saveLocked writes the state file atomically (0600: it holds env values).
func (a *Agent) saveLocked() error {
	apps := make([]*App, len(a.apps))
	for i, app := range a.apps {
		safe := app.masked()
		apps[i] = &safe
	}
	b, err := json.MarshalIndent(stateFile{BootID: a.bootSeq, Apps: apps}, "", " ")
	if err != nil {
		return err
	}
	tmp := a.stateFile() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, a.stateFile())
}

func (a *Agent) rt(id string) *svcState {
	r := a.run[id]
	if r == nil {
		r = &svcState{log: newRing(3000), samples: map[string]*Sample{}, lastStops: map[string]time.Time{}}
		a.run[id] = r
	}
	return r
}

func (a *Agent) find(id string) *App {
	for _, app := range a.apps {
		if app.ID == id {
			return app
		}
	}
	return nil
}

// snapshot returns a copy of an app (nil if gone).
func (a *Agent) snapshot(id string) *App {
	a.mu.Lock()
	defer a.mu.Unlock()
	app := a.find(id)
	if app == nil {
		return nil
	}
	c := *app
	c.Env = slices.Clone(app.Env)
	c.Ports = slices.Clone(app.Ports)
	if app.Deployed != nil {
		d := *app.Deployed
		d.Services = slices.Clone(app.Deployed.Services)
		d.FileEnv = slices.Clone(app.Deployed.FileEnv)
		c.Deployed = &d
	}
	return &c
}

func (a *Agent) busyLocked(r *svcState) bool {
	return r.queued != "" || r.job != nil && r.job.Finished.IsZero()
}

// Run starts the job worker and the supervision loop; it returns when ctx ends.
func (a *Agent) Run(ctx context.Context) {
	if v, err := a.Eng.Version(ctx); err != nil {
		a.Log.Error("apps: docker unreachable", "err", err)
	} else {
		a.Log.Info("apps: docker", "version", v)
	}
	if err := a.Eng.EnsureNetwork(ctx, networkName, a.Cfg.Subnet, bridgeName); err != nil {
		a.Log.Error("apps: network", "err", err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case id := <-a.jobs:
				a.runJob(ctx, id)
			}
		}
	}()
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		a.Reconcile(ctx)
		select {
		case <-ctx.Done():
			wg.Wait()
			return
		case <-t.C:
		}
	}
}

// ---------- jobs ----------

var errBusy = errors.New("a deploy of this service is already queued or running")

// Enqueue asks for a deploy ("deploy": fetch, build, start) or a recreate
// (the current build with new settings).
func (a *Agent) Enqueue(id, kind string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	app := a.find(id)
	if app == nil {
		return errNoApp
	}
	if kind == "recreate" && app.Deployed == nil {
		return nil
	}
	r := a.rt(id)
	if a.busyLocked(r) {
		if r.queued == "recreate" && kind == "deploy" {
			r.queued = kind // a deploy also applies the settings
			r.job.Kind = kind
			a.saveDiagnosticsLocked(id)
			return nil
		}
		return errBusy
	}
	r.queued = kind
	r.job = &Job{Kind: kind, Phase: "queued", Started: a.now()}
	select {
	case a.jobs <- id:
		a.saveDiagnosticsLocked(id)
		return nil
	default:
		r.queued, r.job = "", nil
		return errors.New("muitos deploys na fila, tente de novo em instantes")
	}
}

func (a *Agent) runJob(ctx context.Context, id string) {
	a.mu.Lock()
	r := a.rt(id)
	kind := r.queued
	r.queued = ""
	if kind == "" || a.find(id) == nil {
		r.job = nil
		a.mu.Unlock()
		return
	}
	job := &Job{Kind: kind, Phase: "fetch", Started: a.now()}
	r.job = job
	if kind == "deploy" {
		r.log.reset()
	}
	a.mu.Unlock()
	logf := func(format string, args ...any) { a.logLine(id, fmt.Sprintf(format, args...)) }
	var err error
	if kind == "deploy" {
		err = a.deploy(ctx, id, job, logf)
	} else {
		a.setPhase(id, "start")
		err = a.recreate(ctx, id, logf)
	}
	a.mu.Lock()
	job.Finished = a.now()
	if err != nil {
		job.Phase, job.Error = "failed", a.redactLocked(id, err.Error())
		r.err = job.Error
	} else {
		job.Phase, r.err = "done", ""
	}
	a.eventLocked(id, "deploy", "Deploy: "+job.Phase+" "+job.Error)
	a.mu.Unlock()
	if err != nil {
		logf("✗ %v", err)
		a.Log.Warn("apps: job failed", "app", id, "kind", kind, "err", job.Error)
	} else if kind == "deploy" {
		logf("✓ Deploy finished")
	} else {
		logf("✓ New settings applied")
	}
	a.saveLog(id)
}

func (a *Agent) setPhase(id, phase string) {
	a.mu.Lock()
	if r := a.run[id]; r != nil && r.job != nil {
		r.job.Phase = phase
		a.saveDiagnosticsLocked(id)
	}
	a.mu.Unlock()
}

func (a *Agent) logLine(id, line string) {
	a.mu.Lock()
	r := a.rt(id)
	line = a.redactLocked(id, line)
	a.mu.Unlock()
	r.log.add(a.now().Format("15:04:05") + " " + line)
}

func (a *Agent) saveLog(id string) {
	a.mu.Lock()
	r := a.run[id]
	a.mu.Unlock()
	if r == nil {
		return
	}
	_ = os.WriteFile(filepath.Join(a.appDir(id), "deploy.log"), []byte(strings.Join(r.log.lines(), "\n")+"\n"), 0o600)
}

// deploy takes a new snapshot of the source, prepares it and replaces what
// runs. The old version keeps running until the new one is built.
func (a *Agent) deploy(ctx context.Context, id string, job *Job, logf func(string, ...any)) error {
	app := a.snapshot(id)
	if app == nil {
		return errNoApp
	}
	if err := a.importProject(ctx, app, logf); err != nil {
		return err
	}
	app = a.snapshot(id)
	src, err := a.source(app)
	if err != nil {
		return err
	}
	if free := a.memFreeMB(); free > 0 && free < minFreeMB {
		return fmt.Errorf("little free memory on the server (%d MB): deploy postponed to protect the recordings", free)
	}
	if app.Managed {
		logf("Reading the protected project of %s", app.Name)
	} else if app.Source == SourceLocal {
		logf("Reading the folder %s", app.Path)
	} else {
		logf("Buscando %s @ %s", app.Repo, app.Ref)
	}
	v, err := src.Resolve(ctx, app)
	if err != nil {
		return err
	}
	a.mu.Lock()
	job.Version = v.Label
	a.mu.Unlock()
	if v.Commit != "" {
		logf("Commit %s %s %s", v.Commit[:12], v.Branch, v.Message)
	}
	rel := v.Label + "-" + strconv.FormatInt(a.now().UnixNano(), 36)
	dir := a.releaseDir(id, rel)
	partial := dir + ".partial"
	_ = os.RemoveAll(partial)
	if err := src.Snapshot(ctx, app, v, partial); err != nil {
		_ = os.RemoveAll(partial)
		return err
	}
	if err := os.Rename(partial, dir); err != nil {
		return err
	}
	fail := func(err error) error {
		_ = os.RemoveAll(dir)
		return err
	}
	dep := &Deployment{Commit: v.Commit, Message: v.Message, Ref: v.Branch, Version: v.Label, Release: rel, At: a.now().UTC()}
	work := a.workDir(app, dep)
	if st, err := os.Stat(work); err != nil || !st.IsDir() {
		return fail(fmt.Errorf("folder %q does not exist in the source", app.Dir))
	}
	if err = a.absorbEnv(ctx, id, dir); err != nil {
		return fail(err)
	}
	a.mu.Lock()
	dep.FileEnv = slices.Clone(a.vaults[id].Vars)
	a.rt(id).secrets = envSecrets(dep.FileEnv)
	a.mu.Unlock()
	if len(dep.FileEnv) > 0 {
		logf("Injecting %d variables from the secrets manager", len(dep.FileEnv))
	}
	for _, d := range []string{"data", "cache"} {
		if err := os.MkdirAll(filepath.Join(a.appDir(id), d), 0o750); err != nil {
			return fail(err)
		}
	}
	a.setPhase(id, "build")
	switch app.Mode {
	case ModeScript:
		dep.Image = a.Cfg.Runtimes[app.Runtime]
		if err := a.ensureImage(ctx, dep.Image, logf); err != nil {
			return fail(err)
		}
		if strings.TrimSpace(app.Setup) != "" {
			if err := a.runSetup(ctx, app, dep, logf); err != nil {
				return fail(err)
			}
		}
	case ModeDockerfile:
		dep.Image = imagePrefix + id + ":" + strings.ToLower(v.Label)
		if _, err := os.Stat(filepath.Join(work, "Dockerfile")); err != nil {
			return fail(errors.New("there is no Dockerfile in this folder"))
		}
		if err := a.build(ctx, dep.Image, work, logf); err != nil {
			return fail(err)
		}
	}
	a.setPhase(id, "start")
	cur := a.snapshot(id) // settings may have changed while building
	if cur == nil {
		return fail(errNoApp)
	}
	if cur.Mode == ModeCompose {
		if err := a.canStart(cur); err != nil {
			return fail(err)
		}
		services, ports, err := a.composeUp(ctx, cur, work, true, logf)
		if err != nil {
			return fail(err)
		}
		dep.Services = services
		cur.Ports = ports
		if err := a.composeCheck(ctx, cur, logf); err != nil {
			return err // keep the release: it is what runs now
		}
	} else if err := a.activate(ctx, cur, dep, logf); err != nil {
		if cur.Deployed != nil && cur.Desired == Running {
			logf("The new version failed; restoring the previous version")
			restoreCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
			restoreErr := a.activate(restoreCtx, cur, cur.Deployed, logf)
			cancel()
			if restoreErr != nil {
				err = fmt.Errorf("%w; restore failed: %v", err, restoreErr)
			}
		} else {
			_ = a.Eng.Remove(ctx, containerName(id))
		}
		return fail(err)
	}
	a.mu.Lock()
	var old *Deployment
	if live := a.find(id); live != nil {
		old = live.Deployed
		live.Deployed, live.NeedsDeploy, live.Desired = dep, false, Running
		if cur.Mode == ModeCompose {
			live.Ports = cur.Ports
		}
		live.UpdatedAt = a.now().UTC()
		r := a.rt(id)
		r.restarts, r.pauseTill, r.composeUp = nil, time.Time{}, true
	}
	err = a.saveLocked()
	a.mu.Unlock()
	a.cleanup(ctx, id, dep, old)
	return err
}

// cleanup drops releases and images other than the current deployment.
func (a *Agent) cleanup(ctx context.Context, id string, cur, old *Deployment) {
	entries, _ := os.ReadDir(filepath.Join(a.appDir(id), "releases"))
	for _, e := range entries {
		if e.Name() != cur.Release {
			_ = os.RemoveAll(filepath.Join(a.appDir(id), "releases", e.Name()))
		}
	}
	if old != nil && old.Image != cur.Image && strings.HasPrefix(old.Image, imagePrefix) {
		_ = a.Eng.RemoveImage(ctx, old.Image)
	}
}

func (a *Agent) ensureImage(ctx context.Context, image string, logf func(string, ...any)) error {
	ok, err := a.Eng.ImageExists(ctx, image)
	if err != nil {
		return err
	}
	if ok {
		return nil
	}
	logf("Baixando a imagem %s", image)
	return a.Eng.Pull(ctx, image, func(s string) { logf("  %s", s) })
}

// runSetup runs the setup script once in a throwaway container with the
// build caps, against the new release only.
func (a *Agent) runSetup(ctx context.Context, app *App, dep *Deployment, logf func(string, ...any)) error {
	name := containerName(app.ID) + "-setup"
	_ = a.Eng.Remove(ctx, name)
	spec := a.scriptSpec(app, dep)
	spec.Cmd = []string{"sh", "-c", app.Setup}
	spec.Memory = int64(a.Cfg.BuildMemoryMB) << 20
	spec.NanoCPUs = int64(a.Cfg.BuildCPUs * 1e9)
	spec.CPUShares = 128
	spec.Pids = 1024
	spec.Network = networkName
	spec.Labels = map[string]string{labelApp: app.ID, "sentinela.helper": "setup"}
	logf("Preparando (setup): %s", firstLine(app.Setup))
	if err := a.Eng.Create(ctx, name, spec); err != nil {
		return err
	}
	defer func() {
		c, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		_ = a.Eng.Remove(c, name)
	}()
	if err := a.Eng.Start(ctx, name); err != nil {
		return err
	}
	tctx, cancel := context.WithTimeout(ctx, setupTimeout)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = a.Eng.Logs(tctx, name, LogOptions{Follow: true}, func(_ int, s string) { logf("  %s", s) })
	}()
	code, err := a.Eng.Wait(tctx, name)
	if tctx.Err() != nil {
		return fmt.Errorf("o setup passou de %s e foi interrompido", setupTimeout)
	}
	<-done
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("the setup exited with code %d", code)
	}
	return nil
}

// build builds a Dockerfile service with the classic builder (capped); a
// Dockerfile that needs BuildKit syntax is retried with BuildKit.
func (a *Agent) build(ctx context.Context, image, work string, logf func(string, ...any)) error {
	var buf bytes.Buffer
	if err := TarDir(work, &buf); err != nil {
		return err
	}
	if buf.Len() > maxExtract {
		return errors.New("contexto de build grande demais")
	}
	logf("Construindo a imagem (Dockerfile, %.1f MB de contexto)", float64(buf.Len())/(1<<20))
	opt := BuildOptions{MemoryBytes: int64(a.Cfg.BuildMemoryMB) << 20, CPUs: a.Cfg.BuildCPUs, Network: networkName}
	tctx, cancel := context.WithTimeout(ctx, setupTimeout)
	defer cancel()
	err := a.Eng.Build(tctx, image, bytes.NewReader(buf.Bytes()), opt, func(s string) { logf("  %s", s) })
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "buildkit") {
		logf("Este Dockerfile precisa do BuildKit: construindo de novo com ele (sem limite de CPU)")
		err = a.Eng.Build(tctx, image, bytes.NewReader(buf.Bytes()), BuildOptions{BuildKit: true}, func(s string) { logf("  %s", s) })
	}
	return err
}

// recreate applies new settings to the current deployment.
func (a *Agent) recreate(ctx context.Context, id string, logf func(string, ...any)) error {
	app := a.snapshot(id)
	if app == nil || app.Deployed == nil {
		return nil
	}
	logf("Applying the settings")
	if app.Mode == ModeCompose {
		if err := a.canStart(app); err != nil {
			return err
		}
		_, ports, err := a.composeUp(ctx, app, a.workDir(app, app.Deployed), false, logf)
		if err != nil {
			return err
		}
		a.mu.Lock()
		if live := a.find(id); live != nil {
			live.Ports = ports
			a.rt(id).composeUp = true
		}
		a.mu.Unlock()
		if err := a.composeCheck(ctx, app, logf); err != nil {
			return err
		}
	} else if err := a.activate(ctx, app, app.Deployed, logf); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if live := a.find(id); live != nil {
		live.Desired = Running
	}
	return a.saveLocked()
}

// activate swaps a single-container service's container for one built from
// dep and checks that it stays up for a moment.
func (a *Agent) activate(ctx context.Context, app *App, dep *Deployment, logf func(string, ...any)) error {
	if err := a.canStart(app); err != nil {
		return err
	}
	spec, err := a.spec(app, dep)
	if err != nil {
		return err
	}
	name := containerName(app.ID)
	if err := a.Eng.Stop(ctx, name, stopGrace); err != nil {
		return err
	}
	if err := a.Eng.Remove(ctx, name); err != nil {
		return err
	}
	if err := a.Eng.Create(ctx, name, spec); err != nil {
		return err
	}
	if err := a.Eng.Start(ctx, name); err != nil {
		return err
	}
	logf("Started; checking that it is still running…")
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(a.settle()):
	}
	st, err := a.Eng.Inspect(ctx, name)
	if err != nil {
		return err
	}
	if st == nil || !st.Running {
		a.mu.Lock()
		a.rt(app.ID).container = st
		a.observeExitLocked(app.ID, name, st)
		a.mu.Unlock()
		_ = a.Eng.Logs(ctx, name, LogOptions{Tail: 30}, func(_ int, s string) { logf("  %s", s) })
		if st != nil {
			return errors.New(exitReason(st))
		}
		return errors.New("the container disappeared while checking the start")
	}
	a.mu.Lock()
	a.rt(app.ID).container = st
	a.mu.Unlock()
	a.sample(ctx, app.ID, []string{name}) // usage shows at once, not after the next tick
	return nil
}

// settleTime is how long a fresh container must survive (short in tests).
func (a *Agent) settle() time.Duration { return a.settleTime }

// canStart enforces the shared limits before a service (re)starts.
func (a *Agent) canStart(app *App) error {
	if free := a.memFreeMB(); free > 0 && free < minFreeMB {
		return fmt.Errorf("little free memory on the server (%d MB): the service was not started", free)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	total := 0
	for _, o := range a.apps {
		if o.ID == app.ID {
			total += memBudget(app)
		} else if o.Desired == Running && o.Deployed != nil {
			total += memBudget(o)
		}
	}
	if total > a.Cfg.TotalMemoryMB {
		return fmt.Errorf("the running services would add up to %d MB of memory, above the total limit of %d MB", total, a.Cfg.TotalMemoryMB)
	}
	return nil
}

// memBudget is a service's share of the total: its limit per container.
func memBudget(app *App) int {
	n := 1
	if app.Mode == ModeCompose && app.Deployed != nil && len(app.Deployed.Services) > 0 {
		n = len(app.Deployed.Services)
	}
	return app.MemoryMB * n
}

// bindIP is where an exposure publishes ports.
func (a *Agent) bindIP(exposure string) (string, error) {
	switch exposure {
	case ExposeLocal:
		return "127.0.0.1", nil
	case ExposeTailnet:
		ip := a.tailnetIP()
		if ip == "" {
			return "", errors.New("tailscale is not active on this server")
		}
		return ip, nil
	}
	return "0.0.0.0", nil
}

// scriptSpec is the base container of a script service: the release at
// /app, persistent /data and a /cache for package managers.
func (a *Agent) scriptSpec(app *App, dep *Deployment) Spec {
	user := "1000:1000"
	if app.Root {
		user = "0:0"
	}
	wd := "/app"
	if app.Dir != "" {
		wd += "/" + app.Dir
	}
	base := a.appDir(app.ID)
	return Spec{
		Image: dep.Image, WorkingDir: wd, User: user,
		Binds: []string{a.releaseDir(app.ID, dep.Release) + ":/app", filepath.Join(base, "data") + ":/data", filepath.Join(base, "cache") + ":/cache"},
		Env:   a.envFor(app, dep, true),
	}
}

// spec is the service container for a deployment.
func (a *Agent) spec(app *App, dep *Deployment) (Spec, error) {
	var s Spec
	if app.Mode == ModeScript {
		s = a.scriptSpec(app, dep)
		start := app.Start
		if strings.TrimSpace(start) == "" && app.Runtime == "static" {
			start = "exec httpd -f -v -p ${PORT:-8080} -h ."
		}
		s.Cmd = []string{"sh", "-c", start}
	} else {
		s = Spec{Image: dep.Image, Env: a.envFor(app, dep, false),
			Binds: []string{filepath.Join(a.appDir(app.ID), "data") + ":/data"}}
		if strings.TrimSpace(app.Start) != "" {
			s.Cmd = []string{"sh", "-c", app.Start}
		}
	}
	ip, err := a.bindIP(app.Exposure)
	if err != nil && len(app.Ports) > 0 {
		return s, err
	}
	s.Ports = map[string]HostAddr{}
	for _, p := range app.Ports {
		if p.Host == 0 {
			return s, fmt.Errorf("port %d has no server port", p.Container)
		}
		s.Ports[fmt.Sprintf("%d/%s", p.Container, p.Proto)] = HostAddr{IP: ip, Port: p.Host}
	}
	s.Memory = int64(app.MemoryMB) << 20
	s.NanoCPUs = int64(app.CPUs * 1e9)
	s.CPUShares = 256 // the NVR's containers keep Docker's 1024
	s.Pids = 512
	s.Network, s.Alias = networkName, app.ID
	s.Labels = map[string]string{labelApp: app.ID, "sentinela.version": dep.Version}
	return s, nil
}

// env is the service's environment: Sentinela's defaults, then the user's
// variables (which win).
func (a *Agent) env(app *App, script bool) []string {
	return a.envFor(app, app.Deployed, script)
}

func (a *Agent) envFor(app *App, dep *Deployment, script bool) []string {
	vars := map[string]string{}
	var order []string
	set := func(k, v string) {
		if _, ok := vars[k]; !ok {
			order = append(order, k)
		}
		vars[k] = v
	}
	set("SENTINELA_APP", app.ID)
	if app.Mode != ModeCompose {
		set("DATA_DIR", "/data")
	}
	if len(app.Ports) > 0 && app.Mode != ModeCompose {
		set("PORT", strconv.Itoa(app.Ports[0].Container))
	}
	if script {
		for _, kv := range [][2]string{{"HOME", "/cache/home"}, {"XDG_CACHE_HOME", "/cache"}, {"npm_config_cache", "/cache/npm"},
			{"PIP_CACHE_DIR", "/cache/pip"}, {"GOPATH", "/cache/go"}, {"GOCACHE", "/cache/go-build"}, {"BUN_INSTALL_CACHE_DIR", "/cache/bun"},
			{"DENO_DIR", "/cache/deno"}} {
			set(kv[0], kv[1])
		}
	}
	if dep != nil {
		for _, e := range dep.FileEnv {
			set(e.Key, e.Value)
		}
	}
	a.mu.Lock()
	secrets := slices.Clone(a.vaults[app.ID].Vars)
	a.mu.Unlock()
	for _, e := range secrets {
		set(e.Key, e.Value)
	}
	for _, e := range app.Env {
		set(e.Key, e.Value)
	}
	out := make([]string, 0, len(order))
	for _, k := range order {
		out = append(out, k+"="+vars[k])
	}
	return out
}

// ---------- supervision ----------

// Reconcile brings every service to its desired state, restarts crashed
// ones (with a pause after repeated crashes), samples resource usage and
// publishes the internet-exposed ports for the host firewall.
func (a *Agent) Reconcile(ctx context.Context) {
	a.mu.Lock()
	ids := make([]string, 0, len(a.apps))
	for _, app := range a.apps {
		ids = append(ids, app.ID)
	}
	a.mu.Unlock()
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		a.reconcileOne(ctx, id)
	}
	a.writePublicPorts()
}

// noteRestart records a restart and reports whether the service may be
// restarted now (false while paused after a crash loop). Callers hold a.mu.
func (a *Agent) noteRestart(r *svcState, id string, now time.Time) bool {
	if now.Before(r.pauseTill) {
		return false
	}
	if !r.pauseTill.IsZero() {
		r.pauseTill, r.err = time.Time{}, ""
	}
	cut := now.Add(-crashWindow)
	r.restarts = slices.DeleteFunc(r.restarts, func(t time.Time) bool { return t.Before(cut) })
	r.restarts = append(r.restarts, now)
	if len(r.restarts) > crashLimit {
		r.pauseTill = now.Add(crashPause)
		r.restarts = nil
		r.err = fmt.Sprintf("parou %d vezes em %s; nova tentativa em %s", crashLimit, crashWindow, crashPause)
		a.Log.Warn("apps: crash loop, pausing", "app", id)
		a.eventLocked(id, "pause", r.err)
		return false
	}
	r.restartCount++
	a.eventLocked(id, "restart", fmt.Sprintf("Automatic start attempt #%d", r.restartCount))
	return true
}

func (a *Agent) reconcileOne(ctx context.Context, id string) error {
	app := a.snapshot(id)
	if app == nil {
		return errNoApp
	}
	if app.Mode == ModeCompose {
		return a.reconcileCompose(ctx, app)
	}
	name := containerName(id)
	st, err := a.Eng.Inspect(ctx, name)
	now := a.now()
	a.mu.Lock()
	r := a.rt(id)
	busy := a.busyLocked(r)
	if err == nil {
		r.container = st
		if !busy && app.Desired == Running {
			a.observeExitLocked(id, name, st)
		}
	}
	startAfter := a.startAfter
	a.mu.Unlock()
	if err != nil || busy || app.Deployed == nil {
		a.noteErr(id, err)
		return err
	}
	running := st != nil && st.Running
	switch {
	case app.Desired == Running && !running:
		if now.Before(startAfter) {
			return nil
		}
		// Every attempt counts toward the crash-loop pause, so a service that
		// cannot start is not retried every 15 s forever.
		a.mu.Lock()
		ok := a.noteRestart(r, id, now)
		a.mu.Unlock()
		if !ok {
			return nil
		}
		var err error
		if st == nil {
			err = a.activate(ctx, app, app.Deployed, func(string, ...any) {})
		} else if err = a.canStart(app); err == nil {
			err = a.Eng.Start(ctx, name)
		}
		a.noteErr(id, err)
		if now, ierr := a.Eng.Inspect(ctx, name); ierr == nil {
			a.mu.Lock()
			r.container = now
			a.mu.Unlock()
		}
		return err
	case app.Desired == Stopped && running:
		err := a.Eng.Stop(ctx, name, stopGrace)
		a.noteErr(id, err)
		return err
	case running:
		a.sample(ctx, id, []string{name})
	}
	return nil
}

// reconcileCompose keeps a compose project up: once per agent start (after
// a reboot, containers exist but are stopped) it runs `up`; later only
// containers that crashed (non-zero exit, OOM) are started again, so
// finished one-off jobs stay finished.
func (a *Agent) reconcileCompose(ctx context.Context, app *App) error {
	id := app.ID
	members, err := a.projectContainers(ctx, id)
	now := a.now()
	a.mu.Lock()
	r := a.rt(id)
	busy := a.busyLocked(r)
	if err == nil {
		r.members = members
	}
	upDone, startAfter := r.composeUp, a.startAfter
	a.mu.Unlock()
	if err != nil || busy || app.Deployed == nil {
		a.noteErr(id, err)
		return err
	}
	var running []string
	for _, m := range members {
		if m.Running {
			running = append(running, m.Name)
		}
	}
	switch {
	case app.Desired == Stopped:
		for _, n := range running {
			err := a.Eng.Stop(ctx, n, stopGrace)
			a.noteErr(id, err)
			if err != nil {
				return err
			}
		}
		a.mu.Lock()
		r.composeUp = false
		a.mu.Unlock()
		return nil
	case now.Before(startAfter):
		return nil
	case !upDone || len(members) == 0:
		a.mu.Lock()
		ok := a.noteRestart(r, id, now)
		a.mu.Unlock()
		if !ok {
			return nil
		}
		err := a.canStart(app)
		if err == nil {
			_, _, err = a.composeUp(ctx, app, a.workDir(app, app.Deployed), false, func(string, ...any) {})
		}
		a.mu.Lock()
		r.composeUp = err == nil
		a.mu.Unlock()
		a.noteErr(id, err)
		return err
	}
	var crashed []string
	for _, m := range members {
		if m.Running {
			continue
		}
		st, err := a.Eng.Inspect(ctx, m.Name)
		if err != nil || st == nil || st.ExitCode == 0 && !st.OOMKilled {
			continue
		}
		crashed = append(crashed, m.Name)
		a.mu.Lock()
		a.observeExitLocked(id, m.Name, st)
		a.mu.Unlock()
	}
	a.mu.Lock()
	r.crashed = crashed
	a.mu.Unlock()
	for _, name := range crashed {
		a.mu.Lock()
		ok := a.noteRestart(r, id, now)
		a.mu.Unlock()
		if !ok {
			break
		}
		err := a.Eng.Start(ctx, name)
		a.noteErr(id, err)
		if err != nil {
			return err
		}
	}
	a.sample(ctx, id, running)
	return nil
}

func (a *Agent) noteErr(id string, err error) {
	a.rememberError(id, err)
}

// sample reads resource usage of the running containers; CPU% comes from
// two consecutive readings of each.
func (a *Agent) sample(ctx context.Context, id string, names []string) {
	cur := map[string]*Sample{}
	for _, n := range names {
		if s, err := a.Eng.Stats(ctx, n); err == nil {
			cur[n] = s
		}
	}
	a.mu.Lock()
	r := a.rt(id)
	var cpu float64
	var sum Sample
	for n, s := range cur {
		if p := r.samples[n]; p != nil && s.SystemTotal > p.SystemTotal && s.CPUTotal >= p.CPUTotal {
			cpu += float64(s.CPUTotal-p.CPUTotal) / float64(s.SystemTotal-p.SystemTotal) * float64(max(s.OnlineCPUs, 1)) * 100
		}
		sum.MemUsage += s.MemUsage
		sum.MemLimit += s.MemLimit
		sum.NetRx += s.NetRx
		sum.NetTx += s.NetTx
		sum.Pids += s.Pids
	}
	r.samples, r.cpu, r.mem = cur, cpu, sum
	due := a.now().Sub(r.diskAt) > 10*time.Minute
	if due {
		r.diskAt = a.now()
	}
	a.mu.Unlock()
	if due {
		n := dirSize(a.appDir(id), 3*time.Second)
		a.mu.Lock()
		r.disk = n
		a.mu.Unlock()
	}
}

// writePublicPorts lists the host ports open to the internet, for the host
// firewall (scripts/apps-firewall.sh, via a systemd path unit). Every other
// service port only answers private addresses.
func (a *Agent) writePublicPorts() {
	a.mu.Lock()
	var lines []string
	for _, app := range a.apps {
		if app.Exposure != ExposePublic || app.Desired != Running {
			continue
		}
		for _, p := range app.Ports {
			if p.Host != 0 {
				lines = append(lines, fmt.Sprintf("%d/%s", p.Host, p.Proto))
			}
		}
	}
	sort.Strings(lines)
	body := strings.Join(lines, "\n")
	changed := body != a.lastPublic
	a.lastPublic = body
	a.mu.Unlock()
	p := filepath.Join(a.Cfg.StateDir, "public-ports")
	if _, err := os.Stat(p); err == nil && !changed {
		return
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, []byte(body+"\n"), 0o644); err == nil {
		_ = os.Rename(tmp, p)
	}
}

// ---------- host facts ----------

func memAvailableMB() int {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "MemAvailable:"); ok {
			kb, _ := strconv.Atoi(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(v), "kB")))
			return kb / 1024
		}
	}
	return 0
}

func hostBootID() string {
	b, _ := os.ReadFile("/proc/sys/kernel/random/boot_id")
	return strings.TrimSpace(string(b))
}

func hostUptime() time.Duration {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	f, _ := strconv.ParseFloat(strings.Fields(string(b) + " 0")[0], 64)
	return time.Duration(f * float64(time.Second))
}

// portFree reports whether nothing on the host listens on port (the agent
// runs in the host network namespace).
func portFree(port int, proto string) bool {
	addr := net.JoinHostPort("", strconv.Itoa(port))
	if proto == "udp" {
		c, err := net.ListenPacket("udp", addr)
		if err != nil {
			return false
		}
		c.Close()
		return true
	}
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return false
	}
	l.Close()
	return true
}

// ifaceIPv4 returns the first IPv4 address of interfaces accepted by keep.
func ifaceIPv4(keep func(name string, ip net.IP) bool) string {
	ifs, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, in := range ifs {
		if in.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, _ := in.Addrs()
		for _, ad := range addrs {
			if n, ok := ad.(*net.IPNet); ok && n.IP.To4() != nil && keep(in.Name, n.IP) {
				return n.IP.String()
			}
		}
	}
	return ""
}

func tailnetIPv4() string {
	return ifaceIPv4(func(name string, _ net.IP) bool { return name == "tailscale0" })
}

func lanIPv4() string {
	return ifaceIPv4(func(name string, ip net.IP) bool {
		for _, p := range []string{"docker", "br-", "veth", "tailscale", "sn-", "lo"} {
			if strings.HasPrefix(name, p) {
				return false
			}
		}
		return ip.IsPrivate()
	})
}

// dirSize sums file sizes under dir, giving up after limit.
func dirSize(dir string, limit time.Duration) int64 {
	var n int64
	deadline := time.Now().Add(limit)
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fs.SkipAll
		}
		if !d.IsDir() {
			if info, err := d.Info(); err == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	if len(l) > 120 {
		l = l[:120] + "…"
	}
	return l
}

// ring keeps the last n lines of a deploy log.
type ring struct {
	mu  sync.Mutex
	n   int
	buf []string
}

func newRing(n int) *ring { return &ring{n: n} }

func (r *ring) add(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(s) > 2000 {
		s = s[:2000] + "…"
	}
	r.buf = append(r.buf, s)
	if len(r.buf) > r.n {
		r.buf = r.buf[len(r.buf)-r.n:]
	}
}

func (r *ring) reset() {
	r.mu.Lock()
	r.buf = nil
	r.mu.Unlock()
}

func (r *ring) lines() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.buf)
}
