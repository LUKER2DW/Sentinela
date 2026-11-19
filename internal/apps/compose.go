package apps

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Compose services (ADR-0021). The Compose file is first normalized by
// `docker compose config` (env interpolation, extends, relative paths made
// absolute), then checked and rewritten here, and only the rewritten file
// is ever run. The docker CLI runs in a throwaway container of the
// official CLI image, with the socket and the service's folder mounted at
// their host paths; Sentinela's own image carries no Docker CLI.

// projectName is the Compose project of a service.
func projectName(id string) string { return "sapp-" + id }

// defaultCaps are Docker's default capabilities: cap_add may only re-add
// one of these (after a cap_drop), never grant more.
var defaultCaps = []string{"AUDIT_WRITE", "CHOWN", "DAC_OVERRIDE", "FOWNER", "FSETID", "KILL", "MKNOD",
	"NET_BIND_SERVICE", "NET_RAW", "SETFCAP", "SETGID", "SETPCAP", "SETUID", "SYS_CHROOT"}

// composePlan is a normalized Compose project being rewritten.
type composePlan struct {
	doc      map[string]any
	services []string
	ports    []portRef
	// hostPaths are the bind and secret/config sources, all inside an
	// allowed folder; checkHostPaths refuses symlinks among them.
	hostPaths []string
	// bindDirs are the bind sources the Docker daemon would create when
	// missing; the agent creates them first, as real folders it owns.
	bindDirs []string
	// builds are the services Compose builds from source (`build:`).
	builds []string
}

// portRef is one published port entry and the platform port it maps to.
type portRef struct {
	entry map[string]any
	port  Port
}

