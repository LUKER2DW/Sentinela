package api

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/LUKER2DW/sentinela/internal/config"
	"github.com/LUKER2DW/sentinela/internal/store"
)

// Timelapse limits. One keyframe of the 2K main stream is ~220 KB, so the
// frame cap bounds a timelapse to ~400 MB.
const (
	timelapseMaxSpan   = 7 * 24 * time.Hour
	timelapseMinEvery  = 10 * time.Second
	timelapseMaxFrames = 1800
	// Prepared files: kept a few hours, a handful at most (disk, not RAM).
	timelapseKeep     = 6 * time.Hour
	timelapseKeepN    = 6
	timelapseJobLimit = 15 * time.Minute
)

// timelapseList builds an ffmpeg concat list that samples one instant every
// `every` in [from, to). Each entry opens the segment holding that instant
// with a tiny inpoint/outpoint window, so ffmpeg only reads around one
// keyframe per sample instead of the whole recording. Instants in gaps are
// skipped. It returns the list and the number of samples.
func timelapseList(segs []store.Segment, from, to time.Time, every time.Duration, path func(string) string) (string, int) {
	var b strings.Builder
	n, i := 0, 0
	for t := from; t.Before(to) && n < timelapseMaxFrames; t = t.Add(every) {
		for i < len(segs) && !segs[i].End.After(t) {
			i++
		}
		if i == len(segs) {
			break
		}
		if segs[i].Start.After(t) {
			continue
		}
		p := path(segs[i].Path)
		if p == "" {
			continue
		}
		in := t.Sub(segs[i].Start).Seconds()
		fmt.Fprintf(&b, "file '%s'\ninpoint %.3f\noutpoint %.3f\n",
			strings.ReplaceAll(filepath.ToSlash(p), "'", `'\''`), in, in+0.05)
		n++
	}
	return b.String(), n
}

// tlParams is one timelapse request, validated.
type tlParams struct {
	cam      *config.Camera
	from, to time.Time
	every    time.Duration
	fps      int
}

// key names the prepared file: the same request reuses it.
func (p tlParams) key() string {
	h := sha256.Sum256(fmt.Appendf(nil, "%s|%d|%d|%d|%d", p.cam.ID, p.from.Unix(), p.to.Unix(), p.every/time.Second, p.fps))
	return hex.EncodeToString(h[:12])
}

func (s *Server) timelapseParams(w http.ResponseWriter, r *http.Request) (tlParams, bool) {
	cam := s.camera(w, r)
	if cam == nil {
		return tlParams{}, false
	}
	from, to, err := parseRange(r, timelapseMaxSpan)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return tlParams{}, false
	}
	q := r.URL.Query()
	everyS, _ := strconv.Atoi(q.Get("every"))
	every := time.Duration(everyS) * time.Second
	if every < timelapseMinEvery {
		writeErr(w, http.StatusBadRequest, "every must be at least 10 seconds")
		return tlParams{}, false
	}
	if to.Sub(from)/every > timelapseMaxFrames {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("too many frames (max %d): choose a longer interval", timelapseMaxFrames))
		return tlParams{}, false
	}
	fps, _ := strconv.Atoi(q.Get("fps"))
	if fps == 0 {
		fps = 25
	}
	if fps < 5 || fps > 30 {
		writeErr(w, http.StatusBadRequest, "fps must be between 5 and 30")
		return tlParams{}, false
	}
	return tlParams{cam: cam, from: from, to: to, every: every, fps: fps}, true
}

