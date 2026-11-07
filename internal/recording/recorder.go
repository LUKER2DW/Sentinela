// Package recording supervises one copy-only ffmpeg recorder per camera,
// indexes finished segments into SQLite and enforces retention (ADR-0007).
package recording

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/LUKER2DW/sentinela/internal/auth/redact"
	"github.com/LUKER2DW/sentinela/internal/config"
	"github.com/LUKER2DW/sentinela/internal/store"
)

// SegmentPattern is the strftime pattern (UTC) under recordings/<camera>/.
const SegmentPattern = "%Y/%m/%d/%H-%M-%S.mp4"

// Args builds the ffmpeg command line for a camera. Video is always copied;
// audio is AAC-encoded when enabled (ADR-0006). The URL holds credentials.
func Args(cam *config.Camera, rec config.Recording, camDir string) []string {
	a := []string{
		"-nostdin", "-hide_banner", "-loglevel", "warning",
		"-rtsp_transport", "tcp",
		"-timeout", "10000000", // RTSP socket I/O timeout, µs
		"-fflags", "+discardcorrupt",
		"-i", cam.RTSPURL(0),
		"-map", "0:v:0",
		"-c:v", "copy",
	}
	if cam.AudioRecording() {
		// aresample=async smooths the camera's repeated/backward PCMA
		// timestamps; audio is re-encoded anyway (ADR-0006).
		a = append(a, "-map", "0:a:0?", "-c:a", "aac", "-b:a", "32k", "-af", "aresample=async=1")
	} else {
		a = append(a, "-an")
	}
	a = append(a,
		"-f", "segment",
		"-segment_time", fmt.Sprint(rec.SegmentSeconds),
		"-segment_atclocktime", "1",
		"-segment_format", "mp4",
		"-segment_format_options", "movflags=+frag_keyframe+empty_moov+default_base_moof",
		"-reset_timestamps", "1",
		"-strftime", "1",
		filepath.ToSlash(filepath.Join(camDir, SegmentPattern)),
	)
	return a
}

// Recorder supervises the ffmpeg process of one camera.
type Recorder struct {
	Cam    *config.Camera
	Rec    config.Recording
	Dir    string // recordings/<camera>
	Store  *store.Store
	Log    *slog.Logger
	FFmpeg string
	// Marker, when set, must exist before anything is written: it lives
	// only on the recording disk (config.Recording.VolumeMarker).
	Marker  string
	now     func() time.Time
	mu      sync.Mutex
	status  store.RecorderStatus
	lastErr string
	// restartsAt holds reconnect times of the last hour (reconnect storms).
	restartsAt []time.Time
}

// stormRestarts reconnects in one hour are logged as a reconnect storm.
const stormRestarts = 5

// NewRecorder returns a recorder for cam writing under root/<cam.ID>.
func NewRecorder(cam *config.Camera, rec config.Recording, root string, st *store.Store, log *slog.Logger) *Recorder {
	return &Recorder{Cam: cam, Rec: rec, Dir: filepath.Join(root, cam.ID), Store: st, Log: log.With("camera", cam.ID),
		FFmpeg: "ffmpeg", now: time.Now}
}

func (r *Recorder) setState(state string) {
	r.mu.Lock()
	if r.status.State != state {
		r.status.State = state
		r.status.Since = r.now()
	}
	r.mu.Unlock()
	r.heartbeat()
}

// SetStore attaches the database once it could be opened (recording starts
// without it, ADR-0007), so heartbeats reach the System page.
func (r *Recorder) SetStore(st *store.Store) {
	r.mu.Lock()
	r.Store = st
	r.mu.Unlock()
}

func (r *Recorder) heartbeat() {
	r.mu.Lock()
	st := r.status
	st.CameraID = r.Cam.ID
	st.Heartbeat = r.now()
	st.LastError = r.lastErr
	st.Restarts1h = r.recentRestartsLocked()
	db := r.Store
	r.mu.Unlock()
	// Best effort: recording must not depend on the database (ADR-0007).
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if db != nil {
		if err := db.PutRecorderStatus(ctx, st); err != nil {
			r.Log.Warn("recorder heartbeat not stored", "err", err)
		}
	}
}

// ensureDirs creates the day directories for now and the next hour (UTC),
// because the segment muxer does not create directories itself.
func (r *Recorder) ensureDirs() error {
	for _, t := range []time.Time{r.now().UTC(), r.now().UTC().Add(time.Hour)} {
		if err := os.MkdirAll(filepath.Join(r.Dir, t.Format("2006/01/02")), 0o750); err != nil {
			return err
		}
	}
	return nil
}

// ErrNoVolume: the recording disk's marker file is missing.
var ErrNoVolume = errors.New("recording disk not mounted (volume marker missing); not writing to the system disk")

// volumeReady checks the marker of the recording disk (fail closed).
func (r *Recorder) volumeReady() error {
	if r.Marker == "" {
		return nil
	}
	if _, err := os.Stat(r.Marker); err != nil {
		return fmt.Errorf("%w: %s", ErrNoVolume, r.Marker)
	}
	return nil
}

