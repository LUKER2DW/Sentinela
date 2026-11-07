package onvif

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const profilesXML = `<?xml version="1.0"?><s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:trt="http://www.onvif.org/ver10/media/wsdl" xmlns:tt="http://www.onvif.org/ver10/schema"><s:Body><trt:GetProfilesResponse>
<trt:Profiles token="sub"><tt:Name>Sub</tt:Name><tt:VideoEncoderConfiguration><tt:Encoding>H264</tt:Encoding><tt:Resolution><tt:Width>640</tt:Width><tt:Height>360</tt:Height></tt:Resolution></tt:VideoEncoderConfiguration></trt:Profiles>
<trt:Profiles token="main"><tt:Name>Main</tt:Name><tt:VideoEncoderConfiguration><tt:Encoding>H265</tt:Encoding><tt:Resolution><tt:Width>2560</tt:Width><tt:Height>1440</tt:Height></tt:Resolution></tt:VideoEncoderConfiguration></trt:Profiles>
</trt:GetProfilesResponse></s:Body></s:Envelope>`

func TestStreams(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body := string(b)
		switch {
		case strings.Contains(body, "GetCapabilities"):
			_, _ = io.WriteString(w, `<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Body><GetCapabilitiesResponse><Capabilities><Media><XAddr>http://10.9.9.9/onvif/Media</XAddr></Media></Capabilities></GetCapabilitiesResponse></s:Body></s:Envelope>`)
		case strings.Contains(body, "GetProfiles"):
			if r.URL.Path != "/onvif/Media" {
				t.Errorf("media service not rebased: %s", r.URL.Path)
			}
			_, _ = io.WriteString(w, profilesXML)
		case strings.Contains(body, "<ProfileToken>main<"):
			_, _ = io.WriteString(w, `<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Body><GetStreamUriResponse><MediaUri><Uri>rtsp://admin:secret@10.9.9.9:8554/Streaming/Channels/101?transportmode=unicast</Uri></MediaUri></GetStreamUriResponse></s:Body></s:Envelope>`)
		case strings.Contains(body, "<ProfileToken>sub<"):
			_, _ = io.WriteString(w, `<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Body><GetStreamUriResponse><MediaUri><Uri>rtsp://10.9.9.9/stream2</Uri></MediaUri></GetStreamUriResponse></s:Body></s:Envelope>`)
		}
	}))
	defer ts.Close()
	c := &Client{Base: ts.URL, User: "u", Password: "p", HTTP: ts.Client()}
	got, err := c.Streams(context.Background())
	if err != nil || len(got) != 2 {
		t.Fatalf("streams: %v %+v", err, got)
	}
	if got[0].Token != "main" || got[0].Port != 8554 || got[0].Path != "/Streaming/Channels/101?transportmode=unicast" || got[0].Encoding != "H265" {
		t.Errorf("main: %+v", got[0])
	}
	if got[1].Token != "sub" || got[1].Port != 554 || got[1].Path != "/stream2" {
		t.Errorf("sub: %+v", got[1])
	}
	for _, s := range got {
		if strings.Contains(s.Path, "secret") {
			t.Fatal("credentials leaked into the path")
		}
	}
}
