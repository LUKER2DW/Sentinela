package onvif

import (
	"encoding/xml"
	"reflect"
	"testing"
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
