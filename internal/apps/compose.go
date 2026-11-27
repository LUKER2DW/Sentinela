package apps

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
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
// official CLI image, with the socket, the agent's compose folder and the
// service's release and data folders (read-only) mounted at their host
// paths; Sentinela's own image carries no Docker CLI.
//
// The check is an allow-list: every attribute of the normalized file must be
// one this file knows to stay inside the container (or one the agent
// rewrites), with constrained values. Anything else is refused, so an
// attribute added to a later Compose release cannot reach the host unseen.

// projectName is the Compose project of a service.
func projectName(id string) string { return "sapp-" + id }

// defaultCaps are Docker's default capabilities: cap_add may only re-add
// one of these (after a cap_drop), never grant more.
var defaultCaps = []string{"AUDIT_WRITE", "CHOWN", "DAC_OVERRIDE", "FOWNER", "FSETID", "KILL", "MKNOD",
	"NET_BIND_SERVICE", "NET_RAW", "SETFCAP", "SETGID", "SETPCAP", "SETUID", "SYS_CHROOT"}

// composeRules say what planCompose accepts for one service.
type composeRules struct {
	// ID is the service. Its Compose project is projectName(ID), and its
	// networks and volumes may only carry names starting with that and "_".
	ID string
	// Release is the release folder being run. Build contexts and env files
	// come from it; binds and secret files from it or from Data. The rest
	// of the service's folder (the agent's own files, other releases, the
	// protected project) is never bindable.
	Release string
	// Data is the service's persistent folder.
	Data string
	// Running are the host paths that containers still running from the
	// previous `up` bind; the nesting rule covers them too.
	Running []hostBind
}

// hostBind is a host path the daemon mounts into a container.
type hostBind struct {
	Path string `json:"path"`
	RW   bool   `json:"rw"`
}

// composePlan is a normalized Compose project being rewritten.
type composePlan struct {
	doc      map[string]any
	services []string
	ports    []portRef
	// hostPaths are the bind and secret/config sources, all inside an
	// allowed folder; checkHostPaths refuses symlinks among them.
	hostPaths []string
	// binds are the same sources with their access, for the nesting rule
	// and for the next run (saved as the running binds).
	binds []hostBind
	// bindDirs are the bind sources the Docker daemon would create when
	// missing; the agent creates them first, as real folders it owns.
	bindDirs []string
	// builds are the services Compose builds from source (`build:`).
	builds []string
	// objectVars are the variables top-level secrets and configs read at `up`
	// (`environment: NAME`), and objects the entries that read them; apply
	// points them at agent-named variables (composeObjectVar).
	objectVars []string
	objects    []objectRef
	// images maps each service that runs an image a build produces to that
	// image's agent name (builtImages). It is computed from the file as
	// checked, before vault values in it become variable references.
	images map[string]string
}

// objectRef is a top-level secret or config that reads a variable at `up`.
type objectRef struct {
	entry map[string]any
	key   string
}

// portRef is one published port entry and the platform port it maps to.
type portRef struct {
	entry map[string]any
	port  Port
}

// planCheck collects the problems of one normalized file.
type planCheck struct {
	r    composeRules
	p    *composePlan
	errs []error
}

func (c *planCheck) bad(where, format string, args ...any) {
	c.errs = append(c.errs, fmt.Errorf("%s: "+format, append([]any{where}, args...)...))
}

// The typed readers below return an attribute's value and refuse any other
// type: a value the check does not understand must not pass as "unset".

// str reads a text attribute (nil = "").
func (c *planCheck) str(where, key string, v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	}
	c.bad(where, "%s must be text", key)
	return ""
}

// flag reads a yes/no attribute (nil = false).
func (c *planCheck) flag(where, key string, v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	}
	c.bad(where, "%s must be true or false", key)
	return false
}

// num reads a number (nil = 0).
func (c *planCheck) num(where, key string, v any) float64 {
	switch x := v.(type) {
	case nil:
		return 0
	case float64:
		return x
	}
	c.bad(where, "%s must be a number", key)
	return 0
}

// list reads a list (nil = none).
func (c *planCheck) list(where, key string, v any) []any {
	switch x := v.(type) {
	case nil:
		return nil
	case []any:
		return x
	}
	c.bad(where, "%s must be a list", key)
	return nil
}

// obj reads a mapping (nil = none).
func (c *planCheck) obj(where, key string, v any) map[string]any {
	switch x := v.(type) {
	case nil:
		return nil
	case map[string]any:
		return x
	}
	c.bad(where, "%s must be a mapping", key)
	return nil
}

// strs reads a list of text values.
func (c *planCheck) strs(where, key string, v any) []string {
	var out []string
	for _, e := range c.list(where, key, v) {
		s, ok := e.(string)
		if !ok {
			c.bad(where, "%s must be a list of text values", key)
			return nil
		}
		out = append(out, s)
	}
	return out
}

// extension reports a Compose extension key ("x-..."). Extensions are
// accepted and dropped from the rendered file: Compose itself reads some of
// them (a secret's `x-#value` becomes its content), so none reaches `up`.
func extension(k string) bool { return strings.HasPrefix(k, "x-") }

// composeFree are service attributes that stay inside the container, or
// that apply replaces with the agent's own value (limits, restart policy,
// logging, profiles, container_name): accepted as they are.
var composeFree = setOf("attach", "command", "configs", "cap_drop", "container_name", "cpu_count", "cpu_percent",
	"cpu_period", "cpu_quota", "cpu_shares", "cpus", "cpuset", "depends_on", "develop", "dns", "dns_opt", "dns_search",
	"domainname", "entrypoint", "environment", "expose", "extra_hosts", "group_add", "healthcheck", "hostname", "image",
	"init", "links", "log_driver", "log_opt", "logging", "mem_limit", "mem_reservation", "mem_swappiness", "memswap_limit", "name",
	"oom_score_adj", "pids_limit", "platform", "profiles", "pull_policy", "read_only", "restart", "secrets", "shm_size",
	"stdin_open", "stop_grace_period", "stop_signal", "tmpfs", "tty", "user", "working_dir")

// composeUlimits are the resource limits a project may set. Not rtprio,
// rttime or nice: they let an unprivileged process take real-time or higher
// scheduling priority, which the CPU quota does not bound.
var composeUlimits = setOf("as", "core", "cpu", "data", "fsize", "locks", "memlock", "msgqueue", "nofile", "nproc",
	"sigpending", "stack")

func setOf(keys ...string) map[string]bool {
	m := make(map[string]bool, len(keys))
	for _, k := range keys {
		m[k] = true
	}
	return m
}

