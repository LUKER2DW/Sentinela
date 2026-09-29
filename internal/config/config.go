// Package config loads the non-secret YAML config and resolves camera
// credentials from environment variables (populated from secrets.env).
package config

import (
	"errors"
	"fmt"
	"net"
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
	StallSeconds         int `yaml:"stall_seconds"`
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
	defInt(&c.Recording.SegmentSeconds, 300)
	defInt(&c.Recording.MaxDiskPercent, 90)
	defInt(&c.Recording.DefaultRetentionDays, 15)
	defInt(&c.Recording.StallSeconds, 20)
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
	if c.Recording.MaxDiskPercent < 50 || c.Recording.MaxDiskPercent > 98 {
		errs = append(errs, errors.New("recording.max_disk_percent must be 50..98"))
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
