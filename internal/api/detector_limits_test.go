package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// What the detector stores of the objects it saw is the allow-listed
// fields, zone names as settings allow them, within eventMetaMax.
func TestDetectorEventMetaBounded(t *testing.T) {
	ts, srv := newTestServer(t)
	tok := srv.det.token
	zone := strings.Repeat("<", 200)
	zones := make([]string, maxZones)
	for i := range zones {
		zones[i] = zone
	}
	obj := map[string]any{"x": 0.1, "y": 0.2, "w": 0.3, "h": 0.4, "score": 0.9, "zones": zones, "evil": strings.Repeat("&", 2000)}
	objs := make([]any, 50)
	for i := range objs {
		objs[i] = obj
	}
	body, _ := json.Marshal(map[string]any{"camera": "cam1", "kind": "person", "active": true, "objects": objs})
	code, resp := detectorReq(t, srv, ts.URL, "POST", "/internal/detector/event", tok, string(body))
	var r struct{ Event int64 }
	if _ = json.Unmarshal([]byte(resp), &r); code != 200 || r.Event == 0 {
		t.Fatalf("event %d %s", code, resp)
	}
	e, err := srv.Store.EventByID(t.Context(), r.Event)
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Meta) > eventMetaMax || strings.Contains(e.Meta, "evil") || strings.Contains(e.Meta, "&") {
		t.Fatalf("meta of %d bytes: %.200s", len(e.Meta), e.Meta)
	}
	var meta struct {
		Source  string           `json:"source"`
		Count   int              `json:"count"`
		Objects []detectedObject `json:"objects"`
	}
	if err := json.Unmarshal([]byte(e.Meta), &meta); err != nil || meta.Source != "detector" || meta.Count != 50 || len(meta.Objects) == 0 {
		t.Fatalf("meta %v %+v", err, meta)
	}
	if o := meta.Objects[0]; len(o.Zones) != maxZones || utf8.RuneCountInString(o.Zones[0]) != 40 || o.Score != 0.9 {
		t.Fatalf("object %+v", o)
	}
	// An object that is not an object is refused.
	if code, _ := detectorReq(t, srv, ts.URL, "POST", "/internal/detector/event", tok, `{"camera":"cam1","kind":"vehicle","active":true,"objects":["<<<<"]}`); code != 400 {
		t.Fatalf("string object accepted: %d", code)
	}
}

// Objects and zones are decoded one at a time and refused past their
// limits: a body of tiny elements ("[{},{},…]", up to detectorEventMax)
// no longer allocates one decoded value per element.
func TestDetectorEventListsBounded(t *testing.T) {
	ts, srv := newTestServer(t)
	tok := srv.det.token
	event := func(objects string) string {
		return `{"camera":"cam1","kind":"person","active":true,"objects":` + objects + `}`
	}
	list := func(elem string, n int) string {
		return "[" + strings.TrimSuffix(strings.Repeat(elem+",", n), ",") + "]"
	}
	zones := func(n int) string { return list(`{"zones":`+list(`"z"`, n)+`}`, 1) }
	for _, c := range []struct {
		objects string
		code    int
	}{
		{"null", 200},
		{list("{}", maxEventObjects), 200},
		{list("{}", maxEventObjects+1), 400},
		{zones(maxZones), 200},
		{zones(maxZones + 1), 400},
		{`{"x":1}`, 400},
		{`[{"zones":"z"}]`, 400},
	} {
		code, body := detectorReq(t, srv, ts.URL, "POST", "/internal/detector/event", tok, event(c.objects))
		if code != c.code {
			t.Errorf("objects %.60s: %d %s, want %d", c.objects, code, body, c.code)
		}
		detectorReq(t, srv, ts.URL, "POST", "/internal/detector/event", tok, `{"camera":"cam1","kind":"person","active":false}`)
	}

	// Bodies just under detectorEventMax: allocation stays near the body
	// size (it was ~329 MiB for the object list).
	fill := func(elem string) string {
		n := (detectorEventMax - 200) / (len(elem) + 1)
		return list(elem, n)
	}
	for name, objects := range map[string]string{
		"objects": fill("{}"),
		"zones":   `[{"zones":` + fill(`""`) + `}]`,
	} {
		body := event(objects)
		if len(body) > detectorEventMax {
			t.Fatalf("%s: body of %d bytes", name, len(body))
		}
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		rec := httptest.NewRecorder()
		srv.detectorEvent(rec, httptest.NewRequest("POST", "/internal/detector/event", strings.NewReader(body)))
		runtime.ReadMemStats(&after)
		if rec.Code != 400 {
			t.Fatalf("%s: %d", name, rec.Code)
		}
		if alloc := after.TotalAlloc - before.TotalAlloc; alloc > 32<<20 {
			t.Errorf("%s: allocated %d MiB for a %d KiB body", name, alloc>>20, len(body)>>10)
		}
	}
}