// planCompose parses normalized Compose JSON and refuses anything that
// would reach the host: privileged mode, host namespaces, devices, extra
// capabilities, binds outside allowed, foreign or external networks and
// volumes. allowed are the folders binds may come from.
func planCompose(raw []byte, allowed []string) (*composePlan, error) {
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("compose: %w", err)
	}
	services, _ := doc["services"].(map[string]any)
	if len(services) == 0 {
		return nil, errors.New("compose: no service in the file")
	}
	p := &composePlan{doc: doc}
	var errs []error
	bad := func(svc, format string, args ...any) {
		errs = append(errs, fmt.Errorf("%s: "+format, append([]any{svc}, args...)...))
	}
	inside := func(src string) bool {
		src = path.Clean(filepath.ToSlash(src))
		for _, a := range allowed {
			a = path.Clean(filepath.ToSlash(a))
			if src == a || strings.HasPrefix(src, a+"/") {
				return true
			}
		}
		return false
	}
	for name := range services {
		p.services = append(p.services, name)
	}
	sort.Strings(p.services)
	for _, name := range p.services {
		s, ok := services[name].(map[string]any)
		if !ok {
			bad(name, "invalid format")
			continue
		}
		if v, _ := s["privileged"].(bool); v {
			bad(name, "privileged is not allowed")
		}
		for _, k := range []string{"network_mode", "pid", "ipc", "userns_mode", "uts", "cgroup", "cgroup_parent", "runtime", "isolation"} {
			v, _ := s[k].(string)
			if v == "" || k == "ipc" && (v == "private" || v == "shareable") || k == "network_mode" && v == "none" {
				continue
			}
			bad(name, "%s: %q is not allowed", k, v)
		}
		for _, k := range []string{"devices", "device_cgroup_rules", "gpus"} {
			if v, ok := s[k]; ok && v != nil {
				if l, isList := v.([]any); !isList || len(l) > 0 {
					bad(name, "%s is not allowed", k)
				}
			}
		}
		for _, c := range strList(s["cap_add"]) {
			if !slices.Contains(defaultCaps, strings.TrimPrefix(strings.ToUpper(c), "CAP_")) {
				bad(name, "cap_add %s is not allowed", c)
			}
		}
		for _, o := range strList(s["security_opt"]) {
			if l := strings.ToLower(o); strings.Contains(l, "unconfined") || strings.Contains(l, "disable") {
				bad(name, "security_opt %q is not allowed", o)
			}
		}
		if sys, ok := s["sysctls"].(map[string]any); ok {
			for k := range sys {
				if !strings.HasPrefix(k, "net.") {
					bad(name, "sysctl %s is not allowed", k)
				}
			}
		}
		for _, v := range strList(s["volumes_from"]) {
			if strings.HasPrefix(v, "container:") {
				bad(name, "volumes_from %q is not allowed", v)
			}
		}
		if b, ok := s["build"].(map[string]any); ok {
			p.builds = append(p.builds, name)
			if n, _ := b["network"].(string); n == "host" {
				bad(name, "build with host network is not allowed")
			}
			if v, _ := b["privileged"].(bool); v {
				bad(name, "build privileged is not allowed")
			}
			if e := strList(b["entitlements"]); len(e) > 0 {
				bad(name, "build entitlements are not allowed")
			}
		}
		vols, _ := s["volumes"].([]any)
		for _, v := range vols {
			m, ok := v.(map[string]any)
			if !ok {
				continue
			}
			typ, _ := m["type"].(string)
			src, _ := m["source"].(string)
			switch typ {
			case "volume", "tmpfs", "":
			case "bind":
				if strings.Contains(src, "docker.sock") || !inside(src) {
					bad(name, "folder %q is outside the project; use a named volume", src)
				} else {
					p.hostPaths = append(p.hostPaths, src)
					if b, _ := m["bind"].(map[string]any); b == nil || b["create_host_path"] != false {
						p.bindDirs = append(p.bindDirs, src)
					}
				}
			default:
				bad(name, "volume of type %q is not allowed", typ)
			}
		}
		ports, _ := s["ports"].([]any)
		kept := ports[:0]
		for _, e := range ports {
			m, ok := e.(map[string]any)
			if !ok {
				continue
			}
			pub := fmt.Sprint(m["published"])
			if m["published"] == nil || pub == "" || pub == "0" {
				continue // not published on a fixed port: stays internal
			}
			if strings.Contains(pub, "-") {
				bad(name, "port ranges (%s) are not supported", pub)
				continue
			}
			target, err := strconv.Atoi(fmt.Sprint(m["target"]))
			if err != nil || target < 1 || target > 65535 {
				bad(name, "invalid port %v", m["target"])
				continue
			}
			proto, _ := m["protocol"].(string)
			if proto == "" {
				proto = "tcp"
			}
			if proto != "tcp" && proto != "udp" {
				bad(name, "protocol %q is not supported", proto)
				continue
			}
			kept = append(kept, m)
			p.ports = append(p.ports, portRef{entry: m, port: Port{Service: name, Container: target, Proto: proto}})
		}
		if ports != nil {
			s["ports"] = kept
		}
	}
	nets, _ := doc["networks"].(map[string]any)
	for name, v := range nets {
		n, _ := v.(map[string]any)
		if ext, _ := n["external"].(bool); ext {
			bad("networks", "external network %q is not allowed", name)
		}
		if d, _ := n["driver"].(string); d != "" && d != "bridge" {
			bad("networks", "network driver %q of network %q is not allowed", d, name)
		}
	}
	vols, _ := doc["volumes"].(map[string]any)
	for name, v := range vols {
		m, _ := v.(map[string]any)
		if ext, _ := m["external"].(bool); ext {
			bad("volumes", "external volume %q is not allowed", name)
		}
		if d, _ := m["driver"].(string); d != "" && d != "local" {
			bad("volumes", "volume driver %q of volume %q is not allowed", d, name)
		}
		if o, ok := m["driver_opts"].(map[string]any); ok && len(o) > 0 {
			bad("volumes", "driver_opts of volume %q are not allowed", name)
		}
	}
	for _, top := range []string{"secrets", "configs"} {
		items, _ := doc[top].(map[string]any)
		for name, v := range items {
			m, _ := v.(map[string]any)
			if f, _ := m["file"].(string); f != "" && !inside(f) {
				bad(top, "%q points outside the project", name)
			} else if f != "" {
				p.hostPaths = append(p.hostPaths, f)
			}
			if ext, _ := m["external"].(bool); ext {
				bad(top, "external %q is not allowed", name)
			}
		}
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("the Compose file uses disallowed resources:\n%w", errors.Join(errs...))
	}
	return p, nil
}