// planCompose parses normalized Compose JSON and refuses anything that
// would reach past the service's containers: privileged mode, host
// namespaces, devices, extra capabilities, the Docker socket, binds outside
// the release and data folders or nested under a writable bind, networks
// and volumes of other projects, IPv6 or chosen subnets, replicas, build
// outputs outside the service's image namespace, and every attribute it
// does not know.
func planCompose(raw []byte, r composeRules) (*composePlan, error) {
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("compose: %w", err)
	}
	services, _ := doc["services"].(map[string]any)
	if len(services) == 0 {
		return nil, errors.New("compose: no service in the file")
	}
	p := &composePlan{doc: doc}
	c := &planCheck{r: r, p: p}
	for name := range services {
		p.services = append(p.services, name)
	}
	sort.Strings(p.services)
	for _, name := range p.services {
		s, ok := services[name].(map[string]any)
		if !ok {
			c.bad(name, "invalid format")
			continue
		}
		c.service(name, s, services)
	}
	for _, k := range sortedKeys(doc) {
		v := doc[k]
		switch {
		case k == "services":
		case extension(k):
			delete(doc, k)
		case k == "name" || k == "version":
			c.str("compose", k, v)
		case k == "networks":
			c.networks(v)
		case k == "volumes":
			c.topVolumes(v)
		case k == "secrets" || k == "configs":
			c.files(k, v)
		case empty(v):
			delete(doc, k)
		default:
			c.bad("compose", "%s is not allowed", k)
		}
	}
	if outer, inner, ok := nestedBind(append(slices.Clone(r.Running), p.binds...)); ok {
		if slices.Contains(r.Running, outer) {
			c.bad("volumes", "a running container mounts %q writable, which contains %q: stop the service, then deploy", outer.Path, inner)
		} else {
			c.bad("volumes", "folder %q is mounted writable and contains %q, mounted too: a container could replace it with a link; mount them side by side, or the outer one read-only", outer.Path, inner)
		}
	}
	if len(c.errs) > 0 {
		return nil, fmt.Errorf("the Compose file uses disallowed resources:\n%w", errors.Join(c.errs...))
	}
	p.images = p.builtImages(r.ID)
	return p, nil
}

// service checks one service's attributes.
func (c *planCheck) service(name string, s, all map[string]any) {
	for _, k := range sortedKeys(s) {
		v := s[k]
		switch {
		case extension(k):
			delete(s, k)
		case k == "image" || k == "container_name" || k == "pull_policy":
			c.str(name, k, v)
		case composeFree[k]:
		case k == "build":
			if empty(v) {
				delete(s, k)
			} else {
				c.build(name, v)
			}
		case k == "cap_add":
			for _, cp := range c.strs(name, k, v) {
				if !slices.Contains(defaultCaps, strings.TrimPrefix(strings.ToUpper(cp), "CAP_")) {
					c.bad(name, "cap_add %s is not allowed", cp)
				}
			}
		case k == "cgroup":
			if x := c.str(name, k, v); x != "" && x != "private" {
				c.bad(name, "cgroup: %q is not allowed", x)
			}
		case k == "deploy":
			c.deploy(name, v)
		case k == "env_file":
			c.envFiles(name, v)
		case k == "ipc":
			if x := c.str(name, k, v); x != "" && x != "private" && x != "shareable" {
				c.bad(name, "ipc: %q is not allowed", x)
			}
		case k == "labels":
			c.labels(name, v)
		case k == "network_mode":
			x := c.str(name, k, v)
			other, isService := strings.CutPrefix(x, "service:")
			if _, known := all[other]; x != "" && x != "none" && !(isService && known) {
				c.bad(name, "network_mode: %q is not allowed", x)
			}
		case k == "networks":
			c.serviceNetworks(name, v)
		case k == "ports":
			c.ports(name, s, v)
		case k == "post_start" || k == "pre_stop":
			c.hooks(name, k, v)
		case k == "scale":
			if n := c.num(name, k, v); n > 1 {
				c.bad(name, "scale %v is not allowed: each service runs one container (the memory budget counts one)", n)
			}
		case k == "security_opt":
			for _, o := range c.strs(name, k, v) {
				if o != "no-new-privileges" && o != "no-new-privileges:true" && o != "no-new-privileges=true" {
					c.bad(name, "security_opt %q is not allowed", o)
				}
			}
		case k == "sysctls":
			c.sysctls(name, v)
		case k == "ulimits":
			c.ulimits(name, v)
		case k == "volumes":
			c.volumes(name, v)
		case k == "volumes_from":
			for _, x := range c.strs(name, k, v) {
				if strings.HasPrefix(x, "container:") {
					c.bad(name, "volumes_from %q is not allowed", x)
				}
			}
		case empty(v):
			delete(s, k) // printed by `config` for an unset attribute
		default:
			c.bad(name, "%s is not allowed", k)
		}
	}
}

// build checks a `build:` section. The context and Dockerfile come from the
// release; the output name is chosen by apply (never `tags`).
func (c *planCheck) build(name string, v any) {
	b := c.obj(name, "build", v)
	if b == nil {
		return
	}
	c.p.builds = append(c.p.builds, name)
	ctxDir := c.str(name, "build context", b["context"])
	if !within(ctxDir, c.r.Release) {
		c.bad(name, "build context %q is not a folder of the project", ctxDir)
	}
	for _, k := range sortedKeys(b) {
		x := b[k]
		switch {
		case extension(k):
			delete(b, k)
		case k == "context":
		case k == "dockerfile_inline" || k == "target":
			c.str(name, "build "+k, x)
		case k == "args" || k == "no_cache" || k == "pull" || k == "shm_size" || k == "extra_hosts" || k == "platforms" || k == "secrets":
		case k == "dockerfile":
			f := c.str(name, "build dockerfile", x)
			if f != "" && !path.IsAbs(filepath.ToSlash(f)) && !filepath.IsAbs(f) {
				f = path.Join(filepath.ToSlash(ctxDir), filepath.ToSlash(f))
			}
			if f != "" && !within(f, c.r.Release) {
				c.bad(name, "Dockerfile %q is not a file of the project", x)
			}
		case k == "labels":
			c.labels(name, x)
		case k == "network":
			if n := c.str(name, "build network", x); n != "" && n != "default" && n != "none" {
				c.bad(name, "build network %q is not allowed", n)
			}
		case k == "tags":
			if !empty(x) {
				c.bad(name, "build tags are not allowed: Sentinela names the image it builds")
			}
		case k == "ulimits":
			c.ulimits(name, x)
		case empty(x):
			delete(b, k)
		default:
			c.bad(name, "build %s is not allowed", k)
		}
	}
}

func (c *planCheck) deploy(name string, v any) {
	d := c.obj(name, "deploy", v)
	for _, k := range sortedKeys(d) {
		x := d[k]
		switch {
		case extension(k):
			delete(d, k)
		case k == "resources" || k == "restart_policy": // replaced by the agent's limits
		case k == "labels" || k == "update_config" || k == "rollback_config" || k == "placement" || k == "endpoint_mode": // Swarm only, ignored
		case k == "replicas":
			if n := c.num(name, "deploy.replicas", x); n > 1 {
				c.bad(name, "deploy.replicas %v is not allowed: each service runs one container (the memory budget counts one)", n)
			}
		case k == "mode":
			if m := c.str(name, "deploy.mode", x); m != "" && m != "replicated" && m != "global" {
				c.bad(name, "deploy.mode %q is not allowed", m)
			}
		case empty(x):
			delete(d, k)
		default:
			c.bad(name, "deploy.%s is not allowed", k)
		}
	}
}

