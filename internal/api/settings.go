package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/LUKER2DW/sentinela/internal/config"
)

// Settings editor (ADR-0024). The dashboard edits config.yaml without ever
// writing to /etc: it builds a candidate from the current file plus the
// admin's changes, validates it with the server's own parser, and leaves it
// (with any new camera credentials, 0600) under data_dir/config. The host's
// sentinela-config.path unit (scripts/apply-config.sh, root) validates it
// again with the real secrets, backs up, installs, regenerates go2rtc's
// streams, restarts what needs it and rolls back when the health check
// fails. Remote access, the services platform and where updates come from
// are not editable here: they change what is exposed or what code runs.

// SettingsCamera is one camera as the editor shows it (no secrets).
type SettingsCamera struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Host          string `json:"host"`
	Vendor        string `json:"vendor"`
	Channel       int    `json:"channel"`
	RTSPMain      string `json:"rtsp_main"`
	RTSPSub       string `json:"rtsp_sub"`
	RTSPPort      int    `json:"rtsp_port"`
	DVRIPPort     int    `json:"dvrip_port"`
	ONVIFPort     int    `json:"onvif_port"`
	RetentionDays int    `json:"retention_days"`
	Record        bool   `json:"record"`
	RecordAudio   bool   `json:"record_audio"`
	Preload       bool   `json:"preload"`
	InvertPanTilt bool   `json:"invert_pan_tilt"`
	// What the camera has (config features); PTZ and light need DVRIP.
	PTZ   bool `json:"ptz"`
	Light bool `json:"light"`
	Talk  bool `json:"talk"`
	// Motion: auto, camera or video (Camera.Motion).
	Motion string `json:"motion"`
	// Credentials: only sent to change them (or for a new camera); the
	// server never returns them.
	User     string `json:"user,omitempty"`
	Password string `json:"password,omitempty"`
}

// SettingsDoc is everything the editor may change.
type SettingsDoc struct {
	Timezone   string            `json:"timezone"`
	NASHost    string            `json:"nas_host"`
	TailnetURL string            `json:"tailnet_url"`
	Recording  SettingsRecording `json:"recording"`
	Cameras    []SettingsCamera  `json:"cameras"`
	Volumes    []SettingsVolume  `json:"volumes"`
}

type SettingsRecording struct {
	SegmentSeconds       int    `json:"segment_seconds"`
	MaxDiskPercent       int    `json:"max_disk_percent"`
	DefaultRetentionDays int    `json:"default_retention_days"`
	AlertRetentionDays   int    `json:"alert_retention_days"`
	VolumeMarker         string `json:"volume_marker"`
}

type SettingsVolume struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Path  string `json:"path"`
	Kind  string `json:"kind"`
	Share string `json:"share"`
}

func (s *Server) settingsDir() string { return filepath.Join(s.Cfg.DataDir, "config") }

func (s *Server) currentSettings() SettingsDoc {
	c := s.Cfg
	doc := SettingsDoc{Timezone: c.Timezone, NASHost: c.NASHost, TailnetURL: c.Remote.TailnetURL,
		Recording: SettingsRecording{SegmentSeconds: c.Recording.SegmentSeconds, MaxDiskPercent: c.Recording.MaxDiskPercent,
			DefaultRetentionDays: c.Recording.DefaultRetentionDays, AlertRetentionDays: c.Recording.AlertRetentionDays,
			VolumeMarker: c.Recording.VolumeMarker},
		Cameras: []SettingsCamera{}, Volumes: []SettingsVolume{}}
	for _, cam := range c.Cameras {
		doc.Cameras = append(doc.Cameras, SettingsCamera{ID: cam.ID, Name: cam.Name, Host: cam.Host, Vendor: cam.Vendor,
			Channel: cam.Channel, RTSPMain: cam.RTSPMain, RTSPSub: cam.RTSPSub, RTSPPort: cam.RTSPPort, DVRIPPort: cam.DVRIPPort,
			ONVIFPort: cam.ONVIFPort, RetentionDays: cam.RetentionDays, Record: cam.Recording(), RecordAudio: cam.AudioRecording(),
			Preload: cam.Preload == nil || *cam.Preload, InvertPanTilt: cam.InvertPanTilt,
			PTZ: cam.HasPTZ(), Light: cam.HasLight(), Talk: cam.HasTalk(), Motion: cam.MotionSource()})
	}
	for _, v := range c.Volumes {
		doc.Volumes = append(doc.Volumes, SettingsVolume{ID: v.ID, Name: v.Name, Path: v.Path, Kind: v.Kind, Share: v.Share})
	}
	return doc
}

