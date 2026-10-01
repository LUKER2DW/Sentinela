package apps

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Engine is the part of the Docker Engine API the agent uses. Docker is
// the real one; tests use a fake.
type Engine interface {
	Version(ctx context.Context) (string, error)
	EnsureNetwork(ctx context.Context, name, subnet, bridge string) error
	ImageExists(ctx context.Context, ref string) (bool, error)
	Pull(ctx context.Context, ref string, log func(string)) error
	Build(ctx context.Context, tag string, tarContext io.Reader, opt BuildOptions, log func(string)) error
	RemoveImage(ctx context.Context, ref string) error
	Create(ctx context.Context, name string, spec Spec) error
	Start(ctx context.Context, name string) error
	Stop(ctx context.Context, name string, grace time.Duration) error
	Remove(ctx context.Context, name string) error
	Wait(ctx context.Context, name string) (int, error)
	// Logs calls line for every log line (stream 1 = stdout, 2 = stderr).
	Logs(ctx context.Context, name string, opt LogOptions, line func(stream int, text string)) error
	// Inspect returns nil, nil when the container does not exist.
	Inspect(ctx context.Context, name string) (*ContainerState, error)
	Stats(ctx context.Context, name string) (*Sample, error)
	// List returns the containers carrying a label ("key=value").
	List(ctx context.Context, label string) ([]Summary, error)
}

// Summary is a listed container.
type Summary struct {
	Name    string
	Service string // Compose service, if any
	Running bool
}

// BuildOptions caps an image build.
type BuildOptions struct {
	MemoryBytes int64
	CPUs        float64
	Network     string
	// BuildKit uses the BuildKit builder (no resource caps; for Dockerfiles
	// that need its syntax, e.g. RUN --mount).
	BuildKit bool
}

// LogOptions selects log lines.
type LogOptions struct {
	Tail       int
	Follow     bool
	Timestamps bool
}

// Spec is a container to create (a small, safe subset of Docker's config).
type Spec struct {
	Image      string
	Cmd        []string
	Env        []string
	WorkingDir string
	User       string
	GroupAdd   []string
	Binds      []string
	// Ports maps "port/proto" to the host address (ip, port).
	Ports    map[string]HostAddr
	Memory   int64
	NanoCPUs int64
	// CPUShares is the weight against other containers (Docker default 1024).
	CPUShares int64
	Pids      int64
	Network   string
	Alias     string
	Labels    map[string]string
}

// HostAddr is where a container port is published.
type HostAddr struct {
	IP   string
	Port int
}

// ContainerState is the part of an inspect the agent needs.
type ContainerState struct {
	Status     string // created, running, exited, ...
	Running    bool
	ExitCode   int
	OOMKilled  bool
	Error      string
	StartedAt  time.Time
	FinishedAt time.Time
}

// Sample is one stats reading.
type Sample struct {
	CPUTotal    uint64 // container CPU time, ns
	SystemTotal uint64 // host CPU time, ns
	OnlineCPUs  int
	MemUsage    uint64
	MemLimit    uint64
	NetRx       uint64
	NetTx       uint64
	Pids        uint64
}

// Docker talks to the Engine API over its unix socket.
type Docker struct {
	hc *http.Client
}

// apiPrefix pins the Engine API version (Docker 25+; the server runs 29).
const apiPrefix = "http://docker/v1.44"

// NewDocker returns a client for the socket at path.
func NewDocker(socket string) *Docker {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
		MaxIdleConns:    4,
		IdleConnTimeout: 30 * time.Second,
	}
	return &Docker{hc: &http.Client{Transport: tr}}
}

// errNotFound is a 404 from the Engine.
var errNotFound = errors.New("docker: not found")

func (d *Docker) do(ctx context.Context, method, path string, q url.Values, body io.Reader, ctype string) (*http.Response, error) {
	u := apiPrefix + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	resp, err := d.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("docker: %w", err)
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil, errNotFound
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		var e struct {
			Message string `json:"message"`
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		if json.Unmarshal(b, &e) != nil || e.Message == "" {
			e.Message = strings.TrimSpace(string(b))
		}
		return nil, fmt.Errorf("docker %s %s: %d %s", method, path, resp.StatusCode, e.Message)
	}
	return resp, nil
}

