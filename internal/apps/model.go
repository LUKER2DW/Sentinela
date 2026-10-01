// Package apps is Sentinela's small self-hosted services platform
// (ADR-0021): GitHub repositories deployed as capped Docker containers next
// to the NVR. The agent (`sentinela apps`) is the only Sentinela process
// with the Docker socket; the API reaches it on loopback with a token and
// never touches Docker itself. Recording never depends on any of this.
package apps

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"
)

// Modes: a "script" service runs a runtime image with the source mounted
// at /app (setup once per deploy, start on every run); a "dockerfile"
// service builds the source's own Dockerfile; a "compose" service runs the
// source's Compose file, rewritten to the platform's limits and ports.
const (
	ModeScript     = "script"
	ModeDockerfile = "dockerfile"
	ModeCompose    = "compose"
)

// Sources: a GitHub repository, or a folder on the server (under
// apps.local_root, e.g. a project copied to the Nuvem with its .git).
const (
	SourceGitHub = "github"
	SourceLocal  = "local"
)

// Exposure says who can reach a service's ports.
const (
	ExposeLocal   = "local"   // 127.0.0.1 only (bots, workers, SSH tunnels)
	ExposeLAN     = "lan"     // home network and the tailnet (ACL permitting)
	ExposeTailnet = "tailnet" // the server's Tailscale address only
	ExposePublic  = "public"  // also the internet, through a router forward
)

// Desired states.
const (
	Running = "running"
	Stopped = "stopped"
)

// App is one service, as stored in the agent's state file.
type App struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Source string `json:"source"`
	// Repo is "owner/name" on GitHub; Ref a branch, tag or commit.
	Repo string `json:"repo,omitempty"`
	Ref  string `json:"ref,omitempty"`
	// Path is the folder of a local source, relative to apps.local_root.
	Path string `json:"path,omitempty"`
	// Dir is an optional subdirectory of the source (monorepos).
	Dir     string `json:"dir,omitempty"`
	Mode    string `json:"mode"`
	Runtime string `json:"runtime,omitempty"`
	// ComposeFile is the Compose file of a compose service, relative to
	// Dir ("" = the usual names).
	ComposeFile string `json:"compose_file,omitempty"`
	// Setup runs once per deploy (install, build); Start on every run.
	Setup string `json:"setup,omitempty"`
	Start string `json:"start,omitempty"`

	Env      []EnvVar `json:"env"`
	Ports    []Port   `json:"ports"`
	Exposure string   `json:"exposure"`

	MemoryMB int     `json:"memory_mb"`
	CPUs     float64 `json:"cpus"`
	// Autostart: start again after the server reboots.
	Autostart bool `json:"autostart"`
	// Root runs the process as root inside the container (still without
	// capabilities). Off: uid 1000.
	Root bool `json:"root"`

	Desired  string      `json:"desired"`
	Deployed *Deployment `json:"deployed,omitempty"`
	// NeedsDeploy: the source settings changed since the last deploy.
	NeedsDeploy bool      `json:"needs_deploy"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// EnvVar is one environment variable. Secret values are never returned
// by the API once stored.
type EnvVar struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Secret bool   `json:"secret,omitempty"`
}

// Port publishes a container port on a host port of the configured range.
// Service names the Compose service it belongs to (compose mode).
type Port struct {
	Service   string `json:"service,omitempty"`
	Container int    `json:"container"`
	Host      int    `json:"host"`
	Proto     string `json:"proto"`
}

func (p Port) key() string { return fmt.Sprintf("%s:%d/%s", p.Service, p.Container, p.Proto) }

// Deployment is what currently runs. Commit is empty for a local folder
// without git; Version then names the snapshot.
type Deployment struct {
	Commit  string `json:"commit"`
	Message string `json:"message,omitempty"`
	Ref     string `json:"ref"`
	Version string `json:"version"`
	// Services of a compose deployment.
	Services []string  `json:"services,omitempty"`
	Image    string    `json:"image"`
	Release  string    `json:"release"` // directory under releases/
	At       time.Time `json:"at"`
}

var (
	idPattern   = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}$`)
	repoPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})/[A-Za-z0-9._-]{1,100}$`)
	refPattern  = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,200}$`)
	// pathPattern: a relative path of ordinary names (local folders, Dir).
	pathPattern = regexp.MustCompile(`^[^\\:*?"<>|\x00]{1,400}$`)
	envKey      = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
)

