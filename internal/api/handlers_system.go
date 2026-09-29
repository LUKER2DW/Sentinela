package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/LUKER2DW/sentinela/internal/config"
	"github.com/LUKER2DW/sentinela/internal/store"
	"github.com/LUKER2DW/sentinela/internal/sysinfo"
)

func (s *Server) listEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	to := time.Now().UTC()
	from := to.Add(-7 * 24 * time.Hour)
	if v := q.Get("from"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			from = t
		}
	}
	if v := q.Get("to"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			to = t
		}
	}
	cam := q.Get("camera")
	if cam != "" && (s.Cfg.Camera(cam) == nil || !s.canSee(r, cam)) {
		writeErr(w, http.StatusNotFound, "camera not found")
		return
	}
	ev, err := s.Store.EventsBetween(r.Context(), cam, from, to, 500)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "database error")
		return
	}
	ev = s.visibleEvents(r, ev)
	for i := range ev {
		ev[i].Snapshot = s.hasSnapshot(ev[i])
	}
	writeJSON(w, http.StatusOK, ev)
}

// ---------- live event push (SSE) ----------

// LiveEvent is pushed to browsers when an event starts or ends.
type LiveEvent struct {
	CameraID string    `json:"camera_id"`
	Type     string    `json:"type"`
	Active   bool      `json:"active"`
	At       time.Time `json:"at"`
}

// Hub fans events out to SSE subscribers.
type Hub struct {
	mu   sync.Mutex
	subs map[chan LiveEvent]struct{}
}

// NewHub returns an empty hub.
func NewHub() *Hub { return &Hub{subs: map[chan LiveEvent]struct{}{}} }

// Publish sends to every subscriber without blocking.
func (h *Hub) Publish(e LiveEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.subs {
		select {
		case c <- e:
		default: // slow client: drop
		}
	}
}

func (h *Hub) subscribe() chan LiveEvent {
	c := make(chan LiveEvent, 16)
	h.mu.Lock()
	h.subs[c] = struct{}{}
	h.mu.Unlock()
	return c
}

func (h *Hub) unsubscribe(c chan LiveEvent) {
	h.mu.Lock()
	delete(h.subs, c)
	h.mu.Unlock()
}

func (s *Server) eventStream(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	c := s.hub.subscribe()
	defer s.hub.unsubscribe(c)
	fmt.Fprint(w, "retry: 5000\n\n")
	fl.Flush()
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		case e := <-c:
			if !s.canSee(r, e.CameraID) {
				continue
			}
			b, _ := json.Marshal(e)
			fmt.Fprintf(w, "event: camera\ndata: %s\n\n", b)
			fl.Flush()
		}
	}
}

// ---------- system ----------

type go2rtcStream struct {
	Online    bool `json:"online"`
	Consumers int  `json:"consumers"`
}

func (s *Server) go2rtcStatus(ctx context.Context) (map[string]go2rtcStream, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, s.go2rtc.String()+"/api/streams", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var raw map[string]struct {
		Producers []json.RawMessage `json:"producers"`
		Consumers []json.RawMessage `json:"consumers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	out := map[string]go2rtcStream{}
	for name, st := range raw {
		// go2rtc lists configured producers even when idle; a producer
		// with a non-empty body means an active source connection.
		online := false
		for _, p := range st.Producers {
			if len(p) > 2 && string(p) != "{}" && jsonHas(p, "medias") {
				online = true
			}
		}
		out[name] = go2rtcStream{Online: online, Consumers: len(st.Consumers)}
	}
	return out, nil
}

func jsonHas(raw json.RawMessage, key string) bool {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return false
	}
	_, ok := m[key]
	return ok
}

func (s *Server) system(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	root := filepath.Join(s.Cfg.DataDir, "recordings")
	disk, _ := sysinfo.DiskUsage(root)
	stats, err := s.Store.Stats(ctx)
	if err != nil {
		stats = []store.SegmentStats{}
	}
	recs, err := s.Store.ListRecorderStatus(ctx)
	if err != nil {
		recs = []store.RecorderStatus{}
	}
	// Bytes per day per camera, from the real rate of the last 24 h. Needs
	// at least 10 min of recording to be meaningful.
	daily := map[string]int64{}
	for _, c := range s.Cfg.Cameras {
		b, secs, err := s.Store.RateSince(ctx, c.ID, time.Now().Add(-24*time.Hour))
		if err == nil && secs >= 600 {
			daily[c.ID] = int64(float64(b) / secs * 86400)
		}
	}
	// Recording holes longer than 30 s in the last 24 h (gap alert).
	type gapsDTO struct {
		Count   int         `json:"count"`
		Longest float64     `json:"longest_secs"`
		Recent  []store.Gap `json:"recent"`
	}
	gaps := map[string]gapsDTO{}
	for _, c := range s.Cfg.Cameras {
		if !c.Recording() {
			continue
		}
		g, err := s.Store.GapsSince(ctx, c.ID, time.Now().Add(-24*time.Hour), gapAlert)
		if err != nil {
			continue
		}
		d := gapsDTO{Count: len(g), Recent: g[max(0, len(g)-5):]}
		for _, x := range g {
			d.Longest = max(d.Longest, x.To.Sub(x.From).Seconds())
		}
		gaps[c.ID] = d
	}
	type volumeDTO struct {
		config.Volume
		sysinfo.Disk
		Mounted bool `json:"mounted"`
	}
	volumes := []volumeDTO{}
	for _, v := range s.Cfg.Volumes {
		d, err := sysinfo.DiskUsage(v.Path)
		volumes = append(volumes, volumeDTO{Volume: v, Disk: d, Mounted: err == nil && d.Total > 0})
	}
	// Optional host snapshots (absent on dev machines): SMART and backups.
	health := hostJSON(s.Cfg.DiskHealthFile)
	backup := hostJSON(s.Cfg.BackupStatusFile)
	g2r, gerr := s.go2rtcStatus(ctx)
	g2rErr := ""
	if gerr != nil {
		g2rErr = "go2rtc unreachable"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version":      Version,
		"started":      s.Started.UTC(),
		"now":          time.Now().UTC(),
		"host":         sysinfo.Snapshot(),
		"disk":         disk,
		"max_disk_pct": s.Cfg.Recording.MaxDiskPercent,
		"segment_secs": s.Cfg.Recording.SegmentSeconds,
		"storage":      stats,
		"daily_bytes":  daily,
		"gaps_24h":     gaps,
		"recorders":    recs,
		"go2rtc":       g2r,
		"go2rtc_error": g2rErr,
		"volumes":      volumes,
		"nas_host":     s.Cfg.NASHost,
		"disk_health":  health,
		"backup":       backup,
	})
}

// gapAlert is the shortest recording hole reported on the System page.
const gapAlert = 30 * time.Second

// hostJSON reads a small JSON file written by a host timer; nil if absent or invalid.
func hostJSON(path string) json.RawMessage {
	b, err := os.ReadFile(path)
	if err != nil || len(b) >= 64<<10 || !json.Valid(b) {
		return nil
	}
	return b
}
