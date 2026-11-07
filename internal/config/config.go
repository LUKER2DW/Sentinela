// Package config loads the non-secret YAML config and resolves camera
// credentials from environment variables (populated from secrets.env).
package config

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the whole runtime configuration.
type Config struct {
	Listen    string `yaml:"listen"`
	DataDir   string `yaml:"data_dir"`
	DBPath    string `yaml:"db_path"`
	Go2RTCAPI string `yaml:"go2rtc_api"`
	// SecureCookies forces Secure session cookies even on plain HTTP (which
	// breaks LAN logins). Requests over HTTPS get them anyway.
	SecureCookies bool      `yaml:"secure_cookies"`
	Recording     Recording `yaml:"recording"`
	Cameras       []Camera  `yaml:"cameras"`
	// Volumes are shown on the System and Nuvem pages (usage only).
	Volumes []Volume `yaml:"volumes"`
	// NASHost is the LAN name of the file server (ADR-0008), e.g. SENTINELA.
	NASHost string `yaml:"nas_host"`
	// DiskHealthFile is the SMART snapshot written by the host timer
	// (scripts/disk-health.sh), mounted read-only into the container.
	DiskHealthFile string `yaml:"disk_health_file"`
	// BackupStatusFile is written by the nightly host backup
	// (scripts/backup.sh, ADR-0014), mounted read-only like DiskHealthFile.
	BackupStatusFile string `yaml:"backup_status_file"`
	// UpdateStatusFile / UpdateScheduleFile come from scripts/auto-update.sh
	// and setup-auto-update.sh (ADR-0023), mounted read-only the same way.
	UpdateStatusFile   string `yaml:"update_status_file"`
	UpdateScheduleFile string `yaml:"update_schedule_file"`
	// Timezone for schedules shown to people (night mode); storage stays UTC.
	Timezone string `yaml:"timezone"`
	// PushSubject identifies the sender to push services (VAPID "sub").
	PushSubject string `yaml:"push_subject"`
	// Remote holds the access paths of the ADR-0019 trial. All off by default.
	Remote Remote `yaml:"remote"`
	// AppUpdates: where the Android app's new versions come from.
	AppUpdates AppUpdates `yaml:"app_updates"`
	// Apps are self-hosted services deployed from GitHub (ADR-0021).
	Apps Apps `yaml:"apps"`
}

// Apps configures the self-hosted services platform (ADR-0021). Off unless
// Enabled. The agent (`sentinela apps`) is the only process with the Docker
// socket; the API reaches it on a loopback address with a token.
type Apps struct {
	Enabled bool `yaml:"enabled"`
	// Agent is the agent's loopback listen address.
	Agent string `yaml:"agent"`
	// StateDir holds sources, data and state, on the system disk (never /data,
	// the recordings disk). The same path is mounted in the agent container,
	// because Docker bind-mount sources are host paths.
	StateDir string `yaml:"state_dir"`
	// Docker is the Engine API unix socket.
	Docker string `yaml:"docker"`
	// PortFirst..PortLast are the host ports services are published on.
	PortFirst int `yaml:"port_first"`
	PortLast  int `yaml:"port_last"`
	// Hosts shown in service links. LANHost and TailnetHost are detected when
	// empty; PublicHost (the router's public IP or name) enables the
	// "internet" exposure and defaults to remote.public.domain.
	LANHost     string `yaml:"lan_host"`
	TailnetHost string `yaml:"tailnet_host"`
	PublicHost  string `yaml:"public_host"`
	// Subnet of the services' Docker network (bridge "sn-apps").
	Subnet string `yaml:"subnet"`
	// Limits: per service defaults and caps, the total for running services,
	// and the one-at-a-time build/setup step.
	MaxApps       int     `yaml:"max_apps"`
	MemoryMB      int     `yaml:"memory_mb"`
	MaxMemoryMB   int     `yaml:"max_memory_mb"`
	TotalMemoryMB int     `yaml:"total_memory_mb"`
	CPUs          float64 `yaml:"cpus"`
	MaxCPUs       float64 `yaml:"max_cpus"`
	BuildCPUs     float64 `yaml:"build_cpus"`
	BuildMemoryMB int     `yaml:"build_memory_mb"`
	// BootDelaySeconds: after a host boot, services wait until the host has
	// been up this long, so the NVR starts first.
	BootDelaySeconds int `yaml:"boot_delay_seconds"`
	// Runtimes are the base images of "script" services, by name. Entries
	// here override or add to the defaults (pin digests here if wanted).
	Runtimes map[string]string `yaml:"runtimes"`
	// ComposeImage is the Docker CLI image that runs `docker compose` for
	// compose services, in a throwaway container.
	ComposeImage string `yaml:"compose_image"`
	// LocalRoot is where local project folders are picked from (read-only;
	// "none" turns local folders off). Default: the Nuvem.
	LocalRoot string `yaml:"local_root"`
}