// Limits are the caps an app must fit (from config.Apps).
type Limits struct {
	MaxMemoryMB int
	MaxCPUs     float64
	PortFirst   int
	PortLast    int
	Runtimes    map[string]string
	Public      bool // a public host is configured
	Local       bool // a local root is configured
}

const (
	maxScript = 8 << 10
	maxEnv    = 64
	maxEnvVal = 8 << 10
	maxPorts  = 5
)

// NormalizeRepo accepts "owner/name", a github.com URL or an SSH remote and
// returns "owner/name".
func NormalizeRepo(s string) string {
	s = strings.TrimSpace(s)
	for _, p := range []string{"https://github.com/", "http://github.com/", "github.com/", "git@github.com:"} {
		if after, ok := strings.CutPrefix(s, p); ok {
			s = after
			break
		}
	}
	s = strings.TrimSuffix(strings.TrimSuffix(s, "/"), ".git")
	return s
}

// Validate checks an app's settings against the limits. It normalizes
// fields in place (trimmed strings, default protocol).
func (a *App) Validate(l Limits) error {
	var errs []error
	bad := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	a.Name = strings.TrimSpace(a.Name)
	a.Dir = cleanRel(a.Dir)
	if !idPattern.MatchString(a.ID) {
		bad("id: 2-31 lower-case letters, digits or '-', starting with a letter")
	}
	if a.Name == "" || len([]rune(a.Name)) > 60 {
		bad("name: 1 to 60 characters")
	}
	switch a.Source {
	case "", SourceGitHub:
		a.Source, a.Path = SourceGitHub, ""
		a.Repo = NormalizeRepo(a.Repo)
		a.Ref = strings.TrimSpace(a.Ref)
		if !repoPattern.MatchString(a.Repo) {
			bad("repository: use owner/name of a GitHub repository")
		}
		if a.Ref == "" {
			a.Ref = "main"
		}
		if !refPattern.MatchString(a.Ref) || strings.Contains(a.Ref, "..") {
			bad("branch: invalid name")
		}
	case SourceLocal:
		a.Repo, a.Ref = "", ""
		a.Path = cleanRel(a.Path)
		if !l.Local {
			bad("local folders are not enabled on this server (apps.local_root)")
		}
		if a.Path == "" || !validRel(a.Path) {
			bad("folder: a path inside the local root")
		}
	default:
		bad("source: github or local")
	}
	if a.Dir != "" && !validRel(a.Dir) {
		bad("subfolder: a relative path inside the source")
	}
	a.ComposeFile = cleanRel(a.ComposeFile)
	if a.ComposeFile != "" && !validRel(a.ComposeFile) {
		bad("compose file: a relative path")
	}
	switch a.Mode {
	case ModeScript:
		if _, ok := l.Runtimes[a.Runtime]; !ok {
			bad("runtime: unknown %q", a.Runtime)
		}
		if strings.TrimSpace(a.Start) == "" && a.Runtime != "static" {
			bad("start command: required")
		}
	case ModeDockerfile:
		a.Runtime, a.Setup = "", ""
	case ModeCompose:
		// Ports come from the Compose file at each deploy.
		a.Runtime, a.Setup, a.Start = "", "", ""
	default:
		bad("mode: script, dockerfile or compose")
	}
	if a.Mode != ModeCompose {
		a.ComposeFile = ""
		for i := range a.Ports {
			a.Ports[i].Service = ""
		}
	}
	if len(a.Setup) > maxScript || len(a.Start) > maxScript {
		bad("scripts: at most %d bytes each", maxScript)
	}
	if len(a.Env) > maxEnv {
		bad("environment: at most %d variables", maxEnv)
	}
	keys := map[string]bool{}
	for _, e := range a.Env {
		if !envKey.MatchString(e.Key) || keys[e.Key] {
			bad("environment: invalid or repeated name %q", e.Key)
		}
		keys[e.Key] = true
		if len(e.Value) > maxEnvVal || strings.ContainsRune(e.Value, 0) {
			bad("environment: value of %s too long or invalid", e.Key)
		}
	}
	if len(a.Ports) > maxPorts {
		bad("ports: at most %d", maxPorts)
	}
	seen := map[string]bool{}
	for i := range a.Ports {
		p := &a.Ports[i]
		if p.Proto == "" {
			p.Proto = "tcp"
		}
		if p.Proto != "tcp" && p.Proto != "udp" {
			bad("ports: protocol tcp or udp")
		}
		if p.Container < 1 || p.Container > 65535 {
			bad("ports: container port 1..65535")
		}
		if p.Host != 0 && (p.Host < l.PortFirst || p.Host > l.PortLast) {
			bad("ports: host port must be in %d..%d", l.PortFirst, l.PortLast)
		}
		if seen[p.key()] {
			bad("ports: %d/%s repeated", p.Container, p.Proto)
		}
		seen[p.key()] = true
	}
	switch a.Exposure {
	case "":
		a.Exposure = ExposeLAN
	case ExposeLocal, ExposeLAN, ExposeTailnet:
	case ExposePublic:
		if !l.Public {
			bad("exposure: no public address configured (apps.public_host)")
		}
	default:
		bad("exposure: local, lan, tailnet or public")
	}
	if a.MemoryMB < 32 || a.MemoryMB > l.MaxMemoryMB {
		bad("memory: 32..%d MB", l.MaxMemoryMB)
	}
	if a.CPUs < 0.05 || a.CPUs > l.MaxCPUs {
		bad("cpu: 0.05..%.2f", l.MaxCPUs)
	}
	if a.Desired != Running && a.Desired != Stopped {
		a.Desired = Stopped
	}
	return errors.Join(errs...)
}