// timelapseArgs writes the concat list next to the recordings (/tmp is RAM)
// and returns the ffmpeg input/codec arguments, the sample count and a
// cleanup. No decoding (AGENTS.md §2.1): the noise bitstream filter drops
// every non-keyframe packet and setts rewrites the timestamps.
func (s *Server) timelapseArgs(ctx context.Context, p tlParams) ([]string, int, func(), error) {
	segs, err := s.Store.SegmentsBetween(ctx, p.cam.ID, p.from, p.to)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("database: %w", err)
	}
	list, n := timelapseList(segs, p.from, p.to, p.every, s.segmentPath)
	if n == 0 {
		return nil, 0, nil, nil
	}
	cache := filepath.Join(s.Cfg.DataDir, "cache")
	_ = os.MkdirAll(cache, 0o750)
	tmp, err := os.CreateTemp(cache, "timelapse-*.txt")
	if err != nil {
		return nil, 0, nil, err
	}
	_, werr := tmp.WriteString(list)
	if cerr := tmp.Close(); werr != nil || cerr != nil {
		_ = os.Remove(tmp.Name())
		return nil, 0, nil, fmt.Errorf("concat list: %v %v", werr, cerr)
	}
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error",
		"-f", "concat", "-safe", "0", "-i", tmp.Name(),
		"-map", "0:v:0", "-c", "copy",
		"-bsf:v", fmt.Sprintf("noise=drop=not(key),setts=ts=N/(%d*TB)", p.fps)}
	return args, n, func() { _ = os.Remove(tmp.Name()) }, nil
}

func (s *Server) timelapseName(p tlParams) string {
	return fmt.Sprintf("%s_timelapse_%s.mp4", p.cam.ID, p.from.Local().Format("20060102-1504"))
}

// timelapse serves a timelapse: the prepared file when there is one (byte
// ranges, so the player seeks and buffers normally), otherwise a
// fragmented MP4 streamed while ffmpeg reads the keyframes.
func (s *Server) timelapse(w http.ResponseWriter, r *http.Request) {
	p, ok := s.timelapseParams(w, r)
	if !ok {
		return
	}
	download := r.URL.Query().Get("download") == "1"
	if path := s.tl.ready(s.timelapseDir(), p.key()); path != "" {
		f, err := os.Open(path)
		if err == nil {
			defer f.Close()
			if st, err := f.Stat(); err == nil {
				w.Header().Set("Content-Type", "video/mp4")
				w.Header().Set("Cache-Control", "private, max-age=3600")
				if download {
					w.Header().Set("Content-Disposition", `attachment; filename="`+s.timelapseName(p)+`"`)
				}
				http.ServeContent(w, r, "", st.ModTime(), f)
				return
			}
		}
	}
	select {
	case s.tlSem <- struct{}{}:
		defer func() { <-s.tlSem }()
	default:
		writeErr(w, http.StatusTooManyRequests, "another timelapse is being generated, try again shortly")
		return
	}
	args, n, cleanup, err := s.timelapseArgs(r.Context(), p)
	if err != nil {
		s.Log.Warn("timelapse", "err", err)
		writeErr(w, http.StatusInternalServerError, "timelapse failed")
		return
	}
	if n == 0 {
		writeErr(w, http.StatusNotFound, "no recording in this range")
		return
	}
	defer cleanup()
	args = append(args, "-movflags", "frag_keyframe+empty_moov+default_base_moof", "-f", "mp4", "pipe:1")
	cmd := exec.CommandContext(r.Context(), s.FFmpeg, args...)
	cmd.Stdout = w
	var stderr strings.Builder
	cmd.Stderr = &stderr
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "no-store")
	if download {
		w.Header().Set("Content-Disposition", `attachment; filename="`+s.timelapseName(p)+`"`)
	}
	s.audit(r, "recording.timelapse", p.cam.ID, true, fmt.Sprintf("%s/%s every %s", p.from.Format(time.RFC3339), p.to.Sub(p.from), p.every))
	if err := cmd.Run(); err != nil && r.Context().Err() == nil {
		s.Log.Warn("timelapse failed", "err", err, "stderr", stderr.String())
	}
}

// ---------- prepared timelapses ----------

// tlJob is one timelapse being written to disk.
type tlJob struct {
	State string `json:"state"` // queued, running, ready, failed, empty
	Done  int    `json:"done"`
	Total int    `json:"total"`
	Bytes int64  `json:"bytes,omitempty"`
	path  string
}

type tlJobs struct {
	mu   sync.Mutex
	jobs map[string]*tlJob
}

func (s *Server) timelapseDir() string { return filepath.Join(s.Cfg.DataDir, "cache", "timelapse") }

// ready returns the prepared file for key, or "".
func (t *tlJobs) ready(dir, key string) string {
	path := filepath.Join(dir, key+".mp4")
	if _, err := os.Stat(path); err == nil {
		return path
	}
	return ""
}

