package api

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httputil"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/LUKER2DW/sentinela/internal/store"
)

// streamTicket issues a single-use, 60 s ticket for one camera stream.
func (s *Server) streamTicket(w http.ResponseWriter, r *http.Request) {
	cam := s.camera(w, r)
	if cam == nil {
		return
	}
	stream := r.URL.Query().Get("stream")
	if stream != "sub" {
		stream = "main"
	}
	tok, exp := s.tickets.Issue(userFrom(r).ID, cam.ID, stream)
	writeJSON(w, http.StatusOK, map[string]any{
		"url":     "/stream/ws?ticket=" + tok,
		"expires": exp.UTC(),
	})
}

// streamWS validates a ticket and proxies the WebSocket to go2rtc (loopback).
// The browser never learns the go2rtc address or any camera credential.
func (s *Server) streamWS(w http.ResponseWriter, r *http.Request) {
	tk, ok := s.tickets.Redeem(r.URL.Query().Get("ticket"))
	if !ok || tk.UserID != userFrom(r).ID {
		writeErr(w, http.StatusForbidden, "invalid or expired stream ticket")
		return
	}
	src := tk.CameraID
	if tk.Stream == "sub" {
		src += "_sub"
	}
	target := *s.go2rtc
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = target.Scheme
			pr.Out.URL.Host = target.Host
			pr.Out.URL.Path = "/api/ws"
			pr.Out.URL.RawQuery = "src=" + src
			pr.Out.Host = target.Host
			// go2rtc sees a same-host, cookie-less local client.
			pr.Out.Header.Del("Origin")
			pr.Out.Header.Del("Cookie")
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			s.Log.Warn("go2rtc proxy error", "err", err)
			writeErr(w, http.StatusBadGateway, "live media service unavailable")
		},
	}
	proxy.ServeHTTP(w, r)
}

func parseRange(r *http.Request, maxSpan time.Duration) (time.Time, time.Time, error) {
	q := r.URL.Query()
	from, err1 := time.Parse(time.RFC3339, q.Get("from"))
	to, err2 := time.Parse(time.RFC3339, q.Get("to"))
	if err1 != nil || err2 != nil || !to.After(from) {
		return time.Time{}, time.Time{}, errors.New("from/to must be RFC 3339 with to > from")
	}
	if to.Sub(from) > maxSpan {
		return time.Time{}, time.Time{}, fmt.Errorf("range too long (max %s)", maxSpan)
	}
	return from.UTC(), to.UTC(), nil
}

func (s *Server) timeline(w http.ResponseWriter, r *http.Request) {
	cam := s.camera(w, r)
	if cam == nil {
		return
	}
	from, to, err := parseRange(r, 8*24*time.Hour)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	segs, err := s.Store.SegmentsBetween(r.Context(), cam.ID, from, to)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "database error")
		return
	}
	// Events overlapping the window: look back 10 min for long events.
	events, err := s.Store.EventsBetween(r.Context(), cam.ID, from.Add(-10*time.Minute), to, 5000)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "database error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"segments": segs, "events": events})
}