// cleanRel normalizes a relative slash path ("" for none).
func cleanRel(p string) string {
	p = strings.Trim(strings.ReplaceAll(strings.TrimSpace(p), "\\", "/"), "/")
	if p == "" {
		return ""
	}
	return strings.TrimPrefix(path.Clean("/"+p), "/")
}

// validRel: a clean relative path that stays inside (no "..", no Git
// internals).
func validRel(p string) bool {
	if !pathPattern.MatchString(p) || p == ".." || strings.HasPrefix(p, "../") {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." || part == ".git" {
			return false
		}
	}
	return true
}

// sourceChanged reports whether b needs a new deploy compared with a.
func sourceChanged(a, b *App) bool {
	return a.Source != b.Source || a.Repo != b.Repo || a.Ref != b.Ref || a.Path != b.Path || a.Dir != b.Dir ||
		a.Mode != b.Mode || a.Runtime != b.Runtime || a.Setup != b.Setup || a.ComposeFile != b.ComposeFile
}

// masked returns a copy safe to show: secret values are blanked.
func (a App) masked() App {
	env := make([]EnvVar, len(a.Env))
	for i, e := range a.Env {
		if e.Secret {
			e.Value = ""
		}
		env[i] = e
	}
	a.Env = env
	a.Ports = append([]Port(nil), a.Ports...)
	return a
}

// mergeSecrets keeps stored secret values the client left blank (it never
// receives them).
func mergeSecrets(next []EnvVar, prev []EnvVar) {
	old := map[string]string{}
	for _, e := range prev {
		if e.Secret {
			old[e.Key] = e.Value
		}
	}
	for i := range next {
		if next[i].Secret && next[i].Value == "" {
			next[i].Value = old[next[i].Key]
		}
	}
}