func (t *tlJobs) update(key string, f func(j *tlJob)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if j, ok := t.jobs[key]; ok {
		f(j)
	}
}

// timelapsePrepare starts (or reports) writing a timelapse to disk, so the
// viewer plays a complete file instead of a stream that stalls while the
// keyframes are still being read. The client polls it until "ready".
func (s *Server) timelapsePrepare(w http.ResponseWriter, r *http.Request) {
	p, ok := s.timelapseParams(w, r)
	if !ok {
		return
	}
	key := p.key()
	dir := s.timelapseDir()
	if path := s.tl.ready(dir, key); path != "" {
		st, _ := os.Stat(path)
		writeJSON(w, http.StatusOK, tlJob{State: "ready", Done: 1, Total: 1, Bytes: st.Size()})
		return
	}
	s.tl.mu.Lock()
	if s.tl.jobs == nil {
		s.tl.jobs = map[string]*tlJob{}
	}
	j, running := s.tl.jobs[key]
	if !running || j.State == "failed" || j.State == "ready" {
		j = &tlJob{State: "queued", path: filepath.Join(dir, key+".mp4")}
		s.tl.jobs[key] = j
		running = false
	}
	out := *j
	s.tl.mu.Unlock()
	if !running {
		s.audit(r, "recording.timelapse", p.cam.ID, true, fmt.Sprintf("%s/%s every %s", p.from.Format(time.RFC3339), p.to.Sub(p.from), p.every))
		go s.runTimelapse(p, key, dir)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) runTimelapse(p tlParams, key, dir string) {
	ctx, cancel := context.WithTimeout(context.Background(), timelapseJobLimit)
	defer cancel()
	fail := func(state string, err error) {
		if err != nil {
			s.Log.Warn("timelapse job", "err", err)
		}
		s.tl.update(key, func(j *tlJob) { j.State = state })
	}
	// One at a time: it is disk-bound and competes with the recorder.
	select {
	case s.tlSem <- struct{}{}:
		defer func() { <-s.tlSem }()
	case <-ctx.Done():
		fail("failed", ctx.Err())
		return
	}
	args, n, cleanup, err := s.timelapseArgs(ctx, p)
	if err != nil {
		fail("failed", err)
		return
	}
	if n == 0 {
		fail("empty", nil)
		return
	}
	defer cleanup()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		fail("failed", err)
		return
	}
	pruneTimelapses(dir, time.Now())
	final := filepath.Join(dir, key+".mp4")
	part := final + ".part"
	s.tl.update(key, func(j *tlJob) { j.State, j.Total = "running", n })
	args = append(args, "-movflags", "+faststart", "-progress", "pipe:1", "-nostats", "-f", "mp4", "-y", part)
	cmd := exec.CommandContext(ctx, s.FFmpeg, args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		fail("failed", err)
		return
	}
	if err := cmd.Start(); err != nil {
		fail("failed", err)
		return
	}
	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "frame="); ok {
			if f, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
				s.tl.update(key, func(j *tlJob) { j.Done = min(f, n) })
			}
		}
	}
	if err := cmd.Wait(); err != nil {
		_ = os.Remove(part)
		fail("failed", fmt.Errorf("%w: %s", err, stderr.String()))
		return
	}
	if err := os.Rename(part, final); err != nil {
		fail("failed", err)
		return
	}
	st, _ := os.Stat(final)
	s.tl.update(key, func(j *tlJob) {
		j.State, j.Done = "ready", j.Total
		if st != nil {
			j.Bytes = st.Size()
		}
	})
}

// pruneTimelapses drops prepared files older than timelapseKeep and keeps
// at most timelapseKeepN of the newest, plus stale partial files.
func pruneTimelapses(dir string, now time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type file struct {
		path string
		mod  time.Time
	}
	var files []file
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if now.Sub(info.ModTime()) > timelapseKeep || (strings.HasSuffix(e.Name(), ".part") && now.Sub(info.ModTime()) > timelapseJobLimit) {
			_ = os.Remove(path)
			continue
		}
		if strings.HasSuffix(e.Name(), ".mp4") {
			files = append(files, file{path, info.ModTime()})
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	for i := timelapseKeepN - 1; i < len(files); i++ {
		_ = os.Remove(files[i].path)
	}
}
