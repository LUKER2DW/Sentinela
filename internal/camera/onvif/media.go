package onvif

import (
	"context"
	"encoding/xml"
	"errors"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// Media discovery: the RTSP addresses of a camera's stream profiles, from
// the ONVIF Media service (GetCapabilities → GetProfiles → GetStreamUri).
// Almost every IP camera implements it, so any brand can be added without a
// vendor preset. Credentials are never part of the result: Sentinela adds
// them itself when it builds the URL.

const (
	mediaNS = "http://www.onvif.org/ver10/media/wsdl"
	schemaN = "http://www.onvif.org/ver10/schema"
)

// Stream is one profile's RTSP stream.
type Stream struct {
	Token    string `json:"token"`
	Name     string `json:"name"`
	Encoding string `json:"encoding"` // H264, H265, JPEG...
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	// Port and Path (with query) of the RTSP URL, credentials removed.
	Port int    `json:"port"`
	Path string `json:"path"`
}

// mediaService finds the Media service address (falls back to the common
// /onvif/media_service path when the device does not say).
func (c *Client) mediaService(ctx context.Context) string {
	body := `<GetCapabilities xmlns="` + devNS + `"><Category>Media</Category></GetCapabilities>`
	data, err := c.call(ctx, c.Base+"/onvif/device_service", "", body)
	if err == nil {
		var r struct {
			XAddr string `xml:"Body>GetCapabilitiesResponse>Capabilities>Media>XAddr"`
		}
		if xml.Unmarshal(data, &r) == nil && strings.TrimSpace(r.XAddr) != "" {
			return c.rebase(strings.TrimSpace(r.XAddr))
		}
	}
	return c.Base + "/onvif/media_service"
}

// ParseProfiles reads a GetProfilesResponse.
func ParseProfiles(data []byte) ([]Stream, error) {
	var r struct {
		Profiles []struct {
			Token string `xml:"token,attr"`
			Name  string `xml:"Name"`
			Video struct {
				Encoding string `xml:"Encoding"`
				Width    int    `xml:"Resolution>Width"`
				Height   int    `xml:"Resolution>Height"`
			} `xml:"VideoEncoderConfiguration"`
		} `xml:"Body>GetProfilesResponse>Profiles"`
	}
	if err := xml.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	var out []Stream
	for _, p := range r.Profiles {
		if p.Token == "" {
			continue
		}
		out = append(out, Stream{Token: p.Token, Name: p.Name, Encoding: p.Video.Encoding, Width: p.Video.Width, Height: p.Video.Height})
	}
	return out, nil
}

// splitRTSP turns a device's stream URI into port and path+query, without
// credentials; "" when it is not RTSP.
func splitRTSP(uri string) (int, string) {
	u, err := url.Parse(strings.TrimSpace(uri))
	if err != nil || !strings.EqualFold(u.Scheme, "rtsp") {
		return 0, ""
	}
	port := 554
	if p := u.Port(); p != "" {
		if n, err := strconv.Atoi(p); err == nil {
			port = n
		}
	}
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	if u.RawQuery != "" {
		path += "?" + u.RawQuery
	}
	return port, path
}

// Streams lists the camera's profiles with their RTSP addresses, the
// largest picture first (main stream), then the smaller ones (sub streams).
func (c *Client) Streams(ctx context.Context) ([]Stream, error) {
	media := c.mediaService(ctx)
	data, err := c.call(ctx, media, "", `<GetProfiles xmlns="`+mediaNS+`"/>`)
	if err != nil {
		return nil, err
	}
	profiles, err := ParseProfiles(data)
	if err != nil {
		return nil, err
	}
	var out []Stream
	for _, p := range profiles {
		body := `<GetStreamUri xmlns="` + mediaNS + `"><StreamSetup><Stream xmlns="` + schemaN + `">RTP-Unicast</Stream>` +
			`<Transport xmlns="` + schemaN + `"><Protocol>RTSP</Protocol></Transport></StreamSetup>` +
			`<ProfileToken>` + xmlEscape(p.Token) + `</ProfileToken></GetStreamUri>`
		data, err := c.call(ctx, media, "", body)
		if err != nil {
			continue
		}
		var r struct {
			URI string `xml:"Body>GetStreamUriResponse>MediaUri>Uri"`
		}
		if xml.Unmarshal(data, &r) != nil {
			continue
		}
		if p.Port, p.Path = splitRTSP(r.URI); p.Path != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("onvif: the camera listed no RTSP stream")
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Width*out[i].Height > out[j].Width*out[j].Height })
	return out, nil
}

func xmlEscape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// HostPort is the camera host the client talks to (for callers that check
// it is on the LAN).
func (c *Client) HostPort() string {
	u, err := url.Parse(c.Base)
	if err != nil {
		return ""
	}
	h, _, _ := net.SplitHostPort(u.Host)
	return h
}
