package onvif

import (
	"context"
	"crypto/rand"
	"encoding/xml"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Device is one ONVIF device that answered a WS-Discovery probe.
type Device struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Name     string `json:"name,omitempty"`
	Hardware string `json:"hardware,omitempty"`
}

const wsdAddr = "239.255.255.250:3702"

// Anything on the LAN can send datagrams to the discovery port, so what a
// search reads, parses and keeps is bounded. A real ProbeMatches is a few
// KB with one match.
const (
	maxDiscoveryDatagram = 32 << 10
	maxDiscoveryReplies  = 256 // datagrams parsed per search
	maxMatchesPerReply   = 4
	maxDiscovered        = 64 // devices kept per search
	maxScopeText         = 128
)

// Discover sends one WS-Discovery Probe for NetworkVideoTransmitter devices
// to the LAN multicast group and collects answers until ctx or wait ends.
// Read-only: devices answer with their service address and scopes. A device
// is listed at the address its answer came from (an answer cannot name
// another host). truncated reports that answers were left out because a
// limit was reached.
func Discover(ctx context.Context, wait time.Duration) (devs []Device, truncated bool, err error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{})
	if err != nil {
		return nil, false, fmt.Errorf("discovery: %w", err)
	}
	defer conn.Close()
	dst, err := net.ResolveUDPAddr("udp4", wsdAddr)
	if err != nil {
		return nil, false, err
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
		return nil, false, fmt.Errorf("discovery: send probe: %w", err)
	}
	// UDP multicast is lossy and some cameras ignore a probe that follows
	// another closely: repeat it once after a second (same MessageID).
	resend := time.AfterFunc(time.Second, func() { _, _ = conn.WriteToUDP([]byte(probe), dst) })
	defer resend.Stop()
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
	var col discovered
	var wg sync.WaitGroup
	for _, c := range conns {
		_ = c.SetReadDeadline(deadline)
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf := make([]byte, maxDiscoveryDatagram+1)
			for {
				n, from, err := c.ReadFromUDP(buf)
				if err != nil {
					return // deadline reached
				}
				col.add(buf[:n], from.IP)
			}
		}()
	}
	wg.Wait()
	devs, truncated = col.result()
	return devs, truncated, nil
}

// discovered collects the answers of one search within its limits.
type discovered struct {
	mu        sync.Mutex
	found     map[string]Device
	replies   int
	truncated bool
}

// add parses one datagram from the given sender.
func (c *discovered) add(data []byte, from net.IP) {
	c.mu.Lock()
	if c.replies >= maxDiscoveryReplies {
		c.truncated = true
		c.mu.Unlock()
		return
	}
	c.replies++
	c.mu.Unlock()
	if len(data) > maxDiscoveryDatagram || from == nil || from.IsUnspecified() {
		return
	}
	devs := ParseProbeMatches(data)
	if len(devs) > maxMatchesPerReply {
		devs = devs[:maxMatchesPerReply]
	}
	host := from.String()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.found == nil {
		c.found = map[string]Device{}
	}
	for _, d := range devs {
		// The device is where its answer came from; the port is its own (a
		// camera with several addresses is listed at the one it answered from).
		d.Host = host
		key := net.JoinHostPort(d.Host, strconv.Itoa(d.Port))
		if _, ok := c.found[key]; !ok && len(c.found) >= maxDiscovered {
			c.truncated = true
			continue
		}
		c.found[key] = d
	}
}

func (c *discovered) result() ([]Device, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Device, 0, len(c.found))
	for _, d := range c.found {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Host != out[j].Host {
			return out[i].Host < out[j].Host
		}
		return out[i].Port < out[j].Port
	})
	return out, c.truncated
}

// ParseProbeMatches extracts devices from a ProbeMatches message: host and
// port come from the first http XAddr, name and hardware from the scopes
// (onvif://www.onvif.org/name/…, …/hardware/…), cut to a readable length.
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
			if p, err := strconv.Atoi(u.Port()); err == nil && p > 0 && p <= 65535 {
				d.Port = p
			}
			break
		}
		if d.Host == "" {
			continue
		}
		for _, s := range strings.Fields(pm.Scopes) {
			if v, ok := strings.CutPrefix(s, "onvif://www.onvif.org/name/"); ok {
				d.Name, _ = url.PathUnescape(v)
				d.Name = cutText(d.Name)
			}
			if v, ok := strings.CutPrefix(s, "onvif://www.onvif.org/hardware/"); ok {
				d.Hardware, _ = url.PathUnescape(v)
				d.Hardware = cutText(d.Hardware)
			}
		}
		out = append(out, d)
	}
	return out
}

// cutText keeps at most maxScopeText bytes of valid UTF-8.
func cutText(s string) string {
	s = strings.ToValidUTF8(s, "")
	if len(s) <= maxScopeText {
		return s
	}
	s = s[:maxScopeText]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
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
	data, err := c.call(ctx, c.device(), "", `<GetDeviceInformation xmlns="`+devNS+`"/>`)
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
