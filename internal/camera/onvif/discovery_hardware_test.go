//go:build hardware

package onvif

// Read-only WS-Discovery probe on the real LAN:
//
//	go test -tags hardware -run TestDiscoverLAN -v ./internal/camera/onvif

import (
	"context"
	"testing"
	"time"
)

func TestDiscoverLAN(t *testing.T) {
	devs, err := Discover(context.Background(), 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range devs {
		t.Logf("%s:%d name=%q hardware=%q", d.Host, d.Port, d.Name, d.Hardware)
	}
	if len(devs) == 0 {
		t.Log("no ONVIF device answered")
	}
}