// checkHostPaths refuses a bind or secret source that is, or passes
// through, a symbolic link below root. The Docker daemon resolves links in
// a bind source on the host, so a repository shipping "data -> /" would
// otherwise mount the host's root filesystem into the container.
func checkHostPaths(root string, paths []string) error {
	root = filepath.Clean(root)
	for _, src := range paths {
		rel, err := filepath.Rel(root, filepath.Clean(filepath.FromSlash(src)))
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("folder %q is outside the project", src)
		}
		cur := root
		for _, part := range strings.Split(rel, string(filepath.Separator)) {
			if part == "." || part == "" {
				continue
			}
			cur = filepath.Join(cur, part)
			info, err := os.Lstat(cur)
			if errors.Is(err, os.ErrNotExist) {
				break // Docker creates missing folders; nothing below can be a link
			}
			if err != nil {
				return fmt.Errorf("compose: %w", err)
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("folder %q goes through a symbolic link; links cannot be mounted", src)
			}
		}
	}
	return nil
}

// prepareBindDirs creates the missing bind source folders below root as real
// directories owned by the agent, so the Docker daemon (which would create
// them itself) never resolves a path component that a running service can
// still plant as a symbolic link: after this, a missing component is no
// longer "trusted because it does not exist". A link met on the way, or a
// file where a folder is needed, is refused.
func prepareBindDirs(root string, dirs []string) error {
	root = filepath.Clean(root)
	for _, src := range dirs {
		rel, err := filepath.Rel(root, filepath.Clean(filepath.FromSlash(src)))
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("folder %q is outside the project", src)
		}
		parts := strings.Split(rel, string(filepath.Separator))
		cur := root
		for i, part := range parts {
			if part == "." || part == "" {
				continue
			}
			cur = filepath.Join(cur, part)
			info, err := os.Lstat(cur)
			if errors.Is(err, os.ErrNotExist) {
				if err = os.Mkdir(cur, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
					return fmt.Errorf("compose: %w", err)
				}
				info, err = os.Lstat(cur)
			}
			if err != nil {
				return fmt.Errorf("compose: %w", err)
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("folder %q goes through a symbolic link; links cannot be mounted", src)
			}
			if !info.IsDir() && i < len(parts)-1 {
				return fmt.Errorf("folder %q goes through a file", src)
			}
		}
	}
	return nil
}

// composeLimits are the caps every service of the project gets.
type composeLimits struct {
	ID       string
	MemoryMB int
	CPUs     float64
	BindIP   string
}

// apply rewrites the plan: assigned host ports and bind address, the
// platform's limits and log caps, no restart policy (the agent
// supervises), and bridge names the host firewall recognizes ("sn-*").
func (p *composePlan) apply(assigned []Port, l composeLimits) ([]byte, error) {
	host := map[string]int{}
	for _, a := range assigned {
		host[a.key()] = a.Host
	}
	for _, r := range p.ports {
		h := host[r.port.key()]
		if h == 0 {
			return nil, fmt.Errorf("porta %d de %s sem porta no servidor", r.port.Container, r.port.Service)
		}
		r.entry["published"] = strconv.Itoa(h)
		r.entry["host_ip"] = l.BindIP
		delete(r.entry, "mode")
	}
	services := p.doc["services"].(map[string]any)
	mem := strconv.Itoa(l.MemoryMB) + "m"
	for _, name := range p.services {
		s := services[name].(map[string]any)
		if d, ok := s["deploy"].(map[string]any); ok {
			delete(d, "resources") // replaced by the limits below
			delete(d, "restart_policy")
		}
		s["mem_limit"] = mem
		s["memswap_limit"] = mem
		s["cpus"] = l.CPUs
		s["cpu_shares"] = 256
		s["pids_limit"] = 512
		s["oom_score_adj"] = 500
		s["restart"] = "no"
		s["logging"] = map[string]any{"driver": "json-file", "options": map[string]any{"max-size": "5m", "max-file": "2"}}
		so := strList(s["security_opt"])
		if !slices.Contains(so, "no-new-privileges:true") {
			so = append(so, "no-new-privileges:true")
		}
		s["security_opt"] = so
		labels := map[string]any{}
		switch v := s["labels"].(type) {
		case map[string]any:
			labels = v
		case []any:
			for _, x := range v {
				k, val, _ := strings.Cut(fmt.Sprint(x), "=")
				labels[k] = val
			}
		}
		labels[labelApp] = l.ID
		s["labels"] = labels
	}
	nets, _ := p.doc["networks"].(map[string]any)
	if nets == nil {
		nets = map[string]any{}
		p.doc["networks"] = nets
	}
	if _, ok := nets["default"]; !ok {
		nets["default"] = map[string]any{"name": projectName(l.ID) + "_default"}
	}
	for name, v := range nets {
		n, _ := v.(map[string]any)
		if n == nil {
			n = map[string]any{}
			nets[name] = n
		}
		n["driver"] = "bridge"
		opts, _ := n["driver_opts"].(map[string]any)
		if opts == nil {
			opts = map[string]any{}
		}
		opts["com.docker.network.bridge.name"] = bridgeFor(l.ID, name)
		n["driver_opts"] = opts
	}
	p.doc["name"] = projectName(l.ID)
	return json.MarshalIndent(p.doc, "", " ")
}

