package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
)

// LoadLayout reads the config without secrets, for tools that only need
// the cameras' layout (go2rtc-config): every credential is a stand-in that
// passes validation and is never used.
func LoadLayout(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	return Parse(raw, func(string) string { return "unused" })
}

// go2rtc stream names. "<id>" (main) and "<id>_sub" are for watching: they
// have no way back to the camera's speaker, so whatever a client offers to
// send there goes nowhere. "<id>_talk" (cameras with talk on) adds that way
// back, and the API hands it to operators only (stream tickets).
const (
	Go2RTCSubSuffix  = "_sub"
	Go2RTCTalkSuffix = "_talk"
)

// noBackchannel turns go2rtc's RTSP backchannel off (it is on by default and
// would link a client's audio to an ONVIF speaker).
const noBackchannel = "#backchannel=0"

// go2rtcLoopback is go2rtc's own RTSP restream (deploy/go2rtc.yaml): the
// XMEye talk stream takes its picture from the main stream there instead of
// opening another session to the camera.
const go2rtcLoopback = "rtsp://127.0.0.1:8554/"

// Go2RTCTalkSources are the sources of the camera's "<id>_talk" stream, nil
// when it has no speaker: XMEye takes the picture from the main stream and
// adds the DVRIP backchannel; other brands open the camera's RTSP stream
// with its ONVIF backchannel (one more camera session, only while someone
// talks).
func (cam *Camera) Go2RTCTalkSources() []string {
	if !cam.HasTalk() {
		return nil
	}
	if b := cam.Go2RTCBackchannel(); b != "" {
		return []string{go2rtcLoopback + cam.ID + noBackchannel, b}
	}
	return []string{cam.Go2RTCURL(0) + "#backchannel=1"}
}

// go2rtcNames are the go2rtc streams of cam.
func (cam *Camera) go2rtcNames() []string {
	names := []string{cam.ID, cam.ID + Go2RTCSubSuffix}
	if cam.HasTalk() {
		names = append(names, cam.ID+Go2RTCTalkSuffix)
	}
	return names
}

// StreamClashes maps each go2rtc stream name that two cameras would both
// have (camera "x_sub" next to camera "x") to those cameras. Such a name is
// left out of go2rtc (WriteGo2RTC) and refused for live view, so neither
// camera's viewers get the other's picture or speaker; recording pulls from
// the camera itself and goes on. A new camera with such an id is refused.
func (c *Config) StreamClashes() map[string][]string {
	by := map[string][]string{}
	for i := range c.Cameras {
		for _, n := range c.Cameras[i].go2rtcNames() {
			by[n] = append(by[n], c.Cameras[i].ID)
		}
	}
	for n, ids := range by {
		if len(ids) < 2 {
			delete(by, n)
		}
	}
	return by
}

// NewStreamClash refuses a go2rtc stream clash (StreamClashes) in cand
// that involves a camera old does not have (nil old: any clash). An
// existing clash is only warned about, so recording never stops over it.
func NewStreamClash(cand, old *Config) error {
	var errs []error
	for name, ids := range cand.StreamClashes() {
		for _, id := range ids {
			if old == nil || old.Camera(id) == nil {
				errs = append(errs, fmt.Errorf("camera id %q: its go2rtc stream %q would be shared with camera(s) %s; pick another id", id, name, strings.Join(ids, ", ")))
				break
			}
		}
	}
	slices.SortFunc(errs, func(a, b error) int { return strings.Compare(a.Error(), b.Error()) })
	return errors.Join(errs...)
}

// Go2RTCStreamOff reports whether the go2rtc stream name is left out: it
// clashes, or it is a talk stream that takes its picture from a main stream
// that clashes.
func (c *Config) Go2RTCStreamOff(name string) bool {
	clash := c.StreamClashes()
	if len(clash[name]) > 0 {
		return true
	}
	if base, ok := strings.CutSuffix(name, Go2RTCTalkSuffix); ok {
		if cam := c.Camera(base); cam != nil && cam.Go2RTCBackchannel() != "" && len(clash[base]) > 0 {
			return true
		}
	}
	return false
}

// CredentialNames are the environment names the cameras' credentials come
// from (user_env and password_env), sorted, each once. A camera's stream
// URLs need not carry them (a custom path without {user}/{password}), so
// scripts/apply-config.sh asks for these (`sentinela credential-names`)
// before it accepts credentials from the dashboard.
func (c *Config) CredentialNames() []string {
	var names []string
	for _, cam := range c.Cameras {
		for _, n := range []string{cam.UserEnv, cam.PasswordEnv} {
			if n != "" {
				names = append(names, n)
			}
		}
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// WriteGo2RTC writes go2rtc's camera streams for every camera: the main
// stream (preloaded, so the live view opens fast), "<id>_sub", and for a
// camera with a speaker "<id>_talk" (two-way audio). Credentials stay
// ${VAR} placeholders that go2rtc expands from secrets.env. Stream names
// that clash (StreamClashes) are left out.
func (c *Config) WriteGo2RTC(w io.Writer) error {
	q := strconv.Quote // URLs are validated ASCII: Go quoting is YAML-safe
	if _, err := fmt.Fprintln(w, "# Generated by `sentinela go2rtc-config` from config.yaml; do not edit."); err != nil {
		return err
	}
	fmt.Fprintln(w, "preload:")
	n := 0
	for _, cam := range c.Cameras {
		if (cam.Preload == nil || *cam.Preload) && !c.Go2RTCStreamOff(cam.ID) {
			fmt.Fprintf(w, "  %s: %s\n", cam.ID, q("video&audio"))
			n++
		}
	}
	if n == 0 {
		fmt.Fprintln(w, "  {}")
	}
	fmt.Fprintln(w, "streams:")
	n = 0
	for i := range c.Cameras {
		cam := &c.Cameras[i]
		if !c.Go2RTCStreamOff(cam.ID) {
			fmt.Fprintf(w, "  %s:\n    - %s\n", cam.ID, q(cam.Go2RTCURL(0)+noBackchannel))
			n++
		}
		if !c.Go2RTCStreamOff(cam.ID + Go2RTCSubSuffix) {
			if _, err := fmt.Fprintf(w, "  %s%s:\n    - %s\n", cam.ID, Go2RTCSubSuffix, q(cam.Go2RTCURL(1)+noBackchannel)); err != nil {
				return err
			}
			n++
		}
		if talk := cam.Go2RTCTalkSources(); talk != nil && !c.Go2RTCStreamOff(cam.ID+Go2RTCTalkSuffix) {
			n++
			fmt.Fprintf(w, "  %s%s:\n", cam.ID, Go2RTCTalkSuffix)
			for _, src := range talk {
				if _, err := fmt.Fprintf(w, "    - %s\n", q(src)); err != nil {
					return err
				}
			}
		}
	}
	if n == 0 {
		_, err := fmt.Fprintln(w, "  {}")
		return err
	}
	return nil
}
