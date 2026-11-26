package onvif

import (
	"encoding/xml"
	"reflect"
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
		ns, err := ParseNotifications(data)
		if err == nil {
			for _, n := range ns {
				_, _, _ = Classify(n)
				if len(n.Topic) > maxTopicLen || len(n.Items) > maxItems {
					t.Fatalf("kept a topic of %d bytes and %d items", len(n.Topic), len(n.Items))
				}
				for k, v := range n.Items {
					if len(k) > maxItemNameLen || len(v) > maxItemValueLen {
						t.Fatalf("kept an item of %d+%d bytes", len(k), len(v))
					}
				}
			}
		}
		// The streaming decoder reads what xml.Unmarshal read.
		want, werr := referenceNotifications(data)
		if (err == nil) != (werr == nil) || err == nil && !reflect.DeepEqual(ns, want) {
			t.Fatalf("streaming %v %+v, reference %v %+v", err, ns, werr, want)
		}
		if lim, err := parseNotifications(data, 2); err == nil && (len(lim) > 2 || len(lim) != min(2, len(ns)) ||
			len(lim) > 0 && !reflect.DeepEqual(lim, ns[len(ns)-len(lim):])) {
			t.Fatalf("limited %+v of %+v", lim, ns)
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

// referenceNotifications is the former ParseNotifications (xml.Unmarshal of
// the whole reply), kept to check the streaming decoder against.
func referenceNotifications(data []byte) ([]Notification, error) {
	var r struct {
		Msgs []notificationXML `xml:"Body>PullMessagesResponse>NotificationMessage"`
	}
	if err := xml.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	out := []Notification{}
	for i := range r.Msgs {
		if n, ok := r.Msgs[i].notification(); ok {
			out = append(out, n)
		}
	}
	return out, nil
}