// bridgeFor names a project network's bridge "sn-" + 10 hex digits (Linux
// interface names have at most 15 characters).
func bridgeFor(id, network string) string {
	h := sha256.Sum256([]byte(id + "/" + network))
	return "sn-" + hex.EncodeToString(h[:5])
}

func strList(v any) []string {
	l, _ := v.([]any)
	out := make([]string, 0, len(l))
	for _, x := range l {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// ---------- running compose ----------

// composeDockerBin is the docker CLI inside the official CLI image. The
// helper runs it by absolute path, so no PATH lookup can pick another file.
const composeDockerBin = "/usr/local/bin/docker"

// composeHelperPath is the fixed PATH of the helper container.
const composeHelperPath = "/usr/local/bin:/usr/bin:/bin"

// helperReserved reports a variable name that must never reach the helper
// container, which holds the Docker socket: it could change which program
// runs, how the CLI finds the daemon, its plugins and its configuration. The
// service itself still receives the variable.
func helperReserved(key string) bool {
	k := strings.ToUpper(key)
	if k == "COMPOSE_PROFILES" { // selects profiles; cannot change what runs
		return false
	}
	for _, prefix := range []string{"LD_", "DOCKER_", "COMPOSE_", "BUILDKIT_", "BUILDX_", "XDG_"} {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}
	switch k {
	case "PATH", "HOME", "SHELL", "IFS", "ENV", "BASH_ENV", "BASHOPTS", "SHELLOPTS", "PS4", "PROMPT_COMMAND",
		"CDPATH", "GLOBIGNORE", "PWD", "OLDPWD", "USER", "LOGNAME", "TMPDIR", "TMP", "TEMP",
		"SSL_CERT_FILE", "SSL_CERT_DIR", "GODEBUG", "GOFLAGS", "GOROOT", "GOMAXPROCS", "GOMEMLIMIT", "GOGC", "GOTRACEBACK":
		return true
	}
	return false
}

// composeHelperEnv is the helper's whole environment: a fixed minimal base,
// then the service's variables (Compose interpolates them) except reserved
// names, which are returned in skipped (names only, never values).
func composeHelperEnv(env []string) (out, skipped []string) {
	out = []string{"PATH=" + composeHelperPath, "HOME=/tmp", "DOCKER_CONFIG=/tmp/.docker"}
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if helperReserved(k) {
			if !slices.Contains(skipped, k) {
				skipped = append(skipped, k)
			}
			continue
		}
		out = append(out, kv)
	}
	return out, skipped
}

// composeDir holds what the helper reads but a service must never write: the
// rendered Compose file. It is outside the service's folder, so no bind a
// Compose project may declare can reach it, and is mounted read-only.
func (a *Agent) composeDir(id string) string {
	if !idPattern.MatchString(id) {
		id = "invalid"
	}
	return filepath.Join(a.Cfg.StateDir, "compose", id)
}

// writeRendered stores the rendered file where services cannot touch it.
func (a *Agent) writeRendered(id string, out []byte) (string, error) {
	dir := a.composeDir(id)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	p := filepath.Join(dir, renderedCompose)
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, p); err != nil {
		return "", err
	}
	return p, nil
}