// A detector that toggles events cannot store them without limit, nor a
// picture for each.
func TestDetectorEventRateAndSnapshots(t *testing.T) {
	ts, srv := newTestServer(t)
	tok := srv.det.token
	jpg := base64.StdEncoding.EncodeToString(append([]byte{0xFF, 0xD8}, make([]byte, 1000)...))
	start := `{"camera":"cam1","kind":"person","active":true,"objects":[],"jpeg":"` + jpg + `"}`
	stop := `{"camera":"cam1","kind":"person","active":false,"objects":[]}`
	opened, limited := 0, 0
	for range detectorOpens + 10 {
		switch code, body := detectorReq(t, srv, ts.URL, "POST", "/internal/detector/event", tok, start); code {
		case 200:
			opened++
		case 429:
			limited++
		default:
			t.Fatalf("start = %d %s", code, body)
		}
		detectorReq(t, srv, ts.URL, "POST", "/internal/detector/event", tok, stop)
	}
	if opened != detectorOpens || limited != 10 {
		t.Fatalf("opened %d, limited %d", opened, limited)
	}
	ev, _ := srv.Store.EventsBetween(t.Context(), "cam1", time.Now().Add(-time.Minute), time.Now().Add(time.Minute), 1000)
	if len(ev) != detectorOpens {
		t.Fatalf("stored %d events", len(ev))
	}
	files, _ := os.ReadDir(filepath.Join(srv.Cfg.DataDir, "snapshots", "cam1"))
	if len(files) != 1 {
		t.Fatalf("snapshot files = %d, want 1 (one per camera per %v)", len(files), snapshotEvery)
	}
}

// Each kind has its own allowance: video motion that used up its own does
// not keep a person from being stored and notified. The allowance refills
// one event per detectorOpenEvery, never past detectorOpens.
func TestDetectorAllowancePerKind(t *testing.T) {
	ts, srv := newTestServer(t)
	tok := srv.det.token
	post := func(kind string, active bool) int {
		body := fmt.Sprintf(`{"camera":"cam1","kind":%q,"active":%t,"objects":[]}`, kind, active)
		code, _ := detectorReq(t, srv, ts.URL, "POST", "/internal/detector/event", tok, body)
		return code
	}
	for range detectorOpens {
		if post("motion", true) != 200 || post("motion", false) != 200 {
			t.Fatal("motion within its allowance refused")
		}
	}
	if code := post("motion", true); code != 429 {
		t.Fatalf("motion past its allowance = %d", code)
	}
	for _, kind := range []string{"person", "vehicle", "animal"} {
		if code := post(kind, true); code != 200 {
			t.Fatalf("%s after motion used its allowance = %d", kind, code)
		}
	}

	var d detectorState
	t0 := time.Now()
	for range detectorOpens {
		if !d.allowOpen("cam1|human", t0) {
			t.Fatal("refused within the burst")
		}
	}
	if d.allowOpen("cam1|human", t0.Add(detectorOpenEvery/2)) {
		t.Fatal("allowed before a refill")
	}
	if !d.allowOpen("cam1|human", t0.Add(detectorOpenEvery)) || d.allowOpen("cam1|human", t0.Add(detectorOpenEvery)) {
		t.Fatal("one refill should allow exactly one more")
	}
	// A long calm refills to the burst, not past it.
	n := 0
	for d.allowOpen("cam1|human", t0.Add(24*time.Hour)) {
		n++
	}
	if n != detectorOpens {
		t.Fatalf("after a day of calm: %d allowed, want %d", n, detectorOpens)
	}
}

// Snapshots keep to their budget: the oldest go first, and nothing is
// stored when no room can be made.
func TestSnapshotBudget(t *testing.T) {
	_, srv := newTestServer(t)
	srv.snaps.limit = 1000
	pic := func(n int) []byte { return append([]byte{0xFF, 0xD8}, make([]byte, n-2)...) }
	dir := filepath.Join(srv.Cfg.DataDir, "snapshots", "cam1")
	for id := int64(1); id <= 3; id++ {
		if err := srv.saveSnapshot("cam1", id, pic(300)); err != nil {
			t.Fatal(err)
		}
	}
	// Events 1 and 2 are from yesterday.
	old := time.Now().Add(-24 * time.Hour)
	for _, id := range []string{"1", "2"} {
		if err := os.Chtimes(filepath.Join(dir, id+".jpg"), old, old); err != nil {
			t.Fatal(err)
		}
	}
	// Full, and pruned less than a minute ago: refused.
	if err := srv.saveSnapshot("cam1", 4, pic(300)); err == nil {
		t.Fatal("stored past the budget")
	}
	// Room is made by deleting only the oldest picture: the two left (600
	// bytes) leave room for event 4.
	srv.snaps.prunedAt = time.Time{}
	if err := srv.saveSnapshot("cam1", 4, pic(300)); err != nil {
		t.Fatalf("after making room: %v", err)
	}
	for id, want := range map[string]bool{"1": false, "2": true, "3": true, "4": true} {
		if _, err := os.Stat(filepath.Join(dir, id+".jpg")); (err == nil) != want {
			t.Errorf("snapshot %s kept = %v, want %v", id, err == nil, want)
		}
	}
	// The hourly purge recounts.
	srv.PurgeSnapshots(48 * time.Hour)
	if srv.snaps.bytes != 900 {
		t.Fatalf("counted %d bytes, want 900", srv.snaps.bytes)
	}
	// One picture larger than the whole budget is never stored, and makes
	// no room either.
	srv.snaps.prunedAt = time.Time{}
	if err := srv.saveSnapshot("cam1", 5, pic(1500)); err == nil {
		t.Fatal("stored a picture larger than the budget")
	}
	if files, _ := os.ReadDir(dir); len(files) != 3 {
		t.Fatalf("%d snapshots left, want 3", len(files))
	}
}

