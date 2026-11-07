package api

import (
	"testing"
	"time"
)

func TestQuietWindows(t *testing.T) {
	now := time.Unix(1000, 0)
	q := quietWindows{now: func() time.Time { return now }}
	if q.Quiet("cam1") {
		t.Fatal("quiet before any movement")
	}
	q.hold("cam1", presetSettle)
	q.hold("cam1", ptzSettle) // a shorter hold does not cut the goto window
	now = now.Add(10 * time.Second)
	if !q.Quiet("cam1") || q.Quiet("cam2") {
		t.Fatal("goto window")
	}
	now = now.Add(6 * time.Second)
	if q.Quiet("cam1") {
		t.Fatal("still quiet after the window")
	}
}
