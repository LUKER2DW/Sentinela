// Package config loads the non-secret YAML config and resolves camera
// credentials from environment variables (populated from secrets.env).
package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
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
	// Timezone for schedules shown to people (night mode); storage stays UTC.
	Timezone string `yaml:"timezone"`
	// PushSubject identifies the sender to push services (VAPID "sub").
	PushSubject string `yaml:"push_subject"`
	// Ntfy is the self-hosted UnifiedPush relay for the Android app (ADR-0013).
	Ntfy Ntfy `yaml:"ntfy"`
	// Remote holds the access paths of the ADR-0019 trial. All off by default.
	Remote Remote `yaml:"remote"`
}

// Remote configures the ADR-0019 trial paths. Requests on FunnelListen and
// Public.Listen are in a public zone (mandatory 2FA, closed door).
type Remote struct {
	// FunnelListen is where `tailscale funnel` proxies to (P3), loopback only.
	FunnelListen string    `yaml:"funnel_listen"`
	Public       PublicTLS `yaml:"public"`
	// AppTag is the tag of the embedded app nodes (P2).
	AppTag string `yaml:"app_tag"`
	// Tailscale OAuth client (scope auth_keys) that mints app keys (P2).
	// Read from TS_OAUTH_CLIENT_ID / TS_OAUTH_CLIENT_SECRET, never YAML.
	OAuthClientID     string `yaml:"-"`
	OAuthClientSecret string `yaml:"-"`
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
}

// Ntfy: phones reach PublicURL over the tailnet; the server posts to URL.
// An empty PublicURL disables notifications in the Android app.
type Ntfy struct {
	PublicURL string `yaml:"public_url"`
	URL       string `yaml:"url"`
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
}

// Camera is one XMEye camera. Password is never read from YAML.
type Camera struct {
	ID            string `yaml:"id"`
	Name          string `yaml:"name"`
	Host          string `yaml:"host"`
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

// Parse is Load without file I/O; getenv is injectable for tests.
func Parse(raw []byte, getenv func(string) string) (*Config, error) {
	var c Config
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	c.applyDefaults()
	c.Remote.OAuthClientID = getenv("TS_OAUTH_CLIENT_ID")
	c.Remote.OAuthClientSecret = getenv("TS_OAUTH_CLIENT_SECRET")
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
	def(&c.Timezone, "America/Sao_Paulo")
	def(&c.PushSubject, "mailto:admin@sentinela.invalid")
	def(&c.Ntfy.URL, "http://127.0.0.1:2586")
	def(&c.Remote.AppTag, "tag:sentinela-app")
	if c.Remote.Public.Domain != "" {
		def(&c.Remote.Public.Listen, ":7443")
	}
	defInt(&c.Recording.SegmentSeconds, 300)
	defInt(&c.Recording.MaxDiskPercent, 90)
	defInt(&c.Recording.DefaultRetentionDays, 15)
	defInt(&c.Recording.StallSeconds, 20)
	defInt(&c.Recording.AlertRetentionDays, 30)
	for i := range c.Cameras {
		cam := &c.Cameras[i]
		def(&cam.Name, cam.ID)
		defInt(&cam.RTSPPort, 554)
		defInt(&cam.DVRIPPort, 34567)
		defInt(&cam.ONVIFPort, 8899)
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
	if p := c.Ntfy.PublicURL; p != "" {
		if u, err := url.Parse(p); err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || strings.Trim(u.Path, "/") != "" || u.RawQuery != "" {
			errs = append(errs, errors.New("ntfy.public_url must be https://host[:port] without a path"))
		}
	}
	if u, err := url.Parse(c.Ntfy.URL); err != nil || u.Scheme != "http" && u.Scheme != "https" || u.Host == "" {
		errs = append(errs, errors.New("ntfy.url must be an http(s) URL"))
	}
	if l := c.Remote.FunnelListen; l != "" {
		// Funnel traffic arrives from tailscaled on this host only.
		if h, _, err := net.SplitHostPort(l); err != nil || net.ParseIP(h) == nil || !net.ParseIP(h).IsLoopback() {
			errs = append(errs, errors.New("remote.funnel_listen must be a loopback host:port, e.g. 127.0.0.1:8081"))
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
		if l := c.Remote.Public.HTTP01Listen; l != "" {
			if _, _, err := net.SplitHostPort(l); err != nil {
				errs = append(errs, errors.New("remote.public.http01_listen must be host:port"))
			} else if net.ParseIP(d) == nil {
				errs = append(errs, errors.New("remote.public.http01_listen is only for an IP address (a domain uses autocert)"))
			}
		}
	}
	if !strings.HasPrefix(c.Remote.AppTag, "tag:") {
		errs = append(errs, errors.New("remote.app_tag must be a Tailscale tag such as tag:sentinela-app"))
	}
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
		if strings.ContainsAny(cam.User+cam.Password, "&?/ ") {
			errs = append(errs, fmt.Errorf("camera %s: credentials must not contain '&', '?', '/' or spaces (XMEye RTSP path)", cam.ID))
		}
	}
	return errors.Join(errs...)
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

// RTSPURL builds the XMEye RTSP URL for stream 0 (main) or 1 (sub).
// It contains credentials: never log or return it; use redact.URL.
func (cam *Camera) RTSPURL(stream int) string {
	// The firmware parses this path literally: values are not percent-encoded
	// (verified with a password containing '@'). '&' in credentials is rejected
	// by validate for that reason.
	host := net.JoinHostPort(cam.Host, fmt.Sprint(cam.RTSPPort))
	return fmt.Sprintf("rtsp://%s/user=%s&password=%s&channel=1&stream=%d.sdp?real_stream", host, cam.User, cam.Password, stream)
}

// Segment returns the segment length as a duration.
func (r Recording) Segment() time.Duration { return time.Duration(r.SegmentSeconds) * time.Second }

// Stall returns the stall watchdog timeout.
func (r Recording) Stall() time.Duration { return time.Duration(r.StallSeconds) * time.Second }
