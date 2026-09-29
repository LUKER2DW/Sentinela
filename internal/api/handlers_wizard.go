package api

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/LUKER2DW/sentinela/internal/auth/redact"
	"github.com/LUKER2DW/sentinela/internal/camera/dvrip"
	"github.com/LUKER2DW/sentinela/internal/camera/onvif"
	"github.com/LUKER2DW/sentinela/internal/config"
)

// Camera wizard (PLAN Phase 9, Wave 4): read-only helpers for adding a
// second camera. The page finds and tests a camera; adding it is done on
// the server with scripts/add-camera.sh, which keeps the password in
// /etc/sentinela/secrets.env. Nothing here writes to any camera.

// discoverCameras: POST /api/cameras/discover (admin).
func (s *Server) discoverCameras(w http.ResponseWriter, r *http.Request) {
	if !s.wizardMu.TryLock() {
		writeErr(w, http.StatusTooManyRequests, "a camera search or test is already running")
		return
	}
	defer s.wizardMu.Unlock()
	devs, err := onvif.Discover(r.Context(), 3*time.Second)
	if err != nil {
		s.Log.Warn("camera discovery", "err", err)
		writeErr(w, http.StatusBadGateway, "camera search failed on this network")
		return
	}
	type found struct {
		onvif.Device
		Known string `json:"known,omitempty"` // id of the configured camera at this address
	}
	out := make([]found, 0, len(devs))
	for _, d := range devs {
		f := found{Device: d}
		for _, c := range s.Cfg.Cameras {
			if c.Host == d.Host {
				f.Known = c.ID
			}
		}
		out = append(out, f)
	}
	writeJSON(w, http.StatusOK, out)
}

// rtspSession is the outcome of one RTSP pull during a test.
type rtspSession struct {
	Stream string `json:"stream"` // main or sub
	OK     bool   `json:"ok"`
	Video  string `json:"video,omitempty"`
	Audio  string `json:"audio,omitempty"`
	Error  string `json:"error,omitempty"`
}

type cameraTest struct {
	SuggestedID string            `json:"suggested_id"`
	Known       string            `json:"known,omitempty"`
	ONVIF       *onvif.DeviceInfo `json:"onvif,omitempty"`
	ONVIFError  string            `json:"onvif_error,omitempty"`
	Model       string            `json:"model,omitempty"`
	DVRIPError  string            `json:"dvrip_error,omitempty"`
	Approved    bool              `json:"control_approved"`
	Sessions    []rtspSession     `json:"sessions"`
	// SessionsOK: the three pulls Sentinela needs (recorder, live HD, live
	// SD) ran at the same time.
	SessionsOK bool `json:"sessions_ok"`
}

