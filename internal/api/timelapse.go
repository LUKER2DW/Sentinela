package api

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/LUKER2DW/sentinela/internal/store"
)

// Timelapse limits. One keyframe of the 2K main stream is ~220 KB, so the
// frame cap bounds a timelapse to ~400 MB.
const (
	timelapseMaxSpan   = 7 * 24 * time.Hour
	timelapseMinEvery  = 10 * time.Second
	timelapseMaxFrames = 1800
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

// timelapse streams a copy-only MP4 made of recorded keyframes: one sample
// every `every` seconds, played back at `fps`. No decoding (AGENTS.md §2.1):
// the noise bitstream filter drops every non-keyframe packet and setts
// rewrites the timestamps. One timelapse runs at a time (disk I/O).
func (s *Server) timelapse(w http.ResponseWriter, r *http.Request) {
	cam := s.camera(w, r)
	if cam == nil {
		return
	}
	from, to, err := parseRange(r, timelapseMaxSpan)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	q := r.URL.Query()
	everyS, _ := strconv.Atoi(q.Get("every"))
	every := time.Duration(everyS) * time.Second
	if every < timelapseMinEvery {
		writeErr(w, http.StatusBadRequest, "every must be at least 10 seconds")
		return
	}
	if to.Sub(from)/every > timelapseMaxFrames {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("too many frames (max %d): choose a longer interval", timelapseMaxFrames))
		return
	}
	fps, _ := strconv.Atoi(q.Get("fps"))
	if fps == 0 {
		fps = 25
	}
	if fps < 5 || fps > 30 {
		writeErr(w, http.StatusBadRequest, "fps must be between 5 and 30")
		return
	}
	select {
	case s.tlSem <- struct{}{}:
		defer func() { <-s.tlSem }()
	default:
		writeErr(w, http.StatusTooManyRequests, "another timelapse is being generated, try again shortly")
		return
	}
	segs, err := s.Store.SegmentsBetween(r.Context(), cam.ID, from, to)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "database error")
		return
	}
	list, n := timelapseList(segs, from, to, every, s.segmentPath)
	if n == 0 {
		writeErr(w, http.StatusNotFound, "no recording in this range")
		return
	}
	// The concat list lives next to the recordings: /tmp is RAM (tmpfs).
	cache := filepath.Join(s.Cfg.DataDir, "cache")
	_ = os.MkdirAll(cache, 0o750)
	tmp, err := os.CreateTemp(cache, "timelapse-*.txt")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "temp file failed")
		return
	}
	defer os.Remove(tmp.Name())
	_, werr := tmp.WriteString(list)
	if cerr := tmp.Close(); werr != nil || cerr != nil {
		writeErr(w, http.StatusInternalServerError, "temp file failed")
		return
	}
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error",
		"-f", "concat", "-safe", "0", "-i", tmp.Name(),
		"-map", "0:v:0", "-c", "copy",
		"-bsf:v", fmt.Sprintf("noise=drop=not(key),setts=ts=N/(%d*TB)", fps),
		"-movflags", "frag_keyframe+empty_moov+default_base_moof", "-f", "mp4", "pipe:1"}
	cmd := exec.CommandContext(r.Context(), s.FFmpeg, args...)
	cmd.Stdout = w
	var stderr strings.Builder
	cmd.Stderr = &stderr
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "no-store")
	if q.Get("download") == "1" {
		name := fmt.Sprintf("%s_timelapse_%s.mp4", cam.ID, from.Local().Format("20060102-1504"))
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	}
	s.audit(r, "recording.timelapse", cam.ID, true, fmt.Sprintf("%s/%s every %s", from.Format(time.RFC3339), to.Sub(from), every))
	if err := cmd.Run(); err != nil && r.Context().Err() == nil {
		s.Log.Warn("timelapse failed", "err", err, "stderr", stderr.String())
	}
}