// DefaultRuntimes are the base images offered for script services.
var DefaultRuntimes = map[string]string{
	// glibc's canvas builds support the household i5 without AVX.
	"node":        "node:22-bookworm-slim",
	"node-alpine": "node:22-alpine",
	"python":      "python:3.13-slim",
	"go":          "golang:1.26-alpine",
	"bun":         "oven/bun:1-alpine",
	"deno":        "denoland/deno:alpine",
	"static":      "busybox:1.37",
	"shell":       "alpine:3.22",
}

// AppUpdates configures the Android app update mirror (ADR-0022).
type AppUpdates struct {
	// GitHub is the "owner/repo" whose latest release (sentinela.apk +
	// app.json) the server offers to the app; "none" turns it off and
	// leaves only scripts/publish-apk.sh.
	GitHub string `yaml:"github"`
}

// DefaultAppRepo is the project's public repository.
const DefaultAppRepo = "LUKER2DW/Sentinela"

var repoPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,39}/[A-Za-z0-9._-]{1,100}$`)

// Remote configures the ADR-0019 trial paths. Requests on FunnelListen and
// Public.Listen are in a public zone (mandatory 2FA, closed door).
type Remote struct {
	// FunnelListen is where `tailscale funnel` proxies to (P3), loopback only.
	FunnelListen string    `yaml:"funnel_listen"`
	Public       PublicTLS `yaml:"public"`
	// TailnetURL is this server on the tailnet through `tailscale serve`
	// (e.g. https://sentinela.tail0.ts.net), offered to the Android app's
	// pairing QR. Optional.
	TailnetURL string `yaml:"tailnet_url"`
}

// PublicTLS is the port-forwarded HTTPS listener (P1). Empty Domain = off.
type PublicTLS struct {
	// Domain is a DNS name, or the home's fixed public IP (no domain): the
	// certificate is then a 6-day Let's Encrypt IP certificate, renewed
	// automatically (internal/publiccert).
	Domain string `yaml:"domain"`
	Listen string `yaml:"listen"`
	// Email for Let's Encrypt expiry notices (optional).
	Email string `yaml:"email"`
	// HTTP01Listen, for an IP certificate: validate with HTTP-01 on this
	// plain-HTTP listener (router WAN 80 -> here) instead of TLS-ALPN-01 on
	// WAN 443, for ISPs that block inbound 443. It answers challenges only.
	HTTP01Listen string `yaml:"http01_listen"`
	// SelfSigned, for an IP: no Let's Encrypt at all (the ISP blocks
	// inbound 80 and 443). The server keeps a 10-year self-signed
	// certificate that the Android app pins; browsers show a warning.
	SelfSigned bool `yaml:"self_signed"`
	// URL is the address people use from outside, with the router's port
	// (e.g. https://203.0.113.7:18443): invite links point there.
	URL string `yaml:"url"`
}

// Volume is a filesystem shown in the UI. Share is the SMB share name, if any.
type Volume struct {
	ID    string `yaml:"id" json:"id"`
	Name  string `yaml:"name" json:"name"`
	Path  string `yaml:"path" json:"-"`
	Share string `yaml:"share" json:"share,omitempty"`
	Kind  string `yaml:"kind" json:"kind"` // recordings | nas | backup
}

// Recording holds recorder, indexer and retention settings.
type Recording struct {
	SegmentSeconds       int `yaml:"segment_seconds"`
	MaxDiskPercent       int `yaml:"max_disk_percent"`
	DefaultRetentionDays int `yaml:"default_retention_days"`
	// AlertRetentionDays keeps segments that overlap an Alert event longer
	// (tiered retention, PLAN Phase 9 Wave 2).
	AlertRetentionDays int `yaml:"alert_retention_days"`
	StallSeconds       int `yaml:"stall_seconds"`
	// VolumeMarker is a file, relative to data_dir, that exists only on the
	// dedicated recording disk (scripts/install-server.sh creates it). While
	// it is missing (the disk did not mount) nothing is recorded, so the
	// system disk never fills up. "none" turns the check off (dev machines).
	VolumeMarker string `yaml:"volume_marker"`
}

// DefaultVolumeMarker is the marker file name on the recording disk.
const DefaultVolumeMarker = ".sentinela-volume"

// VolumeMarkerPath is the marker to check under dataDir, "" when off.
func (r Recording) VolumeMarkerPath(dataDir string) string {
	if r.VolumeMarker == "" || r.VolumeMarker == "none" {
		return ""
	}
	return filepath.Join(dataDir, filepath.FromSlash(r.VolumeMarker))
}

// Camera is one camera. Password is never read from YAML.
type Camera struct {
	ID   string `yaml:"id"`
	Name string `yaml:"name"`
	Host string `yaml:"host"`
	// Vendor picks the RTSP URL layout and the control driver: xmeye
	// (default; DVRIP control, PTZ, light), hikvision, dahua or generic
	// (rtsp_main required). Only xmeye cameras get DVRIP control.
	Vendor string `yaml:"vendor"`
	// Channel of a multi-channel device (NVR/DVR), default 1.
	Channel int `yaml:"channel"`
	// RTSPMain / RTSPSub override the vendor's stream path, e.g.
	// "/stream1". {channel} is replaced; credentials are added by Sentinela.
	RTSPMain string `yaml:"rtsp_main"`
	RTSPSub  string `yaml:"rtsp_sub"`
	// Preload keeps go2rtc connected to the main stream so the live view
	// opens at once (default true); it holds one of the camera's RTSP
	// sessions all the time.
	Preload       *bool  `yaml:"preload"`
	UserEnv       string `yaml:"user_env"`
	PasswordEnv   string `yaml:"password_env"`
	RTSPPort      int    `yaml:"rtsp_port"`
	DVRIPPort     int    `yaml:"dvrip_port"`
	ONVIFPort     int    `yaml:"onvif_port"`
	RetentionDays int    `yaml:"retention_days"`
	RecordAudio   *bool  `yaml:"record_audio"`
	Record        *bool  `yaml:"record"`
	// InvertPanTilt swaps left/right and up/down for flipped+mirrored mounts.
	InvertPanTilt bool `yaml:"invert_pan_tilt"`
	// InvertPan / InvertTilt override one axis (image mirrored only, or
	// flipped only); unset follows InvertPanTilt.
	InvertPan  *bool `yaml:"invert_pan"`
	InvertTilt *bool `yaml:"invert_tilt"`

	User     string `yaml:"-"`
	Password string `yaml:"-"`
}

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// Load reads path, applies defaults, resolves secrets from the environment and validates.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	return Parse(raw, os.Getenv)
}

// LoadApps reads the config for the apps agent (ADR-0021): defaults and
// the apps section are checked, camera credentials are neither needed nor
// read (the agent's container never gets secrets.env).
func LoadApps(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var c Config
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	c.applyDefaults()
	c.Cameras = nil
	if err := errors.Join(c.Apps.validate()...); err != nil {
		return nil, err
	}
	return &c, nil
}

// defaultTimezone is the TZ environment variable when it names a zone,
// otherwise UTC. Set timezone in config.yaml for schedules in local time.
func defaultTimezone() string {
	if tz := os.Getenv("TZ"); tz != "" {
		if _, err := time.LoadLocation(tz); err == nil {
			return tz
		}
	}
	return "UTC"
}

// Parse is Load without file I/O; getenv is injectable for tests.
func Parse(raw []byte, getenv func(string) string) (*Config, error) {
	var c Config
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	c.applyDefaults()
	for i := range c.Cameras {
		cam := &c.Cameras[i]
		cam.User = getenv(cam.UserEnv)
		cam.Password = getenv(cam.PasswordEnv)
	}
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) applyDefaults() {
	def := func(v *string, d string) {
		if *v == "" {
			*v = d
		}
	}
	defInt := func(v *int, d int) {
		if *v == 0 {
			*v = d
		}
	}
	def(&c.Listen, ":8080")
	def(&c.DataDir, "/data")
	def(&c.DBPath, c.DataDir+"/db/sentinela.db")
	def(&c.Go2RTCAPI, "http://127.0.0.1:1984")
	def(&c.DiskHealthFile, "/host/disks.json")
	def(&c.BackupStatusFile, "/host/backup.json")
	def(&c.UpdateStatusFile, "/host/update.json")
	def(&c.UpdateScheduleFile, "/host/update-schedule.json")
	def(&c.Timezone, defaultTimezone())
	def(&c.PushSubject, "mailto:admin@sentinela.invalid")
	if c.Remote.Public.Domain != "" {
		def(&c.Remote.Public.Listen, ":7443")
	}
	def(&c.Apps.Agent, "127.0.0.1:7091")
	def(&c.Apps.StateDir, "/var/lib/sentinela-apps")
	def(&c.Apps.Docker, "/var/run/docker.sock")
	def(&c.Apps.Subnet, "172.31.250.0/24")
	def(&c.Apps.ComposeImage, "docker:29-cli")
	def(&c.Apps.LocalRoot, "/srv/nas")
	if c.Apps.LocalRoot == "none" {
		c.Apps.LocalRoot = ""
	}
	defInt(&c.Apps.PortFirst, 20000)
	defInt(&c.Apps.PortLast, 20099)
	defInt(&c.Apps.MaxApps, 12)
	defInt(&c.Apps.MemoryMB, 256)
	defInt(&c.Apps.MaxMemoryMB, 1024)
	defInt(&c.Apps.TotalMemoryMB, 1024)
	defInt(&c.Apps.BuildMemoryMB, 1024)
	defInt(&c.Apps.BootDelaySeconds, 120)
	defFloat := func(v *float64, d float64) {
		if *v == 0 {
			*v = d
		}
	}
	defFloat(&c.Apps.CPUs, 0.5)
	defFloat(&c.Apps.MaxCPUs, 1.5)
	defFloat(&c.Apps.BuildCPUs, 1)
	if c.Apps.PublicHost == "" {
		c.Apps.PublicHost = c.Remote.Public.Domain
	}
	rt := make(map[string]string, len(DefaultRuntimes))
	for k, v := range DefaultRuntimes {
		rt[k] = v
	}
	for k, v := range c.Apps.Runtimes {
		rt[k] = v
	}
	c.Apps.Runtimes = rt
	defInt(&c.Recording.SegmentSeconds, 300)
	// Retention days 0 = no age limit: record until the disk reaches
	// MaxDiskPercent, then overwrite the oldest (owner decision, 2026-11-06).
	defInt(&c.Recording.MaxDiskPercent, 95)
	defInt(&c.Recording.StallSeconds, 20)
	def(&c.Recording.VolumeMarker, DefaultVolumeMarker)
	def(&c.AppUpdates.GitHub, DefaultAppRepo)
	for i := range c.Cameras {
		cam := &c.Cameras[i]
		def(&cam.Name, cam.ID)
		def(&cam.Vendor, VendorXMEye)
		defInt(&cam.Channel, 1)
		defInt(&cam.RTSPPort, 554)
		defInt(&cam.DVRIPPort, 34567)
		if cam.Vendor == VendorXMEye {
			defInt(&cam.ONVIFPort, 8899)
		} else {
			defInt(&cam.ONVIFPort, 80)
		}
		defInt(&cam.RetentionDays, c.Recording.DefaultRetentionDays)
	}
}

func (c *Config) validate() error {
	var errs []error
	if c.Recording.SegmentSeconds < 30 || c.Recording.SegmentSeconds > 3600 {
		errs = append(errs, errors.New("recording.segment_seconds must be 30..3600"))
	}
	if _, err := time.LoadLocation(c.Timezone); err != nil {
		errs = append(errs, fmt.Errorf("timezone %q: %w", c.Timezone, err))
	}
	if c.Recording.MaxDiskPercent < 50 || c.Recording.MaxDiskPercent > 98 {
		errs = append(errs, errors.New("recording.max_disk_percent must be 50..98"))
	}
	if r := c.AppUpdates.GitHub; r != "none" && !repoPattern.MatchString(r) {
		errs = append(errs, errors.New(`app_updates.github must be "owner/repo" or "none"`))
	}
	if m := c.Recording.VolumeMarker; m != "none" && (filepath.IsAbs(m) || !filepath.IsLocal(filepath.FromSlash(m))) {
		errs = append(errs, errors.New(`recording.volume_marker must be a file inside data_dir, or "none"`))
	}
	if c.Recording.DefaultRetentionDays < 0 || c.Recording.AlertRetentionDays < 0 {
		errs = append(errs, errors.New("recording retention days must be 0 (until the disk is full) or more"))
	}
	for _, cam := range c.Cameras {
		if cam.RetentionDays < 0 {
			errs = append(errs, fmt.Errorf("camera %s: retention_days must be 0 (until the disk is full) or more", cam.ID))
		}
	}
	if l := c.Remote.FunnelListen; l != "" {
		// Funnel traffic arrives from tailscaled on this host only.
		if h, _, err := net.SplitHostPort(l); err != nil || net.ParseIP(h) == nil || !net.ParseIP(h).IsLoopback() {
			errs = append(errs, errors.New("remote.funnel_listen must be a loopback host:port, e.g. 127.0.0.1:8081"))
		}
	}
	if u := c.Remote.TailnetURL; u != "" {
		if p, err := url.Parse(u); err != nil || p.Scheme != "https" || p.Host == "" || (p.Path != "" && p.Path != "/") {
			errs = append(errs, errors.New("remote.tailnet_url must be https://host"))
		}
	}
	if u := c.Remote.Public.URL; u != "" {
		if p, err := url.Parse(u); err != nil || p.Scheme != "https" || p.Host == "" || (p.Path != "" && p.Path != "/") {
			errs = append(errs, errors.New("remote.public.url must be https://host[:port]"))
		}
	}
	if d := c.Remote.Public.Domain; d != "" {
		if ip := net.ParseIP(d); ip != nil {
			// A fixed public IP instead of a name (short-lived IP certificate).
			if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() {
				errs = append(errs, errors.New("remote.public.domain: an IP must be the home's public address"))
			}
		} else if !validHostname(d) || !strings.Contains(d, ".") {
			errs = append(errs, errors.New("remote.public.domain must be a DNS name or the fixed public IP"))
		}
		if _, _, err := net.SplitHostPort(c.Remote.Public.Listen); err != nil {
			errs = append(errs, errors.New("remote.public.listen must be host:port"))
		}
		if c.Remote.Public.SelfSigned && (net.ParseIP(d) == nil || c.Remote.Public.HTTP01Listen != "") {
			errs = append(errs, errors.New("remote.public.self_signed needs an IP domain and no http01_listen"))
		}
		if l := c.Remote.Public.HTTP01Listen; l != "" {
			if _, _, err := net.SplitHostPort(l); err != nil {
				errs = append(errs, errors.New("remote.public.http01_listen must be host:port"))
			} else if net.ParseIP(d) == nil {
				errs = append(errs, errors.New("remote.public.http01_listen is only for an IP address (a domain uses autocert)"))
			}
		}
	}
	errs = append(errs, c.Apps.validate()...)
	seen := map[string]bool{}
	for _, cam := range c.Cameras {
		if !idPattern.MatchString(cam.ID) {
			errs = append(errs, fmt.Errorf("camera id %q: must match %s", cam.ID, idPattern))
		}
		if seen[cam.ID] {
			errs = append(errs, fmt.Errorf("camera id %q: duplicate", cam.ID))
		}
		seen[cam.ID] = true
		if cam.Host == "" || net.ParseIP(cam.Host) == nil && !validHostname(cam.Host) {
			errs = append(errs, fmt.Errorf("camera %s: invalid host", cam.ID))
		}
		if cam.User == "" || cam.Password == "" {
			errs = append(errs, fmt.Errorf("camera %s: credentials missing (set %s and %s)", cam.ID, cam.UserEnv, cam.PasswordEnv))
		}
		errs = append(errs, cam.validateStream()...)
	}
	return errors.Join(errs...)
}

var (
	runtimeName  = regexp.MustCompile(`^[a-z][a-z0-9-]{0,19}$`)
	imagePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._/-]*(:[A-Za-z0-9._-]+)?(@sha256:[0-9a-f]{64})?$`)
)

