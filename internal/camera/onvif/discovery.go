package onvif

import (
	"context"
	"crypto/rand"
	"encoding/xml"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// Device is one ONVIF device that answered a WS-Discovery probe.
type Device struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Name     string `json:"name,omitempty"`
	Hardware string `json:"hardware,omitempty"`
}

const wsdAddr = "239.255.255.250:3702"

// Discover sends one WS-Discovery Probe for NetworkVideoTransmitter devices
// to the LAN multicast group and collects answers until ctx or wait ends.
// Read-only: devices answer with their service address and scopes.
func Discover(ctx context.Context, wait time.Duration) ([]Device, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{})
	if err != nil {
		return nil, fmt.Errorf("discovery: %w", err)
	}
	defer conn.Close()
	dst, err := net.ResolveUDPAddr("udp4", wsdAddr)
	if err != nil {
		return nil, err
	}
	id := make([]byte, 16)
	_, _ = rand.Read(id)
	probe := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>`+
		`<e:Envelope xmlns:e="http://www.w3.org/2003/05/soap-envelope" xmlns:w="http://schemas.xmlsoap.org/ws/2004/08/addressing" `+
		`xmlns:d="http://schemas.xmlsoap.org/ws/2005/04/discovery" xmlns:dn="http://www.onvif.org/ver10/network/wsdl">`+
		`<e:Header><w:MessageID>uuid:%x-%x-%x-%x-%x</w:MessageID><w:To e:mustUnderstand="true">urn:schemas-xmlsoap-org:ws:2005:04:discovery</w:To>`+
		`<w:Action e:mustUnderstand="true">http://schemas.xmlsoap.org/ws/2005/04/discovery/Probe</w:Action></e:Header>`+
		`<e:Body><d:Probe><d:Types>dn:NetworkVideoTransmitter</d:Types></d:Probe></e:Body></e:Envelope>`,
		id[0:4], id[4:6], id[6:8], id[8:10], id[10:16])
	if _, err := conn.WriteToUDP([]byte(probe), dst); err != nil {
		return nil, fmt.Errorf("discovery: send probe: %w", err)
	}
	deadline := time.Now().Add(wait)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	// Answers come unicast to our port, but a host firewall drops those (no
	// conntrack match: the source is the camera, not the group). XMEye
	// cameras repeat the answer to the group on 3702, so listen there too.
	conns := []*net.UDPConn{conn}
	if mc, err := net.ListenMulticastUDP("udp4", nil, dst); err == nil {
		defer mc.Close()
		conns = append(conns, mc)
	}
	found := map[string]Device{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, c := range conns {
		_ = c.SetReadDeadline(deadline)
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf := make([]byte, 64<<10)
			for {
				n, _, err := c.ReadFromUDP(buf)
				if err != nil {
					return // deadline reached
				}
				mu.Lock()
				for _, d := range ParseProbeMatches(buf[:n]) {
					found[fmt.Sprintf("%s:%d", d.Host, d.Port)] = d
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	out := make([]Device, 0, len(found))
	for _, d := range found {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Host < out[j].Host })
	return out, nil
}

// ParseProbeMatches extracts devices from a ProbeMatches message: host and
// port come from the first http XAddr, name and hardware from the scopes
// (onvif://www.onvif.org/name/…, …/hardware/…).
func ParseProbeMatches(data []byte) []Device {
	var m struct {
		Matches []struct {
			XAddrs string `xml:"XAddrs"`
			Scopes string `xml:"Scopes"`
		} `xml:"Body>ProbeMatches>ProbeMatch"`
	}
	if xml.Unmarshal(data, &m) != nil {
		return nil
	}
	var out []Device
	for _, pm := range m.Matches {
		var d Device
		for _, x := range strings.Fields(pm.XAddrs) {
			u, err := url.Parse(x)
			if err != nil || u.Scheme != "http" || u.Hostname() == "" {
				continue
			}
			d.Host, d.Port = u.Hostname(), 80
			if p := u.Port(); p != "" {
				fmt.Sscan(p, &d.Port)
			}
			break
		}
		if d.Host == "" {
			continue
		}
		for _, s := range strings.Fields(pm.Scopes) {
			if v, ok := strings.CutPrefix(s, "onvif://www.onvif.org/name/"); ok {
				d.Name, _ = url.PathUnescape(v)
			}
			if v, ok := strings.CutPrefix(s, "onvif://www.onvif.org/hardware/"); ok {
				d.Hardware, _ = url.PathUnescape(v)
			}
		}
		out = append(out, d)
	}
	return out
}

// DeviceInfo is the non-identifying part of GetDeviceInformation (the
// serial number and hardware id are left out on purpose).
type DeviceInfo struct {
	Manufacturer string `json:"manufacturer" xml:"Manufacturer"`
	Model        string `json:"model" xml:"Model"`
	Firmware     string `json:"firmware" xml:"FirmwareVersion"`
}

// DeviceInformation reads GetDeviceInformation (authenticated, read-only).
func (c *Client) DeviceInformation(ctx context.Context) (*DeviceInfo, error) {
	data, err := c.call(ctx, c.Base+"/onvif/device_service", "", `<GetDeviceInformation xmlns="`+devNS+`"/>`)
	if err != nil {
		return nil, err
	}
	var r struct {
		Info DeviceInfo `xml:"Body>GetDeviceInformationResponse"`
	}
	if err := xml.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("onvif: parse device information: %w", err)
	}
	return &r.Info, nil
}