// composeCLI runs `docker compose -p sapp-<id> args...` in a throwaway
// container and returns its standard output; stderr lines go to logf.
func (a *Agent) composeCLI(ctx context.Context, app *App, work string, args []string, logf func(string, ...any)) (string, error) {
	name := containerName(app.ID) + "-compose"
	_ = a.Eng.Remove(ctx, name)
	cmd := append([]string{composeDockerBin, "compose", "--ansi", "never", "--project-name", projectName(app.ID)}, args...)
	env, skipped := composeHelperEnv(a.env(app, false))
	if len(skipped) > 0 {
		logf("Variables not passed to Compose itself (reserved names, the service still gets them): %s", strings.Join(skipped, ", "))
	}
	if err := os.MkdirAll(a.composeDir(app.ID), 0o750); err != nil {
		return "", err
	}
	// Not root: the agent's own user (it owns the files) plus the socket's
	// group, like the agent itself.
	spec := Spec{
		Image: a.Cfg.ComposeImage, Cmd: cmd, WorkingDir: work, User: selfUser(),
		Binds: []string{a.Cfg.Docker + ":/var/run/docker.sock", a.appDir(app.ID) + ":" + a.appDir(app.ID),
			a.composeDir(app.ID) + ":" + a.composeDir(app.ID) + ":ro"},
		Env:    env,
		Memory: int64(a.Cfg.BuildMemoryMB) << 20, NanoCPUs: int64(a.Cfg.BuildCPUs * 1e9), CPUShares: 128, Pids: 1024,
		Network: "none", Labels: map[string]string{labelApp: app.ID, "sentinela.helper": "compose"},
	}
	if g := socketGroup(a.Cfg.Docker); g != "" {
		spec.GroupAdd = []string{g}
	}
	if work == "" {
		spec.WorkingDir = a.appDir(app.ID)
	}
	if err := a.ensureImage(ctx, a.Cfg.ComposeImage, logf); err != nil {
		return "", err
	}
	if err := a.Eng.Create(ctx, name, spec); err != nil {
		return "", err
	}
	defer func() {
		c, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		_ = a.Eng.Remove(c, name)
	}()
	if err := a.Eng.Start(ctx, name); err != nil {
		return "", err
	}
	tctx, cancel := context.WithTimeout(ctx, setupTimeout)
	defer cancel()
	var out strings.Builder
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = a.Eng.Logs(tctx, name, LogOptions{Follow: true}, func(stream int, s string) {
			if stream == 1 {
				out.WriteString(s)
				out.WriteByte('\n')
			} else {
				logf("  %s", s)
			}
		})
	}()
	code, err := a.Eng.Wait(tctx, name)
	if tctx.Err() != nil {
		return "", fmt.Errorf("docker compose passou de %s e foi interrompido", setupTimeout)
	}
	<-done
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("docker compose %s exited with code %d", args[0], code)
	}
	return out.String(), nil
}

// composeFile finds the project's Compose file in work.
func composeFile(work, chosen string) (string, error) {
	if chosen != "" {
		if _, err := os.Stat(filepath.Join(work, filepath.FromSlash(chosen))); err != nil {
			return "", fmt.Errorf("file %q does not exist", chosen)
		}
		return chosen, nil
	}
	for _, n := range composeNames {
		if _, err := os.Stat(filepath.Join(work, n)); err == nil {
			return n, nil
		}
	}
	return "", errors.New("no compose.yaml / docker-compose.yml in the folder")
}

// renderedCompose is the rewritten file the agent runs, kept in composeDir.
const renderedCompose = "rendered.json"

