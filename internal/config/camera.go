package config

import (
	"fmt"
	"net"
	"regexp"
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
	VendorReolink   = "reolink"
	VendorTapo      = "tapo"
	VendorUniview   = "uniview"
	VendorAxis      = "axis"
	VendorGeneric   = "generic"
)

// DefaultONVIFPath is the ONVIF device service path nearly every camera uses.
const DefaultONVIFPath = "/onvif/device_service"

type vendorLayout struct {
	main, sub string
	// inPath: credentials go in the path (the XMEye firmware parses it
	// literally); otherwise they are the URL userinfo.
	inPath bool
	// onvifPort is the brand's usual ONVIF port (0 = 80).
	onvifPort int
}

var vendors = map[string]vendorLayout{
	VendorXMEye: {
		main:      "/user={user}&password={password}&channel={channel}&stream=0.sdp?real_stream",
		sub:       "/user={user}&password={password}&channel={channel}&stream=1.sdp?real_stream",
		inPath:    true,
		onvifPort: 8899,
	},
	VendorHikvision: {main: "/Streaming/Channels/{channel}01", sub: "/Streaming/Channels/{channel}02"},
	VendorDahua:     {main: "/cam/realmonitor?channel={channel}&subtype=0", sub: "/cam/realmonitor?channel={channel}&subtype=1"},
	// Reolink's current firmware; older H.264-only models use /h264Preview_01_main
	// (type it as a generic camera's path).
	VendorReolink: {main: "/Preview_{channel2}_main", sub: "/Preview_{channel2}_sub", onvifPort: 8000},
	// TP-Link Tapo and VIGI: the "camera account" made in the Tapo app.
	VendorTapo:    {main: "/stream1", sub: "/stream2", onvifPort: 2020},
	VendorUniview: {main: "/unicast/c{channel}/s0/live", sub: "/unicast/c{channel}/s1/live"},
	VendorAxis:    {main: "/axis-media/media.amp?camera={channel}", sub: "/axis-media/media.amp?camera={channel}&resolution=640x360"},
	VendorGeneric: {},
}

// Vendors lists the supported vendor names.
func Vendors() []string {
	return []string{VendorXMEye, VendorHikvision, VendorDahua, VendorReolink, VendorTapo, VendorUniview, VendorAxis, VendorGeneric}
}

// HasDVRIP reports whether the camera is driven over DVRIP (PTZ, presets,
// light, day/night, two-way audio). Other vendors use ONVIF for events and,
// when features.ptz is on, for PTZ.
func (cam *Camera) HasDVRIP() bool { return cam.Vendor == VendorXMEye }

// UsesONVIF reports whether Sentinela talks ONVIF to the camera (events,
// clock, ONVIF PTZ). Default on; "onvif: false" for RTSP-only cameras.
func (cam *Camera) UsesONVIF() bool { return orDefault(cam.ONVIF, true) }

// PTZ drivers (Camera.PTZDriver).
const (
	PTZDVRIP = "dvrip"
	PTZONVIF = "onvif"
)

// PTZDriver is how the camera is moved: "dvrip" (XMEye), "onvif" (any other
// brand with features.ptz on and ONVIF on) or "" (no PTZ).
func (cam *Camera) PTZDriver() string {
	switch {
	case !cam.HasPTZ():
		return ""
	case cam.HasDVRIP():
		return PTZDVRIP
	default:
		return PTZONVIF
	}
}

// CameraFeatures are the optional parts of a camera (nil = vendor default).
type CameraFeatures struct {
	// PTZ: a pan/tilt/zoom motor (presets and tours too). XMEye default: on
	// (DVRIP); other brands default off and, when on, are moved over ONVIF
	// PTZ (needs onvif on).
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

// HasPTZ reports whether the camera can be moved: XMEye over DVRIP
// (default on), any other brand over ONVIF PTZ when features.ptz is on.
func (cam *Camera) HasPTZ() bool {
	if cam.HasDVRIP() {
		return orDefault(cam.Features.PTZ, true)
	}
	return cam.UsesONVIF() && orDefault(cam.Features.PTZ, false)
}

// HasLight reports whether the camera has a white light (DVRIP cameras only).
func (cam *Camera) HasLight() bool { return cam.HasDVRIP() && orDefault(cam.Features.Light, true) }

// HasTalk reports whether the camera has a speaker for push-to-talk.
func (cam *Camera) HasTalk() bool { return orDefault(cam.Features.Talk, cam.HasDVRIP()) }

// Where a camera's motion comes from (Camera.Motion).
const (
	MotionAuto   = "auto"
	MotionCamera = "camera"
	MotionVideo  = "video"
)

// MotionSource is Motion with its default (auto).
func (cam *Camera) MotionSource() string {
	if cam.Motion == "" {
		return MotionAuto
	}
	return cam.Motion
}

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
	r := strings.NewReplacer("{channel2}", fmt.Sprintf("%02d", cam.Channel), "{channel}", strconv.Itoa(cam.Channel), "{user}", user, "{password}", password)
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

// pathPlaceholders are the only braces a configured stream path may carry
// (rtspURL replaces them).
var pathPlaceholders = strings.NewReplacer("{channel2}", "", "{channel}", "", "{user}", "", "{password}", "")

// pathPlaceholdersOnly reports whether p has no '$', '{' or '}' once the
// known placeholders are taken out.
func pathPlaceholdersOnly(p string) bool {
	return !strings.ContainsAny(pathPlaceholders.Replace(p), "${}")
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
		if !pathPlaceholdersOnly(p) {
			// go2rtc expands ${VAR} from secrets.env in the generated
			// streams file, so a path must never carry "$", "{" or "}"
			// (apart from the {channel}, {user}, {password} placeholders).
			errs = append(errs, fmt.Errorf("camera %s: rtsp paths must not contain '$', '{' or '}' (only {channel} and {channel2})", cam.ID))
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

// validateONVIF checks the ONVIF switch, path and what depends on them.
func (cam *Camera) validateONVIF() []error {
	var errs []error
	if p := cam.ONVIFPath; p != "" && !ValidONVIFPath(p) {
		errs = append(errs, fmt.Errorf("camera %s: onvif_path is a path such as /onvif/device_service (letters, digits and / . _ ~ -)", cam.ID))
	}
	if !cam.UsesONVIF() {
		if cam.Motion == MotionCamera {
			errs = append(errs, fmt.Errorf("camera %s: motion: camera needs ONVIF (set onvif: true or motion: video)", cam.ID))
		}
		if !cam.HasDVRIP() && orDefault(cam.Features.PTZ, false) {
			errs = append(errs, fmt.Errorf("camera %s: PTZ on this brand works over ONVIF (set onvif: true)", cam.ID))
		}
	}
	return errs
}

// ValidONVIFPath reports whether p may be an onvif_path: an absolute path
// of letters, digits and / . _ ~ - only (no query, host, credentials or
// escapes), so it can never change where a request goes.
func ValidONVIFPath(p string) bool { return ONVIFPathPattern.MatchString(p) }

// ONVIFPathPattern is the allow-list for onvif_path.
var ONVIFPathPattern = regexp.MustCompile(`^/[A-Za-z0-9._~/-]{0,127}$`)