// getSettings: GET /api/settings (admin): the editable settings, whether
// the host can apply them, and the last apply's result.
func (s *Server) getSettings(w http.ResponseWriter, _ *http.Request) {
	_, pendingErr := os.Stat(filepath.Join(s.settingsDir(), "request"))
	writeJSON(w, http.StatusOK, map[string]any{
		"settings": s.currentSettings(),
		"vendors":  config.Vendors(),
		"pending":  pendingErr == nil,
		"last":     hostJSON(s.Cfg.ConfigApplyFile),
		"host":     fileExists(s.Cfg.ConfigUnitsMarker) && s.ConfigPath != "",
	})
}

var envSafe = regexp.MustCompile(`[^A-Z0-9_]`)

// credEnv names a camera's credential variables after its id (as
// scripts/add-camera.sh does): cam-2 → CAM_2_USER / CAM_2_PASS.
func credEnv(id string) (string, string) {
	base := envSafe.ReplaceAllString(strings.ToUpper(id), "_")
	return base + "_USER", base + "_PASS"
}

// buildCandidate merges doc into the current config.yaml text (other keys,
// such as apps and remote.public, are kept) and returns the new YAML, the
// credentials to add or change, and the parsed result for validation.
func (s *Server) buildCandidate(doc SettingsDoc) ([]byte, map[string]string, error) {
	raw, err := os.ReadFile(s.ConfigPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read config: %w", err)
	}
	var m map[string]any
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return nil, nil, fmt.Errorf("parse config: %w", err)
	}
	if m == nil {
		m = map[string]any{}
	}
	m["timezone"] = doc.Timezone
	if doc.NASHost != "" {
		m["nas_host"] = doc.NASHost
	} else {
		delete(m, "nas_host")
	}
	rec, _ := m["recording"].(map[string]any)
	if rec == nil {
		rec = map[string]any{}
	}
	rec["segment_seconds"] = doc.Recording.SegmentSeconds
	rec["max_disk_percent"] = doc.Recording.MaxDiskPercent
	rec["default_retention_days"] = doc.Recording.DefaultRetentionDays
	rec["alert_retention_days"] = doc.Recording.AlertRetentionDays
	rec["volume_marker"] = doc.Recording.VolumeMarker
	m["recording"] = rec
	remote, _ := m["remote"].(map[string]any)
	if remote == nil {
		remote = map[string]any{}
	}
	if doc.TailnetURL != "" {
		remote["tailnet_url"] = doc.TailnetURL
	} else {
		delete(remote, "tailnet_url")
	}
	if len(remote) > 0 {
		m["remote"] = remote
	} else {
		delete(m, "remote")
	}

	// Existing credentials stay where they are; new or changed ones go to
	// the host in the secrets request.
	known := map[string]*config.Camera{}
	for i := range s.Cfg.Cameras {
		known[s.Cfg.Cameras[i].ID] = &s.Cfg.Cameras[i]
	}
	secrets := map[string]string{}
	env := map[string]string{}
	var cams []any
	seen := map[string]bool{}
	for _, c := range doc.Cameras {
		if seen[c.ID] {
			return nil, nil, fmt.Errorf("camera id %q is used twice", c.ID)
		}
		seen[c.ID] = true
		userEnv, passEnv := credEnv(c.ID)
		if old := known[c.ID]; old != nil {
			userEnv, passEnv = old.UserEnv, old.PasswordEnv
			env[userEnv], env[passEnv] = old.User, old.Password
		}
		if c.User != "" || c.Password != "" {
			if c.User == "" || c.Password == "" {
				return nil, nil, fmt.Errorf("camera %s: give both user and password", c.ID)
			}
			secrets[userEnv], secrets[passEnv] = c.User, c.Password
			env[userEnv], env[passEnv] = c.User, c.Password
		} else if known[c.ID] == nil {
			return nil, nil, fmt.Errorf("camera %s: a new camera needs its user and password", c.ID)
		}
		cm := map[string]any{"id": c.ID, "name": c.Name, "host": c.Host, "vendor": c.Vendor,
			"user_env": userEnv, "password_env": passEnv, "record": c.Record, "record_audio": c.RecordAudio,
			"preload": c.Preload, "invert_pan_tilt": c.InvertPanTilt, "retention_days": c.RetentionDays,
			"features": map[string]any{"ptz": c.PTZ, "light": c.Light, "talk": c.Talk}}
		if c.Motion != "" && c.Motion != config.MotionAuto {
			cm["motion"] = c.Motion
		}
		for k, v := range map[string]int{"channel": c.Channel, "rtsp_port": c.RTSPPort, "dvrip_port": c.DVRIPPort, "onvif_port": c.ONVIFPort} {
			if v > 0 {
				cm[k] = v
			}
		}
		if c.RTSPMain != "" {
			cm["rtsp_main"] = c.RTSPMain
		}
		if c.RTSPSub != "" {
			cm["rtsp_sub"] = c.RTSPSub
		}
		cams = append(cams, cm)
	}
	m["cameras"] = cams
	var vols []any
	for _, v := range doc.Volumes {
		vm := map[string]any{"id": v.ID, "name": v.Name, "path": v.Path, "kind": v.Kind}
		if v.Share != "" {
			vm["share"] = v.Share
		}
		vols = append(vols, vm)
	}
	m["volumes"] = vols

	out, err := yaml.Marshal(m)
	if err != nil {
		return nil, nil, err
	}
	out = append([]byte("# Sentinela settings. Edited from System in the dashboard (ADR-0024);\n"+
		"# earlier versions are kept as config.yaml.bak-<time> next to this file.\n"), out...)
	if _, err := config.Parse(out, func(k string) string { return env[k] }); err != nil {
		return nil, nil, err
	}
	return out, secrets, nil
}