// Environment files have already been imported into the encrypted manager.
// Prepare a release-only input so Compose doesn't try to reopen deleted
// files. Manager values are resolved by the helper's environment at runtime.
func (a *Agent) composeInput(app *App, work, file string) (string, error) {
	root, err := os.OpenRoot(work)
	if err != nil {
		return "", err
	}
	defer root.Close()
	b, err := readProjectText(root, file)
	if err != nil {
		return "", err
	}
	var doc map[string]any
	if yaml.Unmarshal(b, &doc) != nil {
		return "", errors.New("invalid Compose file")
	}
	services, _ := doc["services"].(map[string]any)
	a.mu.Lock()
	vault := a.vaults[app.ID]
	a.mu.Unlock()
	for _, raw := range services {
		s, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		refs, ok := s["env_file"]
		if !ok {
			continue
		}
		list, ok := refs.([]any)
		if !ok {
			list = []any{refs}
		}
		kept := []any{}
		for _, ref := range list {
			name, ok := ref.(string)
			if !ok {
				if m, ok := ref.(map[string]any); ok {
					name, _ = m["path"].(string)
				}
			}
			rel := path.Join(app.Dir, path.Dir(file), strings.ReplaceAll(name, "\\", "/"))
			if name == "" || !slices.Contains(vault.Files, rel) {
				kept = append(kept, ref)
			}
		}
		if len(kept) == 0 {
			delete(s, "env_file")
		} else {
			s["env_file"] = kept
		}
	}
	out, err := yaml.Marshal(doc)
	if err != nil {
		return "", err
	}
	name := path.Join(path.Dir(file), ".sentinela-input.yaml")
	if err := root.WriteFile(name, out, 0o600); err != nil {
		return "", err
	}
	return name, nil
}

// The rendered file contains variable references, never manager values.
// Escape literal dollars before replacing known values to avoid an extra
// interpolation of a command's shell variables when Compose reads it again.
func protectComposeValues(v any, vars []EnvVar) any {
	slices.SortFunc(vars, func(x, y EnvVar) int {
		if len(x.Value) != len(y.Value) {
			return len(y.Value) - len(x.Value)
		}
		return strings.Compare(x.Key, y.Key)
	})
	var pairs []string
	seen := map[string]bool{}
	for _, e := range vars {
		if e.Value != "" && !seen[e.Value] {
			seen[e.Value] = true
			pairs = append(pairs, strings.ReplaceAll(e.Value, "$", "$$"), "${"+e.Key+"}")
		}
	}
	replacer := strings.NewReplacer(pairs...)
	var walk func(any) any
	walk = func(v any) any {
		switch value := v.(type) {
		case string:
			return replacer.Replace(strings.ReplaceAll(value, "$", "$$"))
		case map[string]any:
			for key, child := range value {
				value[key] = walk(child)
			}
			return value
		case []any:
			for i, child := range value {
				value[i] = walk(child)
			}
			return value
		default:
			return v
		}
	}
	return walk(v)
}

// composeUp normalizes, checks and rewrites the project in work, assigns
// its ports, then brings it up. It returns the services and ports.
func (a *Agent) composeUp(ctx context.Context, app *App, work string, build bool, logf func(string, ...any)) ([]string, []Port, error) {
	services, ports, _, err := a.composeRun(ctx, app, work, build, logf)
	return services, ports, err
}