// testCamera: POST /api/cameras/test (admin) with host, ports and
// credentials. The password is used for this test only and never returned
// or logged.
func (s *Server) testCamera(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Host      string `json:"host"`
		ONVIFPort int    `json:"onvif_port"`
		DVRIPPort int    `json:"dvrip_port"`
		RTSPPort  int    `json:"rtsp_port"`
		User      string `json:"user"`
		Password  string `json:"password"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ip, err := netip.ParseAddr(body.Host)
	if err != nil || !(ip.IsPrivate() || ip.IsLinkLocalUnicast()) {
		writeErr(w, http.StatusBadRequest, "host must be a LAN IP address")
		return
	}
	if body.User == "" || body.Password == "" || strings.ContainsAny(body.User+body.Password, "&?/ #%'\"\\") {
		writeErr(w, http.StatusBadRequest, "user and password are required and must not contain & ? / # % quotes, backslashes or spaces")
		return
	}
	port := func(p, def int) int {
		if p <= 0 || p > 65535 {
			return def
		}
		return p
	}
	cam := &config.Camera{Host: ip.String(), User: body.User, Password: body.Password,
		ONVIFPort: port(body.ONVIFPort, 8899), DVRIPPort: port(body.DVRIPPort, 34567), RTSPPort: port(body.RTSPPort, 554)}
	if !s.wizardMu.TryLock() {
		writeErr(w, http.StatusTooManyRequests, "a camera search or test is already running")
		return
	}
	defer s.wizardMu.Unlock()

	res := cameraTest{SuggestedID: s.nextCameraID()}
	for _, c := range s.Cfg.Cameras {
		if c.Host == cam.Host {
			res.Known = c.ID
		}
	}
	secret := func(err error) string { return redact.Secrets(err.Error(), cam.Password) }

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	oc := onvif.NewClient(cam.Host, cam.ONVIFPort, cam.User, cam.Password)
	oc.HTTP.Timeout = dvrip.DefaultTimeout
	if info, err := oc.DeviceInformation(ctx); err != nil {
		res.ONVIFError = secret(err)
	} else {
		res.ONVIF = info
	}
	if conn, err := dvrip.Dial(ctx, net.JoinHostPort(cam.Host, fmt.Sprint(cam.DVRIPPort)), cam.User, cam.Password); err != nil {
		res.DVRIPError = secret(err)
	} else {
		if info, err := conn.SystemInfo(ctx); err != nil {
			res.DVRIPError = secret(err)
		} else {
			res.Model, _ = info["DeviceModel"].(string)
			res.Approved = dvrip.ApprovedModel(res.Model)
		}
		conn.Close()
	}

	// A configured camera already spends its RTSP sessions on the recorder
	// and go2rtc: extra pulls would only be refused.
	if res.Known != "" {
		res.Sessions = []rtspSession{}
		s.audit(r, "camera.test", cam.Host, true, fmt.Sprintf("model=%q known=%s", res.Model, res.Known))
		writeJSON(w, http.StatusOK, res)
		return
	}
	// Three simultaneous pulls: the camera must serve recorder + live HD +
	// live SD at once (this model refuses a 4th, docs/CAMERA.md §3).
	res.Sessions = make([]rtspSession, 3)
	var wg sync.WaitGroup
	for i, stream := range []int{0, 1, 0} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res.Sessions[i] = s.probeRTSP(ctx, cam, stream)
		}()
	}
	wg.Wait()
	res.SessionsOK = true
	for _, ss := range res.Sessions {
		res.SessionsOK = res.SessionsOK && ss.OK
	}
	s.audit(r, "camera.test", cam.Host, res.SessionsOK, fmt.Sprintf("model=%q", res.Model))
	writeJSON(w, http.StatusOK, res)
}

var (
	ffVideo = regexp.MustCompile(`Video: (\w+)[^\n]*?, (\d{2,5}x\d{2,5})`)
	ffAudio = regexp.MustCompile(`Audio: (\w+)[^\n]*?(\d+) Hz`)
	ffFPS   = regexp.MustCompile(`, ([\d.]+) fps`)
)

// parseProbe reads codec lines from ffmpeg's stderr ("Stream #0:0: Video:
// hevc (Main), yuv420p(tv), 2560x1440, 12 fps, …").
func parseProbe(stderr string) (video, audio string) {
	if m := ffVideo.FindStringSubmatch(stderr); m != nil {
		video = m[1] + " " + m[2]
		if f := ffFPS.FindStringSubmatch(stderr); f != nil {
			video += " " + f[1] + " fps"
		}
	}
	if m := ffAudio.FindStringSubmatch(stderr); m != nil {
		audio = m[1] + " " + m[2] + " Hz"
	}
	return video, audio
}

// probeRTSP pulls one stream for 4 s with -c copy (no decode).
func (s *Server) probeRTSP(ctx context.Context, cam *config.Camera, stream int) rtspSession {
	out := rtspSession{Stream: "main"}
	if stream == 1 {
		out.Stream = "sub"
	}
	cmd := exec.CommandContext(ctx, s.FFmpeg, "-nostdin", "-hide_banner", "-loglevel", "info",
		"-rtsp_transport", "tcp", "-timeout", "5000000", "-i", cam.RTSPURL(stream),
		"-t", "4", "-map", "0", "-c", "copy", "-f", "null", "-")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	err := cmd.Run()
	log := stderr.String()
	out.Video, out.Audio = parseProbe(log)
	out.OK = err == nil && out.Video != ""
	if !out.OK {
		msg := strings.TrimSpace(log)
		if i := strings.LastIndex(msg, "\n"); i >= 0 {
			msg = msg[i+1:]
		}
		if msg == "" && err != nil {
			msg = err.Error()
		}
		out.Error = redact.Secrets(msg, cam.Password, cam.User+":")
	}
	return out
}

// nextCameraID proposes the first free "camN".
func (s *Server) nextCameraID() string {
	for n := len(s.Cfg.Cameras) + 1; ; n++ {
		id := fmt.Sprintf("cam%d", n)
		if s.Cfg.Camera(id) == nil {
			return id
		}
	}
}