// putSettings: PUT /api/settings (admin, home or tailnet) leaves a
// validated candidate for the host to apply.
func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	if s.ConfigPath == "" || !fileExists(s.Cfg.ConfigUnitsMarker) {
		writeErr(w, http.StatusConflict, "this server cannot apply settings yet (run scripts/setup-config-apply.sh or deploy)")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	var doc SettingsDoc
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if _, err := os.Stat(filepath.Join(s.settingsDir(), "request")); err == nil {
		writeErr(w, http.StatusConflict, "other changes are being applied; wait a moment")
		return
	}
	yml, secrets, err := s.buildCandidate(doc)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := os.MkdirAll(s.settingsDir(), 0o700); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not prepare the change")
		return
	}
	write := func(name string, b []byte, mode os.FileMode) error {
		tmp := filepath.Join(s.settingsDir(), "."+name+".tmp")
		if err := os.WriteFile(tmp, b, mode); err != nil {
			return err
		}
		return os.Rename(tmp, filepath.Join(s.settingsDir(), name))
	}
	sec, _ := json.Marshal(secrets)
	err = write("pending.yaml", yml, 0o644)
	if err == nil {
		err = write("pending-secrets.json", sec, 0o600)
	}
	if err == nil {
		err = write("request", []byte("apply\n"), 0o644)
	}
	names := make([]string, 0, len(doc.Cameras))
	for _, c := range doc.Cameras {
		names = append(names, c.ID)
	}
	s.audit(r, "settings.apply", strings.Join(names, ","), err == nil, fmt.Sprintf("%d cameras, %d credentials changed", len(doc.Cameras), len(secrets)/2))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not save the change")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"pending": true})
}