func (a *Apps) validate() []error {
	var errs []error
	if h, _, err := net.SplitHostPort(a.Agent); err != nil || net.ParseIP(h) == nil || !net.ParseIP(h).IsLoopback() {
		errs = append(errs, errors.New("apps.agent must be a loopback host:port, e.g. 127.0.0.1:7091"))
	}
	if !strings.HasPrefix(a.StateDir, "/") || strings.HasPrefix(a.StateDir+"/", "/data/") {
		errs = append(errs, errors.New("apps.state_dir must be an absolute path off the recordings disk (/data)"))
	}
	if a.PortFirst < 1024 || a.PortLast > 65535 || a.PortLast < a.PortFirst || a.PortLast-a.PortFirst > 999 {
		errs = append(errs, errors.New("apps.port_first..port_last must be a range of at most 1000 ports above 1023"))
	}
	if p, err := netip.ParsePrefix(a.Subnet); err != nil || !p.Addr().Is4() || !p.Addr().IsPrivate() || p.Bits() > 28 {
		errs = append(errs, errors.New("apps.subnet must be a private IPv4 prefix of /28 or larger"))
	}
	for _, h := range []string{a.LANHost, a.TailnetHost, a.PublicHost} {
		if h != "" && net.ParseIP(h) == nil && !validHostname(h) {
			errs = append(errs, fmt.Errorf("apps: invalid host %q", h))
		}
	}
	if a.MaxApps < 1 || a.MaxApps > 50 {
		errs = append(errs, errors.New("apps.max_apps must be 1..50"))
	}
	if a.MemoryMB < 32 || a.MemoryMB > a.MaxMemoryMB || a.MaxMemoryMB > a.TotalMemoryMB || a.TotalMemoryMB > 8192 || a.BuildMemoryMB < 128 {
		errs = append(errs, errors.New("apps memory: 32 <= memory_mb <= max_memory_mb <= total_memory_mb <= 8192, build_memory_mb >= 128"))
	}
	if a.CPUs < 0.05 || a.CPUs > a.MaxCPUs || a.MaxCPUs > 4 || a.BuildCPUs < 0.1 || a.BuildCPUs > 4 {
		errs = append(errs, errors.New("apps cpus: 0.05 <= cpus <= max_cpus <= 4, build_cpus 0.1..4"))
	}
	if !imagePattern.MatchString(a.ComposeImage) {
		errs = append(errs, fmt.Errorf("apps.compose_image: invalid image %q", a.ComposeImage))
	}
	if a.LocalRoot != "" && !strings.HasPrefix(a.LocalRoot, "/") {
		errs = append(errs, errors.New("apps.local_root must be an absolute path (or none)"))
	}
	for name, img := range a.Runtimes {
		if !runtimeName.MatchString(name) || !imagePattern.MatchString(img) {
			errs = append(errs, fmt.Errorf("apps.runtimes: invalid entry %q: %q", name, img))
		}
	}
	return errs
}