// envFiles: env files are read by the helper, from the release only.
func (c *planCheck) envFiles(name string, v any) {
	list, ok := v.([]any)
	if !ok {
		list = []any{v}
	}
	for _, e := range list {
		var f string
		switch x := e.(type) {
		case string:
			f = x
		case map[string]any:
			f = c.str(name, "env_file path", x["path"])
			for _, k := range sortedKeys(x) {
				switch {
				case extension(k):
					delete(x, k)
				case k == "path" || k == "required" || k == "format":
				case empty(x[k]):
					delete(x, k)
				default:
					c.bad(name, "env_file %s is not allowed", k)
				}
			}
		default:
			c.bad(name, "invalid env_file %v", e)
			continue
		}
		if f != "" && !within(f, c.r.Release) {
			c.bad(name, "env file %q is not a file of the project", f)
		}
	}
}

// labels refuses the label names Compose and the agent identify projects
// and services by.
func (c *planCheck) labels(where string, v any) {
	var keys []string
	switch l := v.(type) {
	case nil:
	case map[string]any:
		keys = sortedKeys(l)
	case []any:
		for _, x := range l {
			s, ok := x.(string)
			if !ok {
				c.bad(where, "labels must be text")
				return
			}
			k, _, _ := strings.Cut(s, "=")
			keys = append(keys, k)
		}
	default:
		c.bad(where, "labels must be a mapping or a list")
	}
	for _, k := range keys {
		if strings.HasPrefix(k, "com.docker.compose.") || strings.HasPrefix(k, "sentinela.") {
			c.bad(where, "label %q is reserved", k)
		}
	}
}

// serviceNetworks: a service joins its project's networks by name, with
// aliases; addresses, MAC and driver options are the engine's to choose.
func (c *planCheck) serviceNetworks(name string, v any) {
	if empty(v) {
		return
	}
	nets := c.obj(name, "networks", v)
	for _, net := range sortedKeys(nets) {
		if empty(nets[net]) {
			continue
		}
		m := c.obj(name, "network "+net, nets[net])
		for _, k := range sortedKeys(m) {
			switch {
			case extension(k):
				delete(m, k)
			case k == "aliases":
				c.strs(name, "network "+net+" aliases", m[k])
			case k == "priority" || k == "gw_priority":
				c.num(name, "network "+net+" "+k, m[k])
			case empty(m[k]):
				delete(m, k)
			default:
				c.bad(name, "network %s: %s is not allowed", net, k)
			}
		}
	}
}

// composePortKeys are the attributes of a published port; apply sets the
// host address and port.
var composePortKeys = setOf("target", "published", "host_ip", "protocol", "mode", "name", "app_protocol")

// ports keeps the entries published on a fixed port, for apply to move to
// the services range; unpublished ones stay internal.
func (c *planCheck) ports(name string, s map[string]any, v any) {
	ports := c.list(name, "ports", v)
	kept := []any{}
	for _, e := range ports {
		m, ok := e.(map[string]any)
		if !ok {
			c.bad(name, "invalid port %v", e)
			continue
		}
		for _, k := range sortedKeys(m) {
			switch {
			case extension(k):
				delete(m, k)
			case composePortKeys[k]:
			case empty(m[k]):
				delete(m, k)
			default:
				c.bad(name, "port option %s is not allowed", k)
			}
		}
		pub := fmt.Sprint(m["published"])
		if m["published"] == nil || pub == "" || pub == "0" {
			continue // not published on a fixed port: stays internal
		}
		if strings.Contains(pub, "-") {
			c.bad(name, "port ranges (%s) are not supported", pub)
			continue
		}
		target, err := strconv.Atoi(fmt.Sprint(m["target"]))
		if err != nil || target < 1 || target > 65535 {
			c.bad(name, "invalid port %v", m["target"])
			continue
		}
		proto := c.str(name, "port protocol", m["protocol"])
		if proto == "" {
			proto = "tcp"
		}
		if proto != "tcp" && proto != "udp" {
			c.bad(name, "protocol %q is not supported", proto)
			continue
		}
		kept = append(kept, m)
		c.p.ports = append(c.p.ports, portRef{entry: m, port: Port{Service: name, Container: target, Proto: proto}})
	}
	if v != nil {
		s["ports"] = kept
	}
}

// hooks: post_start / pre_stop commands run in the container as it is, never
// privileged.
func (c *planCheck) hooks(name, key string, v any) {
	for _, e := range c.list(name, key, v) {
		h, ok := e.(map[string]any)
		if !ok {
			c.bad(name, "invalid %s", key)
			continue
		}
		for _, k := range sortedKeys(h) {
			switch {
			case extension(k):
				delete(h, k)
			case k == "command" || k == "user" || k == "working_dir" || k == "environment":
			case empty(h[k]):
				delete(h, k)
			default:
				c.bad(name, "%s %s is not allowed", key, k)
			}
		}
	}
}

func (c *planCheck) sysctls(name string, v any) {
	var keys []string
	switch l := v.(type) {
	case nil:
	case map[string]any:
		keys = sortedKeys(l)
	case []any:
		for _, x := range l {
			s, ok := x.(string)
			if !ok {
				c.bad(name, "sysctls must be text")
				return
			}
			k, _, _ := strings.Cut(s, "=")
			keys = append(keys, k)
		}
	default:
		c.bad(name, "sysctls must be a mapping or a list")
	}
	for _, k := range keys {
		if !strings.HasPrefix(k, "net.") {
			c.bad(name, "sysctl %s is not allowed", k)
		}
	}
}

func (c *planCheck) ulimits(name string, v any) {
	m := c.obj(name, "ulimits", v)
	for _, k := range sortedKeys(m) {
		if !composeUlimits[k] {
			c.bad(name, "ulimit %s is not allowed", k)
		}
	}
}

