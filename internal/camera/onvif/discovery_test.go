package onvif

import (
	"encoding/xml"
	"fmt"
	"net"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

// Shape of an XMEye ProbeMatches answer (addresses and ids made up).
const probeMatch = `<?xml version="1.0" encoding="UTF-8"?>
<SOAP-ENV:Envelope xmlns:SOAP-ENV="http://www.w3.org/2003/05/soap-envelope" xmlns:d="http://schemas.xmlsoap.org/ws/2005/04/discovery">
<SOAP-ENV:Body><d:ProbeMatches><d:ProbeMatch>
<d:Scopes>onvif://www.onvif.org/type/video_encoder onvif://www.onvif.org/name/IPC%20Fundo onvif://www.onvif.org/hardware/IPC_NT98566</d:Scopes>
<d:XAddrs>http://192.168.1.21:8899/onvif/device_service http://[fe80::1]:8899/onvif/device_service</d:XAddrs>
</d:ProbeMatch><d:ProbeMatch>
<d:Scopes>onvif://www.onvif.org/name/Other</d:Scopes>
<d:XAddrs>http://192.168.1.22/onvif/device_service</d:XAddrs>
</d:ProbeMatch></d:ProbeMatches></SOAP-ENV:Body></SOAP-ENV:Envelope>`

func TestParseProbeMatches(t *testing.T) {
	got := ParseProbeMatches([]byte(probeMatch))
	want := []Device{
		{Host: "192.168.1.21", Port: 8899, Name: "IPC Fundo", Hardware: "IPC_NT98566"},
		{Host: "192.168.1.22", Port: 80, Name: "Other"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	if ParseProbeMatches([]byte("not xml")) != nil {
		t.Fatal("garbage parsed")
	}
}

// An answer lists the device at the address it came from, whatever host its
// XAddr names; floods of answers and devices are capped and reported.
func TestDiscoveredBoundToSenderAndBounded(t *testing.T) {
	var c discovered
	c.add([]byte(probeMatch), net.ParseIP("192.168.1.30"))
	got, truncated := c.result()
	want := []Device{
		{Host: "192.168.1.30", Port: 80, Name: "Other"},
		{Host: "192.168.1.30", Port: 8899, Name: "IPC Fundo", Hardware: "IPC_NT98566"},
	}
	if truncated || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v (truncated %v)\nwant %+v", got, truncated, want)
	}

	// Many matches in one datagram: only the first few count.
	var many strings.Builder
	many.WriteString(`<e:Envelope xmlns:e="x" xmlns:d="y"><e:Body><d:ProbeMatches>`)
	for i := range 50 {
		fmt.Fprintf(&many, `<d:ProbeMatch><d:XAddrs>http://10.0.0.1:%d/onvif/device_service</d:XAddrs></d:ProbeMatch>`, 1000+i)
	}
	many.WriteString(`</d:ProbeMatches></e:Body></e:Envelope>`)
	c = discovered{}
	c.add([]byte(many.String()), net.ParseIP("192.168.1.31"))
	if got, _ := c.result(); len(got) != maxMatchesPerReply {
		t.Fatalf("kept %d matches of one datagram", len(got))
	}

	// Many senders: the device list stops at its cap and says so.
	c = discovered{}
	for i := range maxDiscovered + 10 {
		c.add([]byte(probeMatch), net.IPv4(10, 1, byte(i/250), byte(i%250+1)))
	}
	if got, truncated := c.result(); len(got) > maxDiscovered || !truncated {
		t.Fatalf("kept %d devices, truncated %v", len(got), truncated)
	}

	// Many datagrams: parsing stops at its cap; oversized ones are ignored.
	c = discovered{}
	c.add(append([]byte(probeMatch), make([]byte, maxDiscoveryDatagram)...), net.ParseIP("192.168.1.32"))
	for range maxDiscoveryReplies {
		c.add([]byte("not xml"), net.ParseIP("192.168.1.33"))
	}
	c.add([]byte(probeMatch), net.ParseIP("192.168.1.34"))
	if got, truncated := c.result(); len(got) != 0 || !truncated {
		t.Fatalf("got %+v truncated %v", got, truncated)
	}
}

// Scope text is cut to a readable length; ports outside 1–65535 become 80.
func TestParseProbeMatchesLimits(t *testing.T) {
	long := strings.Repeat("é", 200)
	data := `<e:Envelope xmlns:e="x" xmlns:d="y"><e:Body><d:ProbeMatches><d:ProbeMatch>` +
		`<d:Scopes>onvif://www.onvif.org/name/` + long + `</d:Scopes>` +
		`<d:XAddrs>http://192.168.1.40:99999/onvif/device_service</d:XAddrs></d:ProbeMatch></d:ProbeMatches></e:Body></e:Envelope>`
	got := ParseProbeMatches([]byte(data))
	if len(got) != 1 || len(got[0].Name) > maxScopeText || !utf8.ValidString(got[0].Name) || got[0].Port != 80 {
		t.Fatalf("%+v", got)
	}
}

func TestDeviceInfoXML(t *testing.T) {
	data := `<Envelope><Body><GetDeviceInformationResponse><Manufacturer>H264DVR</Manufacturer><Model>IPC</Model>
<FirmwareVersion>V5.00</FirmwareVersion><SerialNumber>secret</SerialNumber></GetDeviceInformationResponse></Body></Envelope>`
	var r struct {
		Info DeviceInfo `xml:"Body>GetDeviceInformationResponse"`
	}
	if err := xml.Unmarshal([]byte(data), &r); err != nil {
		t.Fatal(err)
	}
	if r.Info != (DeviceInfo{Manufacturer: "H264DVR", Model: "IPC", Firmware: "V5.00"}) {
		t.Fatalf("%+v", r.Info)
	}
}