func (s *Server) segmentFile(w http.ResponseWriter, r *http.Request) (*store.Segment, string) {
	id, err := strconv.ParseInt(r.PathValue("seg"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad segment id")
		return nil, ""
	}
	seg, err := s.Store.SegmentByID(r.Context(), id)
	if err != nil || !s.canSee(r, seg.CameraID) {
		writeErr(w, http.StatusNotFound, "segment not found")
		return nil, ""
	}
	return seg, s.segmentPath(seg.Path)
}

func (s *Server) segmentPath(rel string) string {
	root := filepath.Join(s.Cfg.DataDir, "recordings")
	p := filepath.Join(root, filepath.FromSlash(rel))
	if !strings.HasPrefix(p, root+string(filepath.Separator)) {
		return "" // defence in depth against a tampered DB row
	}
	return p
}

func (s *Server) recording(w http.ResponseWriter, r *http.Request) {
	seg, path := s.segmentFile(w, r)
	if seg == nil {
		return
	}
	f, err := os.Open(path)
	if err != nil || path == "" {
		writeErr(w, http.StatusNotFound, "file missing")
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "stat failed")
		return
	}
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "private, max-age=60")
	// ?at=seconds: start at the keyframe before that instant, so the player
	// does not download what comes before it.
	// ?at=seconds&gop=1: the timeline's hover preview, just the keyframe at
	// that instant (video track only, built once and kept in memory), or
	// the whole keyframe group when the fragment cannot be trimmed.
	if at, err := strconv.ParseFloat(r.URL.Query().Get("at"), 64); err == nil && r.URL.Query().Get("gop") == "1" && at >= 0 && at < 86400 {
		if idx, err := s.segs.index(path, f, info.Size(), info.ModTime()); err == nil {
			d := time.Duration(at * float64(time.Second))
			w.Header().Set("Cache-Control", "private, max-age=3600")
			if b, err := s.segs.preview(path, f, idx, d); err == nil {
				http.ServeContent(w, r, filepath.Base(path), info.ModTime(), bytes.NewReader(b))
				return
			}
			if rs, _ := idx.GOP(f, d); rs != nil {
				http.ServeContent(w, r, filepath.Base(path), info.ModTime(), rs)
				return
			}
		}
		// Never the whole segment for a preview.
		w.Header().Set("Cache-Control", "no-store")
		writeErr(w, http.StatusNotFound, "no preview for this segment")
		return
	}
	// Playback, from ?at or the start: served with a fragment index so the
	// player does not read the whole file before the first frame.
	if at, err := strconv.ParseFloat(r.URL.Query().Get("at"), 64); err != nil || at >= 0 && at < 86400 {
		if idx, err := s.segs.index(path, f, info.Size(), info.ModTime()); err == nil {
			if rs, _, _ := idx.Splice(f, time.Duration(max(at, 0)*float64(time.Second))); rs != nil {
				http.ServeContent(w, r, filepath.Base(path), info.ModTime(), rs)
				return
			}
		}
	}
	http.ServeContent(w, r, filepath.Base(path), info.ModTime(), f)
}

func (s *Server) protectSegment(w http.ResponseWriter, r *http.Request) {
	seg, _ := s.segmentFile(w, r)
	if seg == nil {
		return
	}
	var body struct {
		Protected bool `json:"protected"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.Store.SetSegmentProtected(r.Context(), seg.ID, body.Protected); err != nil {
		writeErr(w, http.StatusInternalServerError, "database error")
		return
	}
	s.audit(r, "segment.protect", strconv.FormatInt(seg.ID, 10), true, strconv.FormatBool(body.Protected))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// export streams a copy-only MP4 of [from, to) assembled from segments.
func (s *Server) export(w http.ResponseWriter, r *http.Request) {
	cam := s.camera(w, r)
	if cam == nil {
		return
	}
	from, to, err := parseRange(r, 30*time.Minute)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	segs, err := s.Store.SegmentsBetween(r.Context(), cam.ID, from, to)
	if err != nil || len(segs) == 0 {
		writeErr(w, http.StatusNotFound, "no recording in this range")
		return
	}
	select {
	case s.exportSem <- struct{}{}:
		defer func() { <-s.exportSem }()
	default:
		writeErr(w, http.StatusTooManyRequests, "other exports are running, try again shortly")
		return
	}
	var list strings.Builder
	for _, seg := range segs {
		p := s.segmentPath(seg.Path)
		if p == "" {
			continue
		}
		fmt.Fprintf(&list, "file '%s'\n", strings.ReplaceAll(filepath.ToSlash(p), "'", `'\''`))
	}
	offset := from.Sub(segs[0].Start)
	if offset < 0 {
		offset = 0
	}
	tmp, err := os.CreateTemp("", "sentinela-concat-*.txt")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "temp file failed")
		return
	}
	defer os.Remove(tmp.Name())
	_, werr := tmp.WriteString(list.String())
	if cerr := tmp.Close(); werr != nil || cerr != nil {
		writeErr(w, http.StatusInternalServerError, "temp file failed")
		return
	}
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error",
		"-f", "concat", "-safe", "0",
		"-ss", fmt.Sprintf("%.3f", offset.Seconds()), "-i", tmp.Name(),
		"-t", fmt.Sprintf("%.3f", to.Sub(from).Seconds()),
		"-map", "0", "-c", "copy", "-movflags", "frag_keyframe+empty_moov+default_base_moof", "-f", "mp4", "pipe:1"}
	cmd := exec.CommandContext(r.Context(), s.FFmpeg, args...)
	cmd.Stdout = w
	var stderr strings.Builder
	cmd.Stderr = &stderr
	name := fmt.Sprintf("%s_%s.mp4", cam.ID, from.Local().Format("20060102-150405"))
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	s.audit(r, "recording.export", cam.ID, true, from.Format(time.RFC3339)+"/"+to.Sub(from).String())
	if err := cmd.Run(); err != nil && r.Context().Err() == nil {
		s.Log.Warn("export failed", "err", err, "stderr", stderr.String())
	}
}