// Run keeps ffmpeg running until ctx is cancelled.
func (r *Recorder) Run(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		if err := r.volumeReady(); err != nil {
			r.fail(err)
			backoff = 30 * time.Second
		} else if err := r.ensureDirs(); err != nil {
			r.fail(fmt.Errorf("create dirs: %w", err))
		} else {
			started := r.now()
			err := r.runOnce(ctx)
			if ctx.Err() != nil {
				break
			}
			if r.now().Sub(started) > time.Minute {
				backoff = time.Second
			}
			r.fail(err)
			r.mu.Lock()
			r.status.Restarts++
			r.restartsAt = append(r.restartsAt, r.now())
			recent := r.recentRestartsLocked()
			r.mu.Unlock()
			if recent == stormRestarts {
				r.Log.Warn("recorder reconnect storm", "restarts_1h", recent)
			}
		}
		r.setState("backoff")
		select {
		case <-ctx.Done():
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
	r.setState("stopped")
}

// recentRestartsLocked drops reconnects older than an hour and counts the rest.
func (r *Recorder) recentRestartsLocked() int {
	cut := r.now().Add(-time.Hour)
	i := 0
	for i < len(r.restartsAt) && r.restartsAt[i].Before(cut) {
		i++
	}
	r.restartsAt = r.restartsAt[i:]
	return len(r.restartsAt)
}

func (r *Recorder) fail(err error) {
	if err == nil {
		err = errors.New("ffmpeg exited")
	}
	msg := redact.Secrets(err.Error(), r.Cam.Password)
	r.mu.Lock()
	r.lastErr = msg
	r.mu.Unlock()
	r.Log.Warn("recorder stopped", "err", msg)
}

// runOnce runs one ffmpeg process with a stall watchdog.
func (r *Recorder) runOnce(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(runCtx, r.FFmpeg, Args(r.Cam, r.Rec, r.Dir)...)
	cmd.Env = append(os.Environ(), "TZ=UTC")
	// Ask ffmpeg to finish the segment cleanly; kill after 5 s.
	cmd.Cancel = func() error { return interrupt(cmd.Process) }
	cmd.WaitDelay = 5 * time.Second
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	r.setState("starting")
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start ffmpeg: %w", err)
	}
	tail := newTail(8)
	go r.readStderr(stderr, tail)

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	stall := r.Rec.Stall()
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	lastGrowth := r.now()
	lastFile, lastSize := newestFile(r.Dir) // may be a previous run's file
	growing := false
	for {
		select {
		case err := <-done:
			if msg := tail.String(); msg != "" {
				return fmt.Errorf("%v: %s", err, msg)
			}
			return err
		case <-tick.C:
			_ = r.ensureDirs()
			file, size := newestFile(r.Dir)
			if file != lastFile || size != lastSize {
				lastFile, lastSize, lastGrowth = file, size, r.now()
				if !growing {
					growing = true
					r.setState("recording")
				}
				r.mu.Lock()
				r.status.CurrentFile = relPath(r.Dir, file)
				r.mu.Unlock()
			}
			limit := stall
			if !growing {
				limit += 15 * time.Second // connect + first keyframe grace
			}
			if r.now().Sub(lastGrowth) > limit {
				r.Log.Warn("recorder stalled; restarting", "stall", stall)
				cancel()
				<-done
				return fmt.Errorf("stalled: no data for %s", stall)
			}
			r.heartbeat()
		}
	}
}

func relPath(base, p string) string {
	if p == "" {
		return ""
	}
	rel, err := filepath.Rel(base, p)
	if err != nil {
		return filepath.Base(p)
	}
	return filepath.ToSlash(rel)
}

func (r *Recorder) readStderr(rd io.Reader, t *tail) {
	sc := bufio.NewScanner(rd)
	var lastLog time.Time
	for sc.Scan() {
		line := redact.Secrets(sc.Text(), r.Cam.Password)
		// PCMA from this camera repeats DTS now and then; harmless noise.
		if strings.Contains(line, "non monotonically increasing dts") || strings.Contains(line, "Non-monotonic DTS") {
			continue
		}
		t.add(line)
		if time.Since(lastLog) > 10*time.Second {
			r.Log.Info("ffmpeg", "line", line)
			lastLog = time.Now()
		}
	}
}

// newestFile returns the live segment (latest timestamped name) and its size.
// Names sort chronologically, and os.Stat is used for the size because on
// some filesystems (NTFS) directory listings lag behind a file being written.
func newestFile(dir string) (string, int64) {
	now := time.Now().UTC()
	for _, d := range []time.Time{now.Add(time.Hour), now, now.Add(-24 * time.Hour)} {
		p := filepath.Join(dir, d.Format("2006/01/02"))
		entries, err := os.ReadDir(p)
		if err != nil {
			continue
		}
		best := ""
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".mp4") && e.Name() > best {
				best = e.Name()
			}
		}
		if best == "" {
			continue
		}
		full := filepath.Join(p, best)
		if info, err := os.Stat(full); err == nil {
			return full, info.Size()
		}
	}
	return "", 0
}

type tail struct {
	mu    sync.Mutex
	lines []string
	max   int
}

func newTail(n int) *tail { return &tail{max: n} }

func (t *tail) add(s string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lines = append(t.lines, s)
	if len(t.lines) > t.max {
		t.lines = t.lines[1:]
	}
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.Join(t.lines, " | ")
}
