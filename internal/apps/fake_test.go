package apps

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LUKER2DW/sentinela/internal/config"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// fakeEngine records what the agent asks Docker to do.
type fakeEngine struct {
	mu         sync.Mutex
	images     map[string]bool
	containers map[string]*fakeContainer
	calls      []string
	// setupExit is the exit code of "-setup" containers.
	setupExit int
	// crash: service containers stop right after starting.
	crash bool
	// compose: stdout of `docker compose config`, and the project members.
	composeConfig string
	members       []Summary
	builds        []BuildOptions
}

type fakeContainer struct {
	spec    Spec
	running bool
	exit    int
}

func newFakeEngine() *fakeEngine {
	return &fakeEngine{images: map[string]bool{}, containers: map[string]*fakeContainer{}}
}

func (f *fakeEngine) log(s string) {
	f.mu.Lock()
	f.calls = append(f.calls, s)
	f.mu.Unlock()
}

func (f *fakeEngine) called(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func (f *fakeEngine) exact(call string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == call {
			n++
		}
	}
	return n
}

func (f *fakeEngine) Version(context.Context) (string, error) { return "29.0-test", nil }
func (f *fakeEngine) EnsureNetwork(context.Context, string, string, string) error {
	f.log("network")
	return nil
}
func (f *fakeEngine) ImageExists(_ context.Context, ref string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.images[ref], nil
}
func (f *fakeEngine) Pull(_ context.Context, ref string, _ func(string)) error {
	f.log("pull " + ref)
	f.mu.Lock()
	f.images[ref] = true
	f.mu.Unlock()
	return nil
}
func (f *fakeEngine) Build(_ context.Context, tag string, _ io.Reader, opt BuildOptions, _ func(string)) error {
	f.log("build " + tag)
	f.mu.Lock()
	f.images[tag] = true
	f.builds = append(f.builds, opt)
	f.mu.Unlock()
	return nil
}
func (f *fakeEngine) RemoveImage(_ context.Context, ref string) error {
	f.log("rmi " + ref)
	return nil
}
func (f *fakeEngine) Create(_ context.Context, name string, s Spec) error {
	f.log("create " + name)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.containers[name] = &fakeContainer{spec: s}
	return nil
}
func (f *fakeEngine) Start(_ context.Context, name string) error {
	f.log("start " + name)
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.containers[name]
	if c == nil {
		return errNotFound
	}
	helper := strings.HasSuffix(name, "-setup") || strings.HasSuffix(name, "-compose")
	c.running = !helper && !f.crash
	if f.crash && !helper {
		c.exit = 1
	}
	if strings.HasSuffix(name, "-setup") {
		c.exit = f.setupExit
	}
	return nil
}
func (f *fakeEngine) Stop(_ context.Context, name string, _ time.Duration) error {
	f.log("stop " + name)
	f.mu.Lock()
	defer f.mu.Unlock()
	if c := f.containers[name]; c != nil {
		c.running = false
	}
	for i := range f.members {
		if f.members[i].Name == name {
			f.members[i].Running = false
		}
	}
	return nil
}
func (f *fakeEngine) Remove(_ context.Context, name string) error {
	f.log("rm " + name)
	f.mu.Lock()
	delete(f.containers, name)
	f.mu.Unlock()
	return nil
}
func (f *fakeEngine) Wait(_ context.Context, name string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c := f.containers[name]; c != nil {
		return c.exit, nil
	}
	return 0, nil
}
func (f *fakeEngine) Logs(_ context.Context, name string, _ LogOptions, line func(int, string)) error {
	f.mu.Lock()
	c := f.containers[name]
	cfg := f.composeConfig
	f.mu.Unlock()
	if c != nil && strings.HasSuffix(name, "-compose") && slices.Contains(c.spec.Cmd, "config") {
		for _, l := range strings.Split(cfg, "\n") {
			line(1, l)
		}
		return nil
	}
	line(1, "2026-10-01T10:00:00.000000000Z hello from "+name)
	return nil
}
func (f *fakeEngine) Inspect(_ context.Context, name string) (*ContainerState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range f.members {
		if m.Name == name {
			exit := 0
			if !m.Running {
				exit = 1
			}
			return &ContainerState{Running: m.Running, ExitCode: exit}, nil
		}
	}
	c := f.containers[name]
	if c == nil {
		return nil, nil
	}
	return &ContainerState{Running: c.running, ExitCode: c.exit, Status: "exited"}, nil
}
func (f *fakeEngine) Stats(context.Context, string) (*Sample, error) {
	return &Sample{CPUTotal: 100, SystemTotal: 1000, OnlineCPUs: 4, MemUsage: 50 << 20, MemLimit: 256 << 20}, nil
}
func (f *fakeEngine) List(context.Context, string) ([]Summary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.members), nil
}

// fakeSource serves a fixed tree as every snapshot.
type fakeSource struct {
	files map[string]string
	sha   string
	err   error
}

func (s *fakeSource) Resolve(context.Context, *App) (Version, error) {
	if s.err != nil {
		return Version{}, s.err
	}
	return Version{Commit: s.sha, Message: "msg", Branch: "main", Label: s.sha[:12]}, nil
}

func (s *fakeSource) Snapshot(_ context.Context, _ *App, _ Version, dir string) error {
	for p, body := range s.files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func testAppsCfg(t *testing.T) config.Apps {
	t.Helper()
	cfg, err := config.Parse([]byte("apps: {enabled: true, public_host: 203.0.113.7}\n"), func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	a := cfg.Apps
	a.StateDir = t.TempDir()
	return a
}

// newTestAgent returns an agent with fakes and instant settling.
func newTestAgent(t *testing.T) (*Agent, *fakeEngine, *fakeSource) {
	t.Helper()
	settleTime = 0
	eng := newFakeEngine()
	src := &fakeSource{sha: "0123456789abcdef0123456789abcdef01234567", files: map[string]string{"package.json": `{"scripts":{"start":"node x"}}`}}
	a := NewAgent(testAppsCfg(t), eng, map[string]Source{SourceGitHub: src}, quiet)
	a.memFreeMB = func() int { return 2000 }
	a.bootID = func() string { return "boot-1" }
	a.uptime = func() time.Duration { return time.Hour }
	a.portFree = func(int, string) bool { return true }
	a.tailnetIP = func() string { return "100.64.0.9" }
	a.lanIP = func() string { return "192.168.1.10" }
	if err := a.Load(); err != nil {
		t.Fatal(err)
	}
	return a, eng, src
}

func nodeApp() App {
	return App{Name: "Meu Bot", Source: SourceGitHub, Repo: "https://github.com/ana/bot.git", Mode: ModeScript, Runtime: "node",
		Setup: "npm ci", Start: "npm start", Ports: []Port{{Container: 3000}}, Env: []EnvVar{{Key: "TOKEN", Value: "s3cret", Secret: true}}}
}

// runQueued runs the jobs waiting in the queue, as the worker would.
func runQueued(t *testing.T, a *Agent) {
	t.Helper()
	for {
		select {
		case id := <-a.jobs:
			a.runJob(context.Background(), id)
		default:
			return
		}
	}
}
