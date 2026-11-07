package api

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// recorder of tour moves.
type tourMoves struct {
	mu    sync.Mutex
	seen  []int
	fail  bool
	moved chan int
}

func (m *tourMoves) move(_ context.Context, _ string, p int) error {
	m.mu.Lock()
	m.seen = append(m.seen, p)
	fail := m.fail
	m.mu.Unlock()
	select {
	case m.moved <- p:
	default:
	}
	if fail {
		return errors.New("camera offline")
	}
	return nil
}

func TestValidTour(t *testing.T) {
	ok := []int{1, 2, 3}
	cases := map[string]struct {
		p     []int
		dwell time.Duration
		dur   time.Duration
		good  bool
	}{
		"ok":        {ok, 10 * time.Second, time.Hour, true},
		"one":       {[]int{1}, 10 * time.Second, time.Hour, false},
		"dup":       {[]int{1, 1}, 10 * time.Second, time.Hour, false},
		"range":     {[]int{0, 256}, 10 * time.Second, time.Hour, false},
		"fast":      {ok, time.Second, time.Hour, false},
		"slow":      {ok, time.Hour, time.Hour, false},
		"short":     {ok, 10 * time.Second, time.Second, false},
		"long":      {ok, 10 * time.Second, 24 * time.Hour, false},
		"too many":  {[]int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17}, 10 * time.Second, time.Hour, false},
		"max dwell": {ok, tourMaxDwell, tourMaxDuration, true},
	}
	for name, c := range cases {
		if err := validTour(c.p, c.dwell, c.dur); (err == nil) != c.good {
			t.Errorf("%s: err=%v", name, err)
		}
	}
}

func TestTourVisitsInOrderAndStops(t *testing.T) {
	var tours ptzTours
	m := &tourMoves{moved: make(chan int, 16)}
	st, err := tours.start("cam1", "ana", []int{3, 1}, time.Millisecond, time.Minute, m.move, time.Now)
	if err != nil || !st.Running || st.StartedBy != "ana" {
		t.Fatalf("start: %+v %v", st, err)
	}
	if _, err := tours.start("cam1", "bia", []int{1, 2}, time.Millisecond, time.Minute, m.move, time.Now); !errors.Is(err, errTourBusy) {
		t.Fatalf("second tour: %v", err)
	}
	for range 4 {
		<-m.moved
	}
	if !tours.stop("cam1", "stopped by ana") {
		t.Fatal("stop: not running")
	}
	got := tours.get("cam1")
	if got.Running || got.StopReason != "stopped by ana" || got.StoppedAt.IsZero() {
		t.Fatalf("after stop: %+v", got)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, p := range m.seen[:4] {
		if want := []int{3, 1}[i%2]; p != want {
			t.Fatalf("visit %d = %d, want %d (%v)", i, p, want, m.seen)
		}
	}
	if tours.stop("cam1", "again") {
		t.Fatal("stopping a finished tour reported true")
	}
}

func TestTourEndsOnTimeAndOnFailures(t *testing.T) {
	var tours ptzTours
	m := &tourMoves{moved: make(chan int, 64)}
	if _, err := tours.start("cam1", "ana", []int{1, 2}, time.Millisecond, 30*time.Millisecond, m.move, time.Now); err != nil {
		t.Fatal(err)
	}
	waitTour(t, &tours, "cam1")
	if r := tours.get("cam1").StopReason; r != "time is up" {
		t.Fatalf("reason = %q", r)
	}

	bad := &tourMoves{fail: true, moved: make(chan int, 64)}
	if _, err := tours.start("cam1", "ana", []int{1, 2}, time.Millisecond, time.Minute, bad.move, time.Now); err != nil {
		t.Fatal(err)
	}
	waitTour(t, &tours, "cam1")
	got := tours.get("cam1")
	if got.StopReason != "the camera did not answer" || got.LastError != "camera offline" {
		t.Fatalf("after failures: %+v", got)
	}
	tours.stopAll()
}

func waitTour(t *testing.T, tours *ptzTours, cam string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for tours.get(cam).Running {
		if time.Now().After(deadline) {
			t.Fatal("tour did not end")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestTourRoutes(t *testing.T) {
	ts, srv := newTestServer(t)
	m := &tourMoves{moved: make(chan int, 64)}
	srv.tourGoto = m.move
	admin := newClient(t, ts.URL)
	if code, _, body := admin.do("POST", "/api/setup", `{"username":"admin","password":"long enough pw"}`); code != 200 {
		t.Fatalf("setup: %d %s", code, body)
	}
	if code, _, body := admin.do("PUT", "/api/cameras/cam1/tour", `{"presets":[1],"dwell_seconds":10}`); code != 400 {
		t.Fatalf("one preset: %d %s", code, body)
	}
	code, m1, body := admin.do("PUT", "/api/cameras/cam1/tour", `{"presets":[1,2],"dwell_seconds":5,"hours":0.5}`)
	if code != 200 || m1["running"] != true {
		t.Fatalf("start: %d %s", code, body)
	}
	<-m.moved
	if code, m2, _ := admin.do("GET", "/api/cameras/cam1/tour", ""); code != 200 || m2["running"] != true {
		t.Fatalf("get: %d %v", code, m2)
	}
	// Manual control ends the tour (the PTZ call itself fails: no camera).
	admin.do("POST", "/api/cameras/cam1/ptz", `{"action":"start","op":"move","dir":"left","speed":3}`)
	got := srv.tours.get("cam1")
	if got.Running || got.StopReason != "manual control by admin" {
		t.Fatalf("after manual PTZ: %+v", got)
	}
	if code, _, _ := admin.do("DELETE", "/api/cameras/cam1/tour", ""); code != 200 {
		t.Fatalf("delete: %d", code)
	}
	if code, _, _ := admin.do("GET", "/api/cameras/nope/tour", ""); code != 404 {
		t.Fatalf("unknown camera: %d", code)
	}
}
