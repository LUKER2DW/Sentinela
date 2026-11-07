package config

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// Camera vendors. The RTSP layouts are the vendors' documented defaults
// (ported from the legacy Sentinela adapters); any other camera works as
// "generic" with its own rtsp_main/rtsp_sub paths.
const (
	VendorXMEye     = "xmeye"
	VendorHikvision = "hikvision"
	VendorDahua     = "dahua"
	VendorGeneric   = "generic"
)

type vendorLayout struct {
	main, sub string
	// inPath: credentials go in the path (the XMEye firmware parses it
	// literally); otherwise they are the URL userinfo.
	inPath bool
}

var vendors = map[string]vendorLayout{
	VendorXMEye: {
		main:   "/user={user}&password={password}&channel={channel}&stream=0.sdp?real_stream",
		sub:    "/user={user}&password={password}&channel={channel}&stream=1.sdp?real_stream",
		inPath: true,
	},
	VendorHikvision: {main: "/Streaming/Channels/{channel}01", sub: "/Streaming/Channels/{channel}02"},
	VendorDahua:     {main: "/cam/realmonitor?channel={channel}&subtype=0", sub: "/cam/realmonitor?channel={channel}&subtype=1"},
	VendorGeneric:   {},
}

// Vendors lists the supported vendor names.
func Vendors() []string {
	return []string{VendorXMEye, VendorHikvision, VendorDahua, VendorGeneric}
}

// HasDVRIP reports whether the camera is driven over DVRIP (PTZ, presets,
// light, day/night, two-way audio). Other vendors are view and record only.
func (cam *Camera) HasDVRIP() bool { return cam.Vendor == VendorXMEye }

// CameraFeatures are the optional parts of a camera (nil = vendor default).
type CameraFeatures struct {
	// PTZ: a pan/tilt/zoom motor (presets and tours too). XMEye default: on.
	PTZ *bool `yaml:"ptz"`
	// Light: a white light (spotlight). XMEye default: on.
	Light *bool `yaml:"light"`
	// Talk: a speaker for push-to-talk. XMEye default: on (DVRIP
	// backchannel); other vendors default off and, when on, use the
	// camera's RTSP/ONVIF backchannel through go2rtc.
	Talk *bool `yaml:"talk"`
}

func orDefault(v *bool, def bool) bool {
	if v == nil {
		return def
	}
	return *v
}

// HasPTZ reports whether the camera can be moved (DVRIP cameras only).
func (cam *Camera) HasPTZ() bool { return cam.HasDVRIP() && orDefault(cam.Features.PTZ, true) }

// HasLight reports whether the camera has a white light (DVRIP cameras only).
func (cam *Camera) HasLight() bool { return cam.HasDVRIP() && orDefault(cam.Features.Light, true) }

// HasTalk reports whether the camera has a speaker for push-to-talk.
func (cam *Camera) HasTalk() bool { return orDefault(cam.Features.Talk, cam.HasDVRIP()) }

// HasSubstream reports whether a second, lighter stream is known.
func (cam *Camera) HasSubstream() bool { return cam.streamPath(1) != "" }

// streamPath is the path template of stream 0 (main) or 1 (sub); "" when
// the camera has no known sub stream.
func (cam *Camera) streamPath(stream int) string {
	l := vendors[cam.Vendor]
	main, sub := l.main, l.sub
	if cam.RTSPMain != "" {
		main = cam.RTSPMain
		if cam.RTSPSub == "" {
			sub = "" // a custom main path does not imply the vendor's sub path
		}
	}
	if cam.RTSPSub != "" {
		sub = cam.RTSPSub
	}
	if stream == 1 {
		return sub
	}
	return main
}

// rtspURL fills the layout with user and password, which are real values
// or go2rtc ${VAR} placeholders. Values are inserted verbatim:
// validateStream refuses characters that would break the URL. A missing
// sub stream falls back to the main one.
func (cam *Camera) rtspURL(stream int, user, password string) string {
	p := cam.streamPath(stream)
	if p == "" {
		p = cam.streamPath(0)
	}
	r := strings.NewReplacer("{channel}", strconv.Itoa(cam.Channel), "{user}", user, "{password}", password)
	p = r.Replace(p)
	host := net.JoinHostPort(cam.Host, strconv.Itoa(cam.RTSPPort))
	if vendors[cam.Vendor].inPath {
		return "rtsp://" + host + p
	}
	return "rtsp://" + user + ":" + password + "@" + host + p
}

// Go2RTCURL is the RTSP URL with credentials as go2rtc ${VAR} placeholders,
// expanded by go2rtc from secrets.env: no secret is written to its config.
func (cam *Camera) Go2RTCURL(stream int) string {
	return cam.rtspURL(stream, "${"+cam.UserEnv+"}", "${"+cam.PasswordEnv+"}")
}

// Go2RTCBackchannel is the DVRIP source go2rtc uses for two-way audio
// (XMEye with talk on; "" otherwise: no extra DVRIP session for nothing).
func (cam *Camera) Go2RTCBackchannel() string {
	if !cam.HasDVRIP() || !cam.HasTalk() {
		return ""
	}
	return fmt.Sprintf("dvrip://${%s}:${%s}@%s?channel=%d&subtype=0&backchannel=1",
		cam.UserEnv, cam.PasswordEnv, net.JoinHostPort(cam.Host, strconv.Itoa(cam.DVRIPPort)), cam.Channel-1)
}

// validateStream checks vendor, channel, paths and the credential
// characters each URL layout can carry.
func (cam *Camera) validateStream() []error {
	var errs []error
	l, ok := vendors[cam.Vendor]
	if !ok {
		errs = append(errs, fmt.Errorf("camera %s: vendor must be one of %s", cam.ID, strings.Join(Vendors(), ", ")))
	}
	if cam.Channel < 1 || cam.Channel > 64 {
		errs = append(errs, fmt.Errorf("camera %s: channel must be 1..64", cam.ID))
	}
	if cam.Vendor == VendorGeneric && cam.RTSPMain == "" {
		errs = append(errs, fmt.Errorf("camera %s: vendor generic needs rtsp_main (e.g. /stream1)", cam.ID))
	}
	for _, p := range []string{cam.RTSPMain, cam.RTSPSub} {
		if p != "" && (!strings.HasPrefix(p, "/") || strings.ContainsAny(p, " @\\\"'\n\r\t")) {
			errs = append(errs, fmt.Errorf("camera %s: rtsp paths start with '/' and carry no spaces, quotes or '@'", cam.ID))
		}
	}
	if l.inPath {
		if strings.ContainsAny(cam.User+cam.Password, "&?/ #%\"'\\") {
			errs = append(errs, fmt.Errorf("camera %s: credentials must not contain & ? / # %% quotes, backslashes or spaces (XMEye RTSP path)", cam.ID))
		}
	} else if strings.ContainsAny(cam.User+cam.Password, ":@/?# %\"'\\") {
		errs = append(errs, fmt.Errorf("camera %s: credentials must not contain : @ / ? # %% quotes, backslashes or spaces (RTSP URL)", cam.ID))
	}
	return errs
}