// volumes: named volumes and tmpfs anywhere; binds only from the release
// or data folder.
func (c *planCheck) volumes(name string, v any) {
	list := c.list(name, "volumes", v)
	sub := map[string]map[string]bool{
		"bind":   setOf("propagation", "create_host_path", "selinux", "recursive"),
		"volume": setOf("nocopy", "subpath"),
		"tmpfs":  setOf("size", "mode"),
	}
	for _, e := range list {
		m, ok := e.(map[string]any)
		if !ok {
			c.bad(name, "invalid volume %v", e)
			continue
		}
		typ := c.str(name, "volume type", m["type"])
		src := c.str(name, "volume source", m["source"])
		ro := c.flag(name, "volume read_only", m["read_only"])
		for _, k := range sortedKeys(m) {
			x := m[k]
			switch {
			case extension(k):
				delete(m, k)
			case k == "type" || k == "source" || k == "read_only":
			case k == "target" || k == "consistency":
				c.str(name, "volume "+k, x)
			case sub[k] != nil:
				opts := c.obj(name, "volume "+k, x)
				for _, o := range sortedKeys(opts) {
					switch {
					case extension(o):
						delete(opts, o)
					case sub[k][o]:
					case empty(opts[o]):
						delete(opts, o)
					default:
						c.bad(name, "volume option %s.%s is not allowed", k, o)
					}
				}
				if k == "bind" {
					if p := c.str(name, "bind propagation", opts["propagation"]); p == "shared" || p == "rshared" {
						c.bad(name, "bind propagation %q is not allowed", p)
					}
				}
			case empty(x):
				delete(m, k)
			default:
				c.bad(name, "volume option %s is not allowed", k)
			}
		}
		switch typ {
		case "volume", "tmpfs":
		case "bind":
			if strings.Contains(src, "docker.sock") || !c.bindable(src) {
				c.bad(name, "folder %q is outside the project; use a named volume", src)
				continue
			}
			c.p.hostPaths = append(c.p.hostPaths, src)
			c.p.binds = append(c.p.binds, hostBind{Path: src, RW: !ro})
			if b, _ := m["bind"].(map[string]any); b == nil || b["create_host_path"] != false {
				c.p.bindDirs = append(c.p.bindDirs, src)
			}
		case "":
			c.bad(name, "volume %v has no type", m["target"])
		default:
			c.bad(name, "volume of type %q is not allowed", typ)
		}
	}
}

// bindable reports a host path the daemon may mount: the release folder,
// the data folder, or anything below them.
func (c *planCheck) bindable(src string) bool {
	return within(src, c.r.Release) || within(src, c.r.Data)
}

// networks: bridges of this project only, IPv4, with the engine's own
// subnets; apply then pins each to an "sn-*" bridge the firewall filters.
func (c *planCheck) networks(v any) {
	nets := c.obj("networks", "networks", v)
	prefix := projectName(c.r.ID) + "_"
	for _, net := range sortedKeys(nets) {
		if empty(nets[net]) {
			continue
		}
		n := c.obj("networks", "network "+net, nets[net])
		for _, k := range sortedKeys(n) {
			x := n[k]
			switch {
			case extension(k):
				delete(n, k)
			case k == "name":
				if s := c.str("networks", "network "+net+" name", x); s != "" && !strings.HasPrefix(s, prefix) {
					c.bad("networks", "network %q: name %q is not this project's; remove the name", net, s)
				}
			case k == "driver":
				if d := c.str("networks", "network "+net+" driver", x); d != "" && d != "bridge" {
					c.bad("networks", "network driver %q of network %q is not allowed", d, net)
				}
			case k == "driver_opts":
				opts := c.obj("networks", "network "+net+" driver_opts", x)
				for _, o := range sortedKeys(opts) {
					if o != "com.docker.network.driver.mtu" {
						c.bad("networks", "driver option %s of network %q is not allowed", o, net)
					}
				}
			case k == "ipam":
				if !emptyDeep(x) {
					c.bad("networks", "ipam of network %q is not allowed: the engine chooses the subnet", net)
				}
			case k == "external":
				if c.flag("networks", "network "+net+" external", x) {
					c.bad("networks", "external network %q is not allowed", net)
				}
			case k == "enable_ipv6":
				if c.flag("networks", "network "+net+" enable_ipv6", x) {
					c.bad("networks", "IPv6 on network %q is not allowed: the services firewall filters IPv4", net)
				}
			case k == "enable_ipv4":
				if x != nil && !c.flag("networks", "network "+net+" enable_ipv4", x) {
					c.bad("networks", "network %q must keep IPv4", net)
				}
			case k == "internal" || k == "attachable":
				c.flag("networks", "network "+net+" "+k, x)
			case k == "labels":
				c.labels("networks", x)
			case empty(x):
				delete(n, k)
			default:
				c.bad("networks", "%s of network %q is not allowed", k, net)
			}
		}
	}
}

// topVolumes: local volumes of this project only.
func (c *planCheck) topVolumes(v any) {
	vols := c.obj("volumes", "volumes", v)
	prefix := projectName(c.r.ID) + "_"
	for _, vol := range sortedKeys(vols) {
		if empty(vols[vol]) {
			continue
		}
		m := c.obj("volumes", "volume "+vol, vols[vol])
		for _, k := range sortedKeys(m) {
			x := m[k]
			switch {
			case extension(k):
				delete(m, k)
			case k == "name":
				if s := c.str("volumes", "volume "+vol+" name", x); s != "" && !strings.HasPrefix(s, prefix) {
					c.bad("volumes", "volume %q: name %q is not this project's; remove the name", vol, s)
				}
			case k == "driver":
				if d := c.str("volumes", "volume "+vol+" driver", x); d != "" && d != "local" {
					c.bad("volumes", "volume driver %q of volume %q is not allowed", d, vol)
				}
			case k == "driver_opts":
				if !empty(x) {
					c.bad("volumes", "driver_opts of volume %q are not allowed", vol)
				}
			case k == "external":
				if c.flag("volumes", "volume "+vol+" external", x) {
					c.bad("volumes", "external volume %q is not allowed", vol)
				}
			case k == "labels":
				c.labels("volumes", x)
			case empty(x):
				delete(m, k)
			default:
				c.bad("volumes", "%s of volume %q is not allowed", k, vol)
			}
		}
	}
}

// files checks top-level secrets and configs: a file of the project (mounted
// read-only), an environment variable or inline content.
func (c *planCheck) files(top string, v any) {
	items := c.obj(top, top, v)
	for _, item := range sortedKeys(items) {
		if empty(items[item]) {
			continue
		}
		m := c.obj(top, "entry "+item, items[item])
		for _, k := range sortedKeys(m) {
			x := m[k]
			switch {
			case extension(k):
				delete(m, k)
			case k == "file":
				f := c.str(top, item+" file", x)
				if f == "" {
					continue
				}
				if !c.bindable(f) {
					c.bad(top, "%q points outside the project", item)
					continue
				}
				c.p.hostPaths = append(c.p.hostPaths, f)
				c.p.binds = append(c.p.binds, hostBind{Path: f})
			case k == "environment":
				// `config` keeps the name and blanks the value: Compose reads the
				// variable again at `up`, under the name apply gives it.
				if e := c.str(top, item+" environment", x); e != "" {
					c.p.objects = append(c.p.objects, objectRef{entry: m, key: e})
					if !slices.Contains(c.p.objectVars, e) {
						c.p.objectVars = append(c.p.objectVars, e)
					}
				}
			case k == "content" || k == "name":
				c.str(top, item+" "+k, x)
			case k == "labels":
				c.labels(top, x)
			case k == "external":
				if c.flag(top, item+" external", x) {
					c.bad(top, "external %q is not allowed", item)
				}
			case empty(x):
				delete(m, k)
			default:
				c.bad(top, "%s of %q is not allowed", k, item)
			}
		}
	}
}