// composeRun is composeUp that also returns the services Compose built from
// source in this run (their build steps run outside the sn-* firewall).
func (a *Agent) composeRun(ctx context.Context, app *App, work string, build bool, logf func(string, ...any)) ([]string, []Port, []string, error) {
	file, err := composeFile(work, app.ComposeFile)
	if err != nil {
		return nil, nil, nil, err
	}
	logf("Lendo %s", file)
	input, err := a.composeInput(app, work, file)
	if err != nil {
		return nil, nil, nil, err
	}
	defer os.Remove(filepath.Join(work, input))
	raw, err := a.composeCLI(ctx, app, work, []string{"-f", input, "config", "--format", "json"}, logf)
	if err != nil {
		return nil, nil, nil, err
	}
	plan, err := planCompose([]byte(raw), []string{a.appDir(app.ID)})
	if err != nil {
		return nil, nil, nil, err
	}
	if err := checkHostPaths(a.appDir(app.ID), plan.hostPaths); err != nil {
		return nil, nil, nil, err
	}
	// Missing bind folders become real folders owned by the agent now, so
	// the daemon never creates or resolves one a service could swap for a link.
	if err := prepareBindDirs(a.appDir(app.ID), plan.bindDirs); err != nil {
		return nil, nil, nil, err
	}
	a.mu.Lock()
	vars := slices.Clone(a.vaults[app.ID].Vars)
	a.mu.Unlock()
	for _, raw := range plan.doc["services"].(map[string]any) {
		s := raw.(map[string]any)
		env, _ := s["environment"].(map[string]any)
		if env == nil {
			env = map[string]any{}
		}
		for _, e := range vars {
			env[e.Key] = e.Value
		}
		s["environment"] = env
	}
	protectComposeValues(plan.doc, vars)
	want := App{ID: app.ID}
	for _, r := range plan.ports {
		want.Ports = append(want.Ports, r.port)
	}
	a.mu.Lock()
	err = a.assignPorts(&want, app.Ports)
	a.mu.Unlock()
	if err != nil {
		return nil, nil, nil, err
	}
	ip, err := a.bindIP(app.Exposure)
	if err != nil && len(want.Ports) > 0 {
		return nil, nil, nil, err
	}
	out, err := plan.apply(want.Ports, composeLimits{ID: app.ID, MemoryMB: app.MemoryMB, CPUs: app.CPUs, BindIP: ip})
	if err != nil {
		return nil, nil, nil, err
	}
	// The rendered file lives outside every folder a service can bind and is
	// mounted read-only into the helper.
	rendered, err := a.writeRendered(app.ID, out)
	if err != nil {
		return nil, nil, nil, err
	}
	args := []string{"-f", rendered, "--project-directory", work, "up", "--detach", "--remove-orphans"}
	var built []string
	if build {
		args = append(args, "--build")
		built = plan.builds
		if len(built) > 0 {
			logf("Warning: the build steps of %s run on Docker's own network, outside the sn-* egress firewall (ADR-0021)", strings.Join(built, ", "))
		}
	} else {
		args = append(args, "--no-build")
	}
	logf("Starting %d service(s): %s", len(plan.services), strings.Join(plan.services, ", "))
	// The folders were checked a moment ago and may have changed since: look
	// again just before the helper runs `up`, refusing any link met now.
	if err := checkHostPaths(a.appDir(app.ID), plan.hostPaths); err != nil {
		return nil, nil, nil, err
	}
	if _, err := a.composeCLI(ctx, app, work, args, logf); err != nil {
		return nil, nil, nil, err
	}
	return plan.services, want.Ports, built, nil
}

// projectContainers lists the containers of a compose service.
func (a *Agent) projectContainers(ctx context.Context, id string) ([]Summary, error) {
	return a.Eng.List(ctx, "com.docker.compose.project="+projectName(id))
}

// composeCheck waits a moment and reports the first crashed container.
func (a *Agent) composeCheck(ctx context.Context, app *App, logf func(string, ...any)) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(a.settle()):
	}
	cs, err := a.projectContainers(ctx, app.ID)
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.rt(app.ID).members = cs
	a.mu.Unlock()
	var running []string
	for _, c := range cs {
		if c.Running {
			running = append(running, c.Name)
		}
	}
	a.sample(ctx, app.ID, running)
	if len(cs) == 0 {
		return errors.New("no container of the project is running")
	}
	for _, c := range cs {
		if c.Running {
			continue
		}
		st, err := a.Eng.Inspect(ctx, c.Name)
		if err != nil || st == nil || st.ExitCode == 0 && !st.OOMKilled {
			continue // finished jobs (migrations…) are fine
		}
		a.mu.Lock()
		a.observeExitLocked(app.ID, c.Name, st)
		a.mu.Unlock()
		_ = a.Eng.Logs(ctx, c.Name, LogOptions{Tail: 30}, func(_ int, s string) { logf("  %s | %s", c.Service, s) })
		if st.OOMKilled {
			return fmt.Errorf("%s went over the memory limit (%d MB)", c.Service, app.MemoryMB)
		}
		return fmt.Errorf("%s: %s", c.Service, exitReason(st))
	}
	return nil
}