// call does a request and decodes a JSON answer into out (nil: discard).
func (d *Docker) call(ctx context.Context, method, path string, q url.Values, in, out any) error {
	var body io.Reader
	ctype := ""
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body, ctype = bytes.NewReader(b), "application/json"
	}
	resp, err := d.do(ctx, method, path, q, body, ctype)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out)
}

// Version returns the Engine version.
func (d *Docker) Version(ctx context.Context) (string, error) {
	var v struct{ Version string }
	err := d.call(ctx, http.MethodGet, "/version", nil, nil, &v)
	return v.Version, err
}

// EnsureNetwork creates the services' bridge network if missing.
func (d *Docker) EnsureNetwork(ctx context.Context, name, subnet, bridge string) error {
	err := d.call(ctx, http.MethodGet, "/networks/"+name, nil, nil, nil)
	if !errors.Is(err, errNotFound) {
		return err
	}
	return d.call(ctx, http.MethodPost, "/networks/create", nil, map[string]any{
		"Name": name, "Driver": "bridge", "CheckDuplicate": true,
		"IPAM":    map[string]any{"Config": []map[string]string{{"Subnet": subnet}}},
		"Options": map[string]string{"com.docker.network.bridge.name": bridge},
		"Labels":  map[string]string{"sentinela.apps": "1"},
	}, nil)
}

// ImageExists reports whether ref is present locally.
func (d *Docker) ImageExists(ctx context.Context, ref string) (bool, error) {
	err := d.call(ctx, http.MethodGet, "/images/"+ref+"/json", nil, nil, nil)
	if errors.Is(err, errNotFound) {
		return false, nil
	}
	return err == nil, err
}

// splitRef splits "name[:tag][@digest]" for /images/create.
func splitRef(ref string) (name, tag string) {
	if i := strings.Index(ref, "@"); i >= 0 {
		return ref[:i], ref[i+1:]
	}
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		return ref[:i], ref[i+1:]
	}
	return ref, "latest"
}

