package onvif

import (
	"strings"
	"testing"
)

// Everything a camera sends back is untrusted: the parsers must never
// panic, and what reaches go2rtc or the UI keeps its invariants.
// Run longer with: go test -fuzz=FuzzX ./internal/camera/onvif

const soap = `<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Body>`

func FuzzParsers(f *testing.F) {
	for _, seed := range []string{
		soap + `<GetCapabilitiesResponse><Capabilities><Media><XAddr>http://10.0.0.1/onvif/Media</XAddr></Media></Capabilities></GetCapabilitiesResponse></s:Body></s:Envelope>`,
		soap + `<GetProfilesResponse><Profiles token="p"><PTZConfiguration token="c"/><VideoEncoderConfiguration><Encoding>H265</Encoding></VideoEncoderConfiguration></Profiles></GetProfilesResponse></s:Body></s:Envelope>`,
		soap + `<PullMessagesResponse><NotificationMessage><Topic>tns1:VideoSource/MotionAlarm</Topic><Message><Message><Data><SimpleItem Name="State" Value="true"/></Data></Message></Message></NotificationMessage></PullMessagesResponse></s:Body></s:Envelope>`,
		soap + `<GetSystemDateAndTimeResponse><SystemDateAndTime><UTCDateTime><Date><Year>2026</Year><Month>13</Month><Day>40</Day></Date></UTCDateTime></SystemDateAndTime></GetSystemDateAndTimeResponse></s:Body></s:Envelope>`,
		`<d:ProbeMatches xmlns:d="x"><d:ProbeMatch><d:XAddrs>http://[fe80::1]/onvif/device_service</d:XAddrs></d:ProbeMatch></d:ProbeMatches>`,
		"", "<", "\x00\xff",
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = ParseCapabilities(data)
		_, _ = ParseProfiles(data)
		_, _ = PTZProfile(data)
		_, _ = ParseSystemTime(data)
		_ = ParseProbeMatches(data)
		if ns, err := ParseNotifications(data); err == nil {
			for _, n := range ns {
				_, _, _ = Classify(n)
			}
		}
		_ = faultReason(data)
	})
}

// Preset numbers are unique and in 1..255 whatever names and tokens the
// camera reports.
func FuzzParsePresets(f *testing.F) {
	f.Add([]byte(soap + `<GetPresetsResponse><Preset token="1"><Name>Preset 1</Name></Preset><Preset token="1"><Name>1</Name></Preset><Preset token="x"><Name>Preset 999</Name></Preset></GetPresetsResponse></s:Body></s:Envelope>`))
	f.Add([]byte(soap + `<GetPresetsResponse><Preset token="255"><Name>0255</Name></Preset></GetPresetsResponse></s:Body></s:Envelope>`))
	f.Fuzz(func(t *testing.T, data []byte) {
		list, err := ParsePresets(data)
		if err != nil {
			return
		}
		seen := map[int]bool{}
		for _, p := range list {
			if p.ID < 1 || p.ID > 255 || seen[p.ID] || p.token == "" {
				t.Fatalf("bad preset %+v in %+v", p, list)
			}
			seen[p.ID] = true
		}
	})
}

// A stream address from the camera never carries a go2rtc ${VAR}
// placeholder or credentials into the path Sentinela stores.
func FuzzSplitRTSP(f *testing.F) {
	for _, s := range []string{
		"rtsp://user:pw@10.0.0.1:554/Streaming/Channels/101?transportmode=unicast",
		"rtsp://10.0.0.1/${CAM_PASS}/{x}",
		"http://10.0.0.1/x", "rtsp://[fe80::1]:8554/a", "rtsp://h:99999/x", "",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, uri string) {
		port, path := splitRTSP(uri)
		if path == "" {
			return
		}
		if strings.ContainsAny(path, "${}") {
			t.Fatalf("placeholder survived in %q (from %q)", path, uri)
		}
		if !strings.HasPrefix(path, "/") || port < 1 || port > 65535 {
			t.Fatalf("bad stream %d %q from %q", port, path, uri)
		}
	})
}