// nestedBind finds a host path mounted writable that contains another one.
// A container could replace the inner folder or file with a link before the
// daemon mounts it, and the daemon follows links on the host.
func nestedBind(binds []hostBind) (outer hostBind, inner string, found bool) {
	for _, o := range binds {
		if !o.RW {
			continue
		}
		op := path.Clean(filepath.ToSlash(o.Path))
		for _, i := range binds {
			if ip := path.Clean(filepath.ToSlash(i.Path)); strings.HasPrefix(ip, op+"/") {
				return o, i.Path, true
			}
		}
	}
	return hostBind{}, "", false
}

// within reports p is root or below it (lexically: links are checked by
// checkHostPaths, and the helper only sees the folders it is given).
func within(p, root string) bool {
	if p == "" || root == "" {
		return false
	}
	p, root = path.Clean(filepath.ToSlash(p)), path.Clean(filepath.ToSlash(root))
	return p == root || strings.HasPrefix(p, root+"/")
}

// empty reports a value that sets nothing (null, false, 0, "", [] or {}),
// which `docker compose config` may print for an attribute left unset.
func empty(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case bool:
		return !x
	case string:
		return x == ""
	case float64:
		return x == 0
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	}
	return false
}

// emptyDeep is empty, also for a map or list of empty values.
func emptyDeep(v any) bool {
	switch x := v.(type) {
	case map[string]any:
		for _, e := range x {
			if !emptyDeep(e) {
				return false
			}
		}
		return true
	case []any:
		for _, e := range x {
			if !emptyDeep(e) {
				return false
			}
		}
		return true
	}
	return empty(v)
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
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

// composeDropped are accepted attributes that apply removes: Compose watch
// settings, profiles (the normalized file holds only the active services),
// container_name (a host-wide name; kept as a network alias) and limits the
// agent sets itself.
var composeDropped = []string{"develop", "profiles", "container_name", "cpu_count", "cpu_percent", "cpu_period",
	"cpu_quota", "log_driver", "log_opt"}

// apply rewrites the plan: assigned host ports and bind address, the
// platform's limits and log caps, no restart policy (the agent
// supervises), build outputs in the service's image namespace, and IPv4
// bridges with names the host firewall recognizes ("sn-*").
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
	// A build may only produce sentinela-app/<id>:compose-<service>, never a
	// name another service, the helper or the NVR runs. A service that runs
	// the image another one builds follows the rename. These names are never
	// pulled: an image missing at a later `up --no-build` fails instead of
	// being fetched from a registry under the same name.
	for name, img := range p.images {
		s := services[name].(map[string]any)
		s["image"] = img
		s["pull_policy"] = "never"
	}
	// Top-level secrets and configs read their variable again at `up`, whose
	// environment has only agent-named variables (composeObjectVar).
	for _, o := range p.objects {
		o.entry["environment"] = composeObjectVar(o.key)
	}
	mem := strconv.Itoa(l.MemoryMB) + "m"
	for _, name := range p.services {
		s := services[name].(map[string]any)
		if alias, _ := s["container_name"].(string); alias != "" {
			if nets, ok := s["networks"].(map[string]any); ok {
				for net, v := range nets {
					m, _ := v.(map[string]any)
					if m == nil {
						m = map[string]any{}
					}
					aliases := strList(m["aliases"])
					if !slices.Contains(aliases, alias) {
						aliases = append(aliases, alias)
					}
					m["aliases"] = aliases
					nets[net] = m
				}
			}
		}
		for _, k := range composeDropped {
			delete(s, k)
		}
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
		// Explicitly IPv4 only, whatever the daemon's default network options:
		// the services firewall has no IPv6 rules.
		n["enable_ipv6"] = false
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

// imageTag is a valid Docker image tag.
var imageTag = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)

// composeImage is the only name a Compose service's build may produce: the
// agent's image namespace, the service id and the Compose service, so no
// build can retag an image that something else runs by name (the Compose
// helper, the NVR's own images, another service's).
func composeImage(id, service string) string {
	tag := "compose-" + service
	if !imageTag.MatchString(tag) {
		h := sha256.Sum256([]byte(service))
		tag = "compose-" + hex.EncodeToString(h[:8])
	}
	return imagePrefix + id + ":" + tag
}

// composeImagePrefix starts the name of every image a Compose build of the
// service produces (composeImage).
func composeImagePrefix(id string) string { return imagePrefix + id + ":compose-" }

// builtImages maps each service that runs an image a build of the plan
// produces to that image's agent name (composeImage of the building
// service): every service with `build:`, and every service whose `image:`
// names the same image as a build's `image:`, whatever the spelling
// ("myapp", "myapp:latest", "docker.io/library/myapp:latest").
func (p *composePlan) builtImages(id string) map[string]string {
	services := p.doc["services"].(map[string]any)
	out := map[string]string{}
	byRef := map[string]string{}
	for _, name := range p.builds {
		img := composeImage(id, name)
		out[name] = img
		if orig, _ := services[name].(map[string]any)["image"].(string); orig != "" {
			if ref := normalizeRef(orig); byRef[ref] == "" {
				byRef[ref] = img
			}
		}
	}
	for _, name := range p.services {
		if _, ok := out[name]; ok {
			continue
		}
		if orig, _ := services[name].(map[string]any)["image"].(string); orig != "" {
			if img := byRef[normalizeRef(orig)]; img != "" {
				out[name] = img
			}
		}
	}
	return out
}

// foreignBuilds refuses a service that runs, by name, an image the agent
// built for a service (sentinela-app/...) other than this project's own
// builds: it would run that service's private files and build arguments.
func (p *composePlan) foreignBuilds(id string) error {
	services := p.doc["services"].(map[string]any)
	for _, name := range p.services {
		if _, own := p.images[name]; own {
			continue
		}
		orig, _ := services[name].(map[string]any)["image"].(string)
		if orig != "" && strings.HasPrefix(normalizeRef(orig), "docker.io/"+imagePrefix) {
			return fmt.Errorf("service %q runs %q, a name Sentinela keeps for the images it builds; use the project's own build instead", name, orig)
		}
	}
	return nil
}

// normalizeRef spells an image reference the way Docker resolves it: the
// registry (docker.io by default, with "library/" for official images) and
// the tag ("latest" unless a tag or digest is given).
func normalizeRef(ref string) string {
	name, digest, hasDigest := strings.Cut(strings.TrimSpace(ref), "@")
	tag := ""
	if i := strings.LastIndex(name, ":"); i > strings.LastIndex(name, "/") {
		name, tag = name[:i], name[i+1:]
	}
	domain, rest, found := strings.Cut(name, "/")
	if !found || !strings.ContainsAny(domain, ".:") && domain != "localhost" && strings.ToLower(domain) == domain {
		domain, rest = "docker.io", name
	}
	if domain == "index.docker.io" {
		domain = "docker.io"
	}
	if domain == "docker.io" && !strings.Contains(rest, "/") {
		rest = "library/" + rest
	}
	if tag == "" && !hasDigest {
		tag = "latest"
	}
	out := domain + "/" + rest
	if tag != "" {
		out += ":" + tag
	}
	if hasDigest {
		out += "@" + digest
	}
	return out
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

// composeBaseEnv is the fixed part of the helper's environment.
func composeBaseEnv() []string {
	return []string{"PATH=" + composeHelperPath, "HOME=/tmp", "DOCKER_CONFIG=/tmp/.docker"}
}

// composeHelperEnv is the environment of `docker compose config`: the fixed
// base, then the service's variables (Compose interpolates them) except
// reserved names, which are returned in skipped (names only, never values).
func composeHelperEnv(env []string) (out, skipped []string) {
	out = composeBaseEnv()
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
// rendered Compose file, the empty env file and the record of the running
// binds. It is outside the service's folder, so no bind a Compose project
// may declare can reach it, and is mounted read-only. It is also the
// project directory of every Compose action except `config`.
func (a *Agent) composeDir(id string) string {
	if !idPattern.MatchString(id) {
		id = "invalid"
	}
	return filepath.Join(a.Cfg.StateDir, "compose", id)
}

// writeComposeFile stores one of the agent's files in composeDir.
func (a *Agent) writeComposeFile(id, name string, out []byte) (string, error) {
	dir := a.composeDir(id)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	p := filepath.Join(dir, name)
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, p); err != nil {
		return "", err
	}
	return p, nil
}

// writeRendered stores the rendered file where services cannot touch it.
func (a *Agent) writeRendered(id string, out []byte) (string, error) {
	return a.writeComposeFile(id, renderedCompose, out)
}

// composeEnvFile is an empty env file the agent owns, given to every
// Compose run as --env-file: Compose then never loads a `.env` from the
// project folder, which a service binding its release could write.
func (a *Agent) composeEnvFile(id string) (string, error) {
	return a.writeComposeFile(id, "empty.env", nil)
}

// runningBinds are the host paths the containers of the last `up` bind.
func (a *Agent) runningBinds(id string) []hostBind {
	b, err := os.ReadFile(filepath.Join(a.composeDir(id), "binds.json"))
	if err != nil {
		return nil
	}
	var out []hostBind
	_ = json.Unmarshal(b, &out)
	return out
}

// bindPaths are the host paths of binds.
func bindPaths(binds []hostBind) []string {
	out := make([]string, 0, len(binds))
	for _, b := range binds {
		out = append(out, b.Path)
	}
	return out
}

func (a *Agent) saveBinds(id string, binds []hostBind) error {
	b, err := json.Marshal(binds)
	if err == nil {
		_, err = a.writeComposeFile(id, "binds.json", b)
	}
	return err
}

// composeCall is one run of the Compose helper container.
type composeCall struct {
	// timeout bounds the helper (setupTimeout when zero).
	timeout time.Duration
	// dir is the working directory: the release for `config` (relative paths
	// of the source file), the agent-owned composeDir for everything else.
	dir string
	// mounts are folders of the service the helper reads (build contexts,
	// env and secret files), mounted read-only at their host paths. Nothing
	// else of the service's folder is visible to the helper.
	mounts []string
	// env is the helper's whole environment.
	env []string
}

// maxComposeOutput bounds what the agent keeps of the helper's standard
// output (the Compose file it reads is at most maxDetectFile).
const maxComposeOutput = 4 << 20

// composeHelper runs `docker compose -p sapp-<id> args...` in a throwaway
// container and returns its standard output; stderr lines go to logf.
func (a *Agent) composeHelper(ctx context.Context, app *App, call composeCall, args []string, logf func(string, ...any)) (string, error) {
	name := containerName(app.ID) + "-compose"
	_ = a.Eng.Remove(ctx, name)
	cmd := append([]string{composeDockerBin, "compose", "--ansi", "never", "--project-name", projectName(app.ID)}, args...)
	dir := a.composeDir(app.ID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	binds := []string{a.Cfg.Docker + ":/var/run/docker.sock", dir + ":" + dir + ":ro"}
	for _, m := range call.mounts {
		binds = append(binds, m+":"+m+":ro")
	}
	// Not root: the agent's own user (it owns the files) plus the socket's
	// group, like the agent itself.
	spec := Spec{
		Image: a.Cfg.ComposeImage, Cmd: cmd, WorkingDir: call.dir, User: selfUser(), Binds: binds, Env: call.env,
		Memory: int64(a.Cfg.BuildMemoryMB) << 20, NanoCPUs: int64(a.Cfg.BuildCPUs * 1e9), CPUShares: 128, Pids: 1024,
		Network: "none", Labels: map[string]string{labelApp: app.ID, "sentinela.helper": "compose"},
	}
	if g := socketGroup(a.Cfg.Docker); g != "" {
		spec.GroupAdd = []string{g}
	}
	if spec.WorkingDir == "" {
		spec.WorkingDir = dir
	}
	if spec.Env == nil {
		spec.Env = composeBaseEnv()
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
	limit := setupTimeout
	if call.timeout > 0 {
		limit = call.timeout
	}
	tctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	// Standard output (the rendered project of `config`) is kept up to
	// maxComposeOutput; a longer line or output fails the call instead of
	// being held whole or cut into invalid JSON.
	var out strings.Builder
	tooLarge := false
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = a.Eng.Logs(tctx, name, LogOptions{Follow: true, MaxLine: maxComposeOutput + 1}, func(stream int, s string) {
			if stream == 1 {
				if tooLarge || out.Len()+len(s)+1 > maxComposeOutput {
					tooLarge = true
					return
				}
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
		return "", fmt.Errorf("docker compose %s exited with code %d", composeVerb(args), code)
	}
	if tooLarge {
		return "", fmt.Errorf("docker compose %s printed more than %d MiB", composeVerb(args), maxComposeOutput>>20)
	}
	return out.String(), nil
}

// composeVerb is the Compose command in args (after the global flags).
func composeVerb(args []string) string {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--env-file", "-f", "--project-directory":
			i++
		default:
			return args[i]
		}
	}
	return ""
}

// composeDown removes a service's project: containers, networks, volumes,
// and the images its builds produced, also those of services an earlier
// release had.
func (a *Agent) composeDown(ctx context.Context, app *App) error {
	if _, err := a.composeHelper(ctx, app, composeCall{}, []string{"down", "--volumes", "--remove-orphans", "--rmi", "local"}, func(string, ...any) {}); err != nil {
		return err
	}
	if err := a.removeComposeImages(ctx, app.ID, nil); err != nil && app.Deployed != nil {
		for _, s := range app.Deployed.Services {
			_ = a.Eng.RemoveImage(ctx, composeImage(app.ID, s)) // most services build nothing
		}
	}
	return nil
}

// removeComposeImages removes the images the service's Compose builds
// produced (composeImagePrefix), except keep. An image still in use stays.
func (a *Agent) removeComposeImages(ctx context.Context, id string, keep []string) error {
	prefix := composeImagePrefix(id)
	refs, err := a.Eng.ListImages(ctx, prefix+"*")
	if err != nil {
		return err
	}
	for _, ref := range refs {
		if strings.HasPrefix(ref, prefix) && !slices.Contains(keep, ref) {
			_ = a.Eng.RemoveImage(ctx, ref)
		}
	}
	return nil
}

// blockedError is a deployed Compose project that cannot start again until
// the owner deploys it: the current checks refuse its file, or an image its
// builds produced is gone. Retrying does not help.
type blockedError struct{ err error }

// composeConfigTimeout bounds "compose config".
const composeConfigTimeout = 2 * time.Minute

// blocked marks err as a refusal of the project (blockedError), unless it
// is the system's (a full disk, a failed stat): that is retried, not a
// reason to stop the project until the next deploy.
func blocked(err error) error {
	var pe *fs.PathError
	var le *os.LinkError
	var se *os.SyscallError
	if errors.As(err, &pe) || errors.As(err, &le) || errors.As(err, &se) {
		return err
	}
	return &blockedError{err}
}

func (e *blockedError) Error() string { return e.err.Error() }
func (e *blockedError) Unwrap() error { return e.err }

// imageID is the form of a local image ID.
var imageID = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// adoptImages makes sure every image a build of the plan produced exists
// under its agent name (composeImage) before an `up --no-build`, which
// neither builds nor pulls it. images is composePlan.images.
//
// A deployment made before the agent named its builds runs them under the
// names its file chose. The agent gives the new name to the image ID that a
// container of the project runs now, never to what an old name points to
// (any build could have retagged that name since). An image no container
// runs is used only when the deployment's own build produced it under the
// agent's name; otherwise the service needs a new deploy.
func (a *Agent) adoptImages(ctx context.Context, app *App, images map[string]string, logf func(string, ...any)) error {
	if len(images) == 0 {
		return nil
	}
	members, err := a.projectContainers(ctx, app.ID)
	if err != nil {
		return err
	}
	users := map[string][]string{} // image -> the services that run it
	for svc, img := range images {
		users[img] = append(users[img], svc)
	}
	var missing []string
	for _, img := range slices.Sorted(maps.Keys(users)) {
		svcs := users[img]
		// The service that builds the image first, then those that run it.
		slices.SortFunc(svcs, func(x, y string) int {
			if bx, by := composeImage(app.ID, x) == img, composeImage(app.ID, y) == img; bx != by {
				if bx {
					return -1
				}
				return 1
			}
			return strings.Compare(x, y)
		})
		id, from, err := a.memberImage(ctx, members, svcs)
		if err != nil {
			return err
		}
		if id != "" {
			if err := a.Eng.TagImage(ctx, id, img); err != nil {
				return fmt.Errorf("naming the image of %s: %w", svcs[0], err)
			}
			logf("Image of %s: %s, as container %s runs it", svcs[0], img, from)
			continue
		}
		if app.Deployed != nil && app.Deployed.ComposeImages {
			ok, err := a.Eng.ImageExists(ctx, img)
			if err != nil {
				return err
			}
			if ok {
				continue
			}
		}
		missing = append(missing, svcs[0])
	}
	if len(missing) == 0 {
		return nil
	}
	hint := "deploy the service again (Update) to build it"
	if !app.UnfilteredBuilds {
		hint += `; builds run outside the services firewall, so turn on "Allow builds and downloads outside the firewall" in the service's settings first, if you trust this code`
	}
	return &blockedError{fmt.Errorf("the image built for %s is not on the server under the name Sentinela now runs (%s), and no container of the project runs it: %s",
		strings.Join(missing, ", "), composeImage(app.ID, missing[0]), hint)}
}

// memberImage is the image ID a container of the first of services that has
// one runs, and that container's name ("" when none does).
func (a *Agent) memberImage(ctx context.Context, members []Summary, services []string) (id, name string, err error) {
	for _, svc := range services {
		for _, m := range members {
			if m.Service != svc {
				continue
			}
			st, err := a.Eng.Inspect(ctx, m.Name)
			if err != nil {
				return "", "", err
			}
			if st != nil && imageID.MatchString(st.Image) {
				return st.Image, m.Name, nil
			}
		}
	}
	return "", "", nil
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

// composeInput prepares the file `config` reads: the project's Compose file
// without the env files already imported into the encrypted manager (so
// Compose does not try to reopen deleted files; their values reach it through
// the helper's environment). It is written to the agent's composeDir, never
// into the release, which a service may bind writable: a FIFO or link planted
// there must not stall or redirect the agent. Its path is returned.
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
	return a.writeComposeFile(app.ID, composeInputFile, out)
}

// composeInputFile is the file `config` reads, kept in composeDir.
const composeInputFile = "input.yaml"

// composeObjectEnv gives `up` the variables that top-level secrets and
// configs read (`environment: NAME`), under their agent names
// (composeObjectVar), with the values of the service's environment env. A
// name the service does not define stays unset, as it was for `config`.
func composeObjectEnv(env, keys []string) []string {
	vals := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		vals[k] = v
	}
	var out []string
	for _, k := range keys {
		if v, ok := vals[k]; ok {
			out = append(out, composeObjectVar(k)+"="+v)
		}
	}
	return out
}

// composeVar is the name under which the helper gets a manager variable at
// `up`. It never is a reserved name (helperReserved), so every reference in
// the rendered file resolves from the agent's own environment, never from a
// file in the project folder, and services receive every variable.
func composeVar(key string) string {
	h := sha256.Sum256([]byte("sentinela-vault/" + key))
	return "SENTINELA_VAR_" + strings.ToUpper(hex.EncodeToString(h[:8]))
}

// composeObjectVar is the variable a top-level secret or config with
// `environment: key` reads at `up`. It is apart from composeVar, whose
// references stand for vault values inside the file, so the value given here
// (the service's own variable, plain or from the vault) never changes them.
func composeObjectVar(key string) string {
	h := sha256.Sum256([]byte("sentinela-object/" + key))
	return "SENTINELA_OBJ_" + strings.ToUpper(hex.EncodeToString(h[:8]))
}

// The rendered file contains variable references, never manager values.
// Escape literal dollars before replacing known values to avoid an extra
// interpolation of a command's shell variables when Compose reads it again.
// It returns the helper environment entries those references need.
func protectComposeValues(v any, vars []EnvVar) []string {
	slices.SortFunc(vars, func(x, y EnvVar) int {
		if len(x.Value) != len(y.Value) {
			return len(y.Value) - len(x.Value)
		}
		return strings.Compare(x.Key, y.Key)
	})
	var pairs, env []string
	seen := map[string]bool{}
	for _, e := range vars {
		if e.Value != "" && !seen[e.Value] {
			seen[e.Value] = true
			pairs = append(pairs, strings.ReplaceAll(e.Value, "$", "$$"), "${"+composeVar(e.Key)+"}")
			env = append(env, composeVar(e.Key)+"="+e.Value)
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
	walk(v)
	return env
}

// releaseRoot is the release folder that holds work.
func (a *Agent) releaseRoot(id, work string) (string, error) {
	releases := filepath.Join(a.appDir(id), "releases")
	rel, err := filepath.Rel(releases, filepath.Clean(work))
	first, _, _ := strings.Cut(filepath.ToSlash(rel), "/")
	if err != nil || first == "." || first == ".." || first == "" {
		return "", fmt.Errorf("folder %q is not a release of the service", work)
	}
	return filepath.Join(releases, first), nil
}

// composeBudget refuses a plan whose containers, each at the service's
// memory limit, would take the running services past apps.total_memory_mb.
// It counts the plan being started, not the previous deployment.
func (a *Agent) composeBudget(app *App, containers int) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	total := app.MemoryMB * containers
	for _, o := range a.apps {
		if o.ID != app.ID && o.Desired == Running && o.Deployed != nil {
			total += memBudget(o)
		}
	}
	if total > a.Cfg.TotalMemoryMB {
		return fmt.Errorf("the %d containers of this project, at %d MB each, would take the running services to %d MB of memory, above the total limit of %d MB", containers, app.MemoryMB, total, a.Cfg.TotalMemoryMB)
	}
	return nil
}

// errUnfilteredBuild refuses an image build that would run outside the
// services firewall without the owner's consent (ADR-0021).
func errUnfilteredBuild(what string) error {
	return fmt.Errorf("%s builds outside the services firewall: its steps could reach the cameras, the home network and this server. Turn on \"Allow builds and downloads outside the firewall\" in the service's settings only if you trust this code", what)
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
	release, err := a.releaseRoot(app.ID, work)
	if err != nil {
		return nil, nil, nil, err
	}
	data := filepath.Join(a.appDir(app.ID), "data")
	if err := os.MkdirAll(data, 0o750); err != nil {
		return nil, nil, nil, err
	}
	mounts := []string{release, data}
	file, err := composeFile(work, app.ComposeFile)
	if err != nil {
		return nil, nil, nil, err
	}
	logf("Lendo %s", file)
	input, err := a.composeInput(app, work, file)
	if err != nil {
		return nil, nil, nil, err
	}
	defer func() { _ = os.Remove(input) }()
	envFile, err := a.composeEnvFile(app.ID)
	if err != nil {
		return nil, nil, nil, err
	}
	full := a.env(app, false)
	env, skipped := composeHelperEnv(full)
	if len(skipped) > 0 {
		logf("Variables not passed to Compose itself (reserved names, the service still gets them): %s", strings.Join(skipped, ", "))
	}
	// The input lives in the agent's folder: relative paths in it resolve from
	// the Compose file's own folder in the release, as they would by default.
	projectDir := filepath.Join(work, filepath.FromSlash(path.Dir(file)))
	// config only reads files (seconds): a short limit, so that a file a
	// service replaced with a FIFO cannot hold the agent for setupTimeout.
	raw, err := a.composeHelper(ctx, app, composeCall{dir: work, mounts: mounts, env: env, timeout: composeConfigTimeout},
		[]string{"--env-file", envFile, "-f", input, "--project-directory", projectDir, "config", "--format", "json"}, logf)
	if err != nil {
		return nil, nil, nil, err
	}
	// Containers still running from the previous `up` keep their binds while
	// Compose replaces them, so the nesting rule covers those too.
	var running []hostBind
	if members, err := a.projectContainers(ctx, app.ID); err != nil || slices.ContainsFunc(members, func(m Summary) bool { return m.Running }) {
		running = a.runningBinds(app.ID)
	}
	plan, err := planCompose([]byte(raw), composeRules{ID: app.ID, Release: release, Data: data, Running: running})
	if err != nil {
		return nil, nil, nil, &blockedError{err}
	}
	if err := plan.foreignBuilds(app.ID); err != nil {
		return nil, nil, nil, &blockedError{err}
	}
	if build && len(plan.builds) > 0 && !app.UnfilteredBuilds {
		return nil, nil, nil, errUnfilteredBuild("This Compose project (" + strings.Join(plan.builds, ", ") + ")")
	}
	// `up` pulls a missing image (or one whose policy pulls again) through
	// the daemon, on the host's network: from another registry only with
	// the same consent, checked before anything reaches the socket.
	if !app.UnfilteredBuilds {
		pulls, err := plan.foreignPulls(ctx, a.Eng.ImageExists)
		if err != nil {
			return nil, nil, nil, err
		}
		if len(pulls) > 0 {
			return nil, nil, nil, &blockedError{errForeignPull(pulls)}
		}
	}
	if err := a.composeBudget(app, len(plan.services)); err != nil {
		return nil, nil, nil, err
	}
	if err := checkHostPaths(a.appDir(app.ID), plan.hostPaths); err != nil {
		return nil, nil, nil, blocked(err)
	}
	// Missing bind folders become real folders owned by the agent now, so
	// the daemon never creates or resolves one a service could swap for a link.
	if err := prepareBindDirs(a.appDir(app.ID), plan.bindDirs); err != nil {
		return nil, nil, nil, blocked(err)
	}
	// `up --no-build` neither builds nor pulls: the images the builds made
	// must exist under the names the rendered file runs.
	if !build {
		if err := a.adoptImages(ctx, app, plan.images, logf); err != nil {
			return nil, nil, nil, err
		}
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
	// From here on the helper's environment is the fixed base plus the
	// manager values and the variables of secrets and configs, under names of
	// the agent's own (composeVar, composeObjectVar).
	upEnv := append(composeBaseEnv(), protectComposeValues(plan.doc, vars)...)
	upEnv = append(upEnv, composeObjectEnv(full, plan.objectVars)...)
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
	// Until `up` finishes, old and new containers may both run.
	if err := a.saveBinds(app.ID, append(running, plan.binds...)); err != nil {
		return nil, nil, nil, err
	}
	// The project directory is the agent's own folder: every path in the
	// rendered file is absolute, and no `.env` a service could write is read.
	dir := a.composeDir(app.ID)
	args := []string{"--env-file", envFile, "-f", rendered, "--project-directory", dir, "up", "--detach", "--remove-orphans"}
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
	if err := a.firewallGate(app.Exposure, want.Ports); err != nil {
		return nil, nil, nil, err
	}
	// The folders were checked a moment ago and may have changed since: look
	// again just before the helper runs `up`, refusing any link met now.
	if err := checkHostPaths(a.appDir(app.ID), plan.hostPaths); err != nil {
		return nil, nil, nil, err
	}
	if _, err := a.composeHelper(ctx, app, composeCall{dir: dir, mounts: mounts, env: upEnv}, args, logf); err != nil {
		return nil, nil, nil, err
	}
	if err := a.saveBinds(app.ID, plan.binds); err != nil {
		logf("Warning: could not record the folders the services mount: %v", err)
	}
	return plan.services, want.Ports, built, nil
}

// projectContainers lists the containers of a compose service: those with
// its Compose project label and its own sentinela.app label. Docker copies
// an image's labels onto its containers, so any image (another service's
// Dockerfile) can carry the Compose labels; sentinela.app is set by the
// agent on every container it creates, which overrides the image's.
func (a *Agent) projectContainers(ctx context.Context, id string) ([]Summary, error) {
	cs, err := a.Eng.List(ctx, "com.docker.compose.project="+projectName(id), labelApp+"="+id)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(cs, func(c Summary) bool { return c.App != id }), nil
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