// Pruning removes whole ten-minute buckets while that keeps enough, then
// the oldest files of the bucket where it stops: when the newest bucket
// alone is over the target, its newest pictures are kept.
func TestSnapshotPruneKeepsNewest(t *testing.T) {
	_, srv := newTestServer(t)
	dir := filepath.Join(srv.Cfg.DataDir, "snapshots", "cam1")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	base := time.Unix((time.Now().Unix()/snapshotBucket-3)*snapshotBucket, 0) // a bucket start
	write := func(id int, at time.Time) {
		p := filepath.Join(dir, strconv.Itoa(id)+".jpg")
		if err := os.WriteFile(p, make([]byte, 300), 0o640); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
	}
	kept := func() []string {
		var out []string
		files, _ := os.ReadDir(dir)
		for _, f := range files {
			out = append(out, strings.TrimSuffix(f.Name(), ".jpg"))
		}
		slices.Sort(out)
		return out
	}

	// All five in one bucket, 1500 bytes for a budget of 1000 with 300 to
	// add: the three oldest go, the two newest stay.
	for i := 1; i <= 5; i++ {
		write(i, base.Add(time.Duration(i)*time.Second))
	}
	if total := srv.pruneSnapshots(time.Time{}, 1000, 300); total != 600 {
		t.Fatalf("kept %d bytes, want 600", total)
	}
	if got := kept(); !slices.Equal(got, []string{"4", "5"}) {
		t.Fatalf("kept %v, want [4 5]", got)
	}

	// An older bucket goes whole, then the oldest of the next.
	for _, f := range kept() {
		_ = os.Remove(filepath.Join(dir, f+".jpg"))
	}
	write(1, base.Add(-9*time.Minute))
	write(2, base.Add(-8*time.Minute))
	for i := 3; i <= 6; i++ {
		write(i, base.Add(time.Duration(i)*time.Second))
	}
	if total := srv.pruneSnapshots(time.Time{}, 1000, 0); total != 900 {
		t.Fatalf("kept %d bytes, want 900", total)
	}
	if got := kept(); !slices.Equal(got, []string{"4", "5", "6"}) {
		t.Fatalf("kept %v, want [4 5 6]", got)
	}
}

// Transitions the watcher did not store (event id 0) only update the
// motion state the detector waits for.
func TestUnstoredEventOnlyWakesTheDetector(t *testing.T) {
	ts, srv := newTestServer(t)
	c := srv.hub.subscribe()
	defer srv.hub.unsubscribe(c)
	srv.HandleEvent("cam1", "motion", 0, true, time.Now())
	if _, body := detectorReq(t, srv, ts.URL, "GET", "/internal/detector/motion", srv.det.token, ""); !strings.Contains(body, `"cam1":true`) {
		t.Fatalf("motion %s", body)
	}
	select {
	case m := <-c:
		t.Fatalf("published %+v", m)
	case <-time.After(100 * time.Millisecond):
	}
}

// The detector's boxes are relayed as one SSE data line whatever the
// whitespace of its JSON.
func TestDetectorObjectsRelayIsOneLine(t *testing.T) {
	ts, srv := newTestServer(t)
	c := srv.hub.subscribe()
	defer srv.hub.unsubscribe(c)
	msg := "{\"camera\":\"cam1\",\r\n\r\n\"objects\" :\n[ ]\n}"
	if code, _ := detectorReq(t, srv, ts.URL, "POST", "/internal/detector/objects", srv.det.token, msg); code != 200 {
		t.Fatalf("objects %d", code)
	}
	select {
	case m := <-c:
		if string(m.data) != `{"camera":"cam1","objects":[]}` {
			t.Fatalf("relayed %q", m.data)
		}
	case <-time.After(time.Second):
		t.Fatal("not relayed")
	}
}