// Pull downloads an image.
func (d *Docker) Pull(ctx context.Context, ref string, log func(string)) error {
	name, tag := splitRef(ref)
	resp, err := d.do(ctx, http.MethodPost, "/images/create", url.Values{"fromImage": {name}, "tag": {tag}}, nil, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return readProgress(resp.Body, func(m progressMsg) {
		// Per-layer download ticks are noise; keep the status lines.
		if m.Status != "" && m.Progress == "" && log != nil {
			log(strings.TrimSpace(m.ID + " " + m.Status))
		}
	})
}

// Build builds an image from a tar context with the classic builder,
// which honors memory and CPU caps; BuildKit when asked.
func (d *Docker) Build(ctx context.Context, tag string, tarContext io.Reader, opt BuildOptions, log func(string)) error {
	q := url.Values{"t": {tag}, "rm": {"1"}, "forcerm": {"1"}, "version": {"1"}}
	if opt.BuildKit {
		q.Set("version", "2")
	} else {
		if opt.MemoryBytes > 0 {
			q.Set("memory", strconv.FormatInt(opt.MemoryBytes, 10))
			q.Set("memswap", strconv.FormatInt(opt.MemoryBytes, 10))
		}
		if opt.CPUs > 0 {
			q.Set("cpuperiod", "100000")
			q.Set("cpuquota", strconv.Itoa(int(opt.CPUs*100000)))
			q.Set("cpushares", "128")
		}
		if opt.Network != "" {
			q.Set("networkmode", opt.Network)
		}
	}
	resp, err := d.do(ctx, http.MethodPost, "/build", q, tarContext, "application/x-tar")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return readProgress(resp.Body, func(m progressMsg) {
		if s := strings.TrimRight(m.Stream, "\n"); s != "" && log != nil {
			for _, l := range strings.Split(s, "\n") {
				log(l)
			}
		}
	})
}

type progressMsg struct {
	Stream   string `json:"stream"`
	Status   string `json:"status"`
	Progress string `json:"progress"`
	ID       string `json:"id"`
	Error    string `json:"error"`
}

// readProgress reads a JSON-lines progress stream; an "error" line fails.
func readProgress(r io.Reader, fn func(progressMsg)) error {
	dec := json.NewDecoder(r)
	for {
		var m progressMsg
		if err := dec.Decode(&m); err == io.EOF {
			return nil
		} else if err != nil {
			return fmt.Errorf("docker: progress: %w", err)
		}
		if m.Error != "" {
			return errors.New(strings.TrimSpace(m.Error))
		}
		fn(m)
	}
}

// RemoveImage deletes an image (best effort: in use = kept).
func (d *Docker) RemoveImage(ctx context.Context, ref string) error {
	err := d.call(ctx, http.MethodDelete, "/images/"+ref, nil, nil, nil)
	if errors.Is(err, errNotFound) {
		return nil
	}
	return err
}

// Create creates a container from spec, hardened the same way every time:
// no capabilities, no privilege gain, an init process, capped logs, lower
// CPU weight and OOM priority than the NVR, and no Docker restart policy
// (the agent supervises).
func (d *Docker) Create(ctx context.Context, name string, s Spec) error {
	exposed := map[string]struct{}{}
	bindings := map[string][]map[string]string{}
	for p, h := range s.Ports {
		exposed[p] = struct{}{}
		bindings[p] = []map[string]string{{"HostIp": h.IP, "HostPort": strconv.Itoa(h.Port)}}
	}
	host := map[string]any{
		"Binds":         s.Binds,
		"PortBindings":  bindings,
		"Memory":        s.Memory,
		"MemorySwap":    s.Memory, // no swap
		"NanoCpus":      s.NanoCPUs,
		"CpuShares":     s.CPUShares,
		"PidsLimit":     s.Pids,
		"CapDrop":       []string{"ALL"},
		"SecurityOpt":   []string{"no-new-privileges:true"},
		"OomScoreAdj":   500,
		"Init":          true,
		"RestartPolicy": map[string]any{"Name": "no"},
		"LogConfig":     map[string]any{"Type": "json-file", "Config": map[string]string{"max-size": "5m", "max-file": "2"}},
		"NetworkMode":   s.Network,
	}
	if len(s.GroupAdd) > 0 {
		host["GroupAdd"] = s.GroupAdd
	}
	body := map[string]any{
		"Image": s.Image, "Env": s.Env, "WorkingDir": s.WorkingDir, "User": s.User,
		"Labels": s.Labels, "ExposedPorts": exposed, "HostConfig": host,
	}
	if len(s.Cmd) > 0 {
		body["Cmd"] = s.Cmd
		body["Entrypoint"] = []string{}
	}
	if s.Network != "" && s.Alias != "" {
		body["NetworkingConfig"] = map[string]any{"EndpointsConfig": map[string]any{s.Network: map[string]any{"Aliases": []string{s.Alias}}}}
	}
	return d.call(ctx, http.MethodPost, "/containers/create", url.Values{"name": {name}}, body, nil)
}

// Start starts a container (already running is fine).
func (d *Docker) Start(ctx context.Context, name string) error {
	return d.call(ctx, http.MethodPost, "/containers/"+name+"/start", nil, nil, nil)
}

// Stop stops a container, killing it after grace.
func (d *Docker) Stop(ctx context.Context, name string, grace time.Duration) error {
	err := d.call(ctx, http.MethodPost, "/containers/"+name+"/stop", url.Values{"t": {strconv.Itoa(int(grace.Seconds()))}}, nil, nil)
	if errors.Is(err, errNotFound) {
		return nil
	}
	return err
}

// Remove force-removes a container (gone already is fine).
func (d *Docker) Remove(ctx context.Context, name string) error {
	err := d.call(ctx, http.MethodDelete, "/containers/"+name, url.Values{"force": {"1"}, "v": {"1"}}, nil, nil)
	if errors.Is(err, errNotFound) {
		return nil
	}
	return err
}

// Wait blocks until the container exits and returns its exit code.
func (d *Docker) Wait(ctx context.Context, name string) (int, error) {
	var out struct {
		StatusCode int
	}
	err := d.call(ctx, http.MethodPost, "/containers/"+name+"/wait", nil, nil, &out)
	return out.StatusCode, err
}

// Logs reads a container's log (non-TTY: multiplexed frames).
func (d *Docker) Logs(ctx context.Context, name string, opt LogOptions, line func(int, string)) error {
	q := url.Values{"stdout": {"1"}, "stderr": {"1"}}
	if opt.Tail > 0 {
		q.Set("tail", strconv.Itoa(opt.Tail))
	}
	if opt.Follow {
		q.Set("follow", "1")
	}
	if opt.Timestamps {
		q.Set("timestamps", "1")
	}
	resp, err := d.do(ctx, http.MethodGet, "/containers/"+name+"/logs", q, nil, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return demux(resp.Body, line)
}

// demux splits Docker's multiplexed log stream into lines.
func demux(r io.Reader, line func(int, string)) error {
	br := bufio.NewReader(r)
	var hdr [8]byte
	partial := map[int]string{}
	for {
		if _, err := io.ReadFull(br, hdr[:]); err != nil {
			for st, p := range partial {
				if p != "" {
					line(st, p)
				}
			}
			if err == io.EOF || errors.Is(err, io.ErrUnexpectedEOF) {
				return nil
			}
			return err
		}
		stream, n := int(hdr[0]), binary.BigEndian.Uint32(hdr[4:])
		if n > 1<<20 {
			return errors.New("docker: log frame too large")
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(br, buf); err != nil {
			return nil
		}
		text := partial[stream] + string(buf)
		parts := strings.Split(text, "\n")
		for _, p := range parts[:len(parts)-1] {
			line(stream, strings.TrimRight(p, "\r"))
		}
		partial[stream] = parts[len(parts)-1]
	}
}

// Inspect returns a container's state, or nil if it does not exist.
func (d *Docker) Inspect(ctx context.Context, name string) (*ContainerState, error) {
	var out struct {
		State struct {
			Status     string
			Running    bool
			ExitCode   int
			OOMKilled  bool
			Error      string
			StartedAt  time.Time
			FinishedAt time.Time
		}
	}
	err := d.call(ctx, http.MethodGet, "/containers/"+name+"/json", nil, nil, &out)
	if errors.Is(err, errNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	s := out.State
	return &ContainerState{Status: s.Status, Running: s.Running, ExitCode: s.ExitCode, OOMKilled: s.OOMKilled,
		Error: s.Error, StartedAt: s.StartedAt, FinishedAt: s.FinishedAt}, nil
}

// List returns the containers with a label.
func (d *Docker) List(ctx context.Context, label string) ([]Summary, error) {
	f, _ := json.Marshal(map[string][]string{"label": {label}})
	var raw []struct {
		Names  []string
		State  string
		Labels map[string]string
	}
	if err := d.call(ctx, http.MethodGet, "/containers/json", url.Values{"all": {"1"}, "filters": {string(f)}}, nil, &raw); err != nil {
		return nil, err
	}
	out := make([]Summary, 0, len(raw))
	for _, c := range raw {
		name := ""
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		out = append(out, Summary{Name: name, Service: c.Labels["com.docker.compose.service"], Running: c.State == "running"})
	}
	return out, nil
}

// Stats takes one reading (one-shot: no 1 s wait for a second sample; the
// agent computes CPU% from its own consecutive readings).
func (d *Docker) Stats(ctx context.Context, name string) (*Sample, error) {
	var raw struct {
		CPU struct {
			Usage struct {
				Total uint64 `json:"total_usage"`
			} `json:"cpu_usage"`
			System uint64 `json:"system_cpu_usage"`
			Online int    `json:"online_cpus"`
		} `json:"cpu_stats"`
		Mem struct {
			Usage uint64            `json:"usage"`
			Limit uint64            `json:"limit"`
			Stats map[string]uint64 `json:"stats"`
		} `json:"memory_stats"`
		Networks map[string]struct {
			Rx uint64 `json:"rx_bytes"`
			Tx uint64 `json:"tx_bytes"`
		} `json:"networks"`
		Pids struct {
			Current uint64 `json:"current"`
		} `json:"pids_stats"`
	}
	err := d.call(ctx, http.MethodGet, "/containers/"+name+"/stats", url.Values{"stream": {"0"}, "one-shot": {"1"}}, nil, &raw)
	if err != nil {
		return nil, err
	}
	s := &Sample{CPUTotal: raw.CPU.Usage.Total, SystemTotal: raw.CPU.System, OnlineCPUs: raw.CPU.Online,
		MemUsage: raw.Mem.Usage, MemLimit: raw.Mem.Limit, Pids: raw.Pids.Current}
	// What `docker stats` shows: usage minus reclaimable page cache.
	if c := raw.Mem.Stats["inactive_file"]; c < s.MemUsage {
		s.MemUsage -= c
	}
	for _, n := range raw.Networks {
		s.NetRx += n.Rx
		s.NetTx += n.Tx
	}
	return s, nil
}