var hostPattern = regexp.MustCompile(`^[A-Za-z0-9.-]{1,253}$`)

func validHostname(h string) bool { return hostPattern.MatchString(h) }

// Camera returns the camera with id, or nil.
func (c *Config) Camera(id string) *Camera {
	for i := range c.Cameras {
		if c.Cameras[i].ID == id {
			return &c.Cameras[i]
		}
	}
	return nil
}

// Recording reports whether this camera is recorded (default true).
func (cam *Camera) Recording() bool { return cam.Record == nil || *cam.Record }

// AudioRecording reports whether audio is recorded (default true, see ADR-0006).
func (cam *Camera) AudioRecording() bool { return cam.RecordAudio == nil || *cam.RecordAudio }

// RTSPURL builds the camera's RTSP URL for stream 0 (main) or 1 (sub).
// It contains credentials: never log or return it; use redact.URL.
func (cam *Camera) RTSPURL(stream int) string {
	return cam.rtspURL(stream, cam.User, cam.Password)
}

// Segment returns the segment length as a duration.
func (r Recording) Segment() time.Duration { return time.Duration(r.SegmentSeconds) * time.Second }

// Stall returns the stall watchdog timeout.
func (r Recording) Stall() time.Duration { return time.Duration(r.StallSeconds) * time.Second }

// Inversion is the pan/tilt inversion of the camera's PTZ.
func (c *Camera) Inversion() (pan, tilt bool) {
	pan, tilt = c.InvertPanTilt, c.InvertPanTilt
	if c.InvertPan != nil {
		pan = *c.InvertPan
	}
	if c.InvertTilt != nil {
		tilt = *c.InvertTilt
	}
	return pan, tilt
}
