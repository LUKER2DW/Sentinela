package recording

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// segment makes a fragmented MP4 like the recorder's (keyframe every 1 s).
func segment(t *testing.T, ff, path string, secs int, trailer bool) {
	t.Helper()
	args := []string{"-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc=size=160x90:rate=10",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=8000", "-t", strconv.Itoa(secs),
		"-c:v", "libx264", "-g", "10", "-pix_fmt", "yuv420p", "-c:a", "aac",
		"-movflags", "frag_keyframe+empty_moov+default_base_moof", "-y", path}
	if out, err := exec.Command(ff, args...).CombinedOutput(); err != nil {
		t.Skipf("ffmpeg cannot make a test segment: %v %s", err, out)
	}
	if !trailer { // a segment still being written: no mfra yet
		b, _ := os.ReadFile(path)
		if i := bytes.LastIndex(b, []byte("mfra")); i > 4 {
			_ = os.WriteFile(path, b[:i-4], 0o600)
		}
	}
}

func TestWriteKeyframes(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	probe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not installed")
	}
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.mp4"), filepath.Join(dir, "b.mp4")
	segment(t, ff, a, 8, true)
	segment(t, ff, b, 6, false)
	// a has an mfra (closed segment), b does not (being written).
	for _, c := range []struct {
		path string
		mfra bool
	}{{a, true}, {b, false}} {
		f, _ := os.Open(c.path)
		st, _ := f.Stat()
		var tail [16]byte
		_, _ = f.ReadAt(tail[:], st.Size()-16)
		s, err := openSeg(c.path)
		f.Close()
		if err != nil || (string(tail[4:8]) == "mfro") != c.mfra || len(s.syncs) < 5 {
			t.Fatalf("%s: err=%v mfro=%q syncs=%d", c.path, err, tail[4:8], len(s.syncs))
		}
		s.f.Close()
	}
	picks := []Pick{{a, 0}, {a, 2500 * time.Millisecond}, {a, 7 * time.Second}, {b, time.Second}, {b, 5 * time.Second}}

	var buf bytes.Buffer
	var done int
	if err := WriteKeyframes(&buf, picks, 25, func(n int) { done = n }); err != nil {
		t.Fatal(err)
	}
	if done != len(picks) {
		t.Fatalf("progress %d, want %d", done, len(picks))
	}
	// ffmpeg re-wraps it without decoding; every frame is a keyframe.
	out := filepath.Join(dir, "tl.mp4")
	cmd := exec.Command(ff, "-hide_banner", "-loglevel", "error", "-f", "mp4", "-i", "pipe:0", "-map", "0:v:0", "-c", "copy", "-movflags", "+faststart", "-y", out)
	cmd.Stdin = &buf
	if o, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg remux: %v %s", err, o)
	}
	o, err := exec.Command(probe, "-v", "error", "-select_streams", "v:0", "-count_frames", "-show_entries",
		"stream=nb_read_frames:frame=key_frame", "-of", "csv=p=0", out).CombinedOutput()
	if err != nil {
		t.Fatalf("ffprobe: %v %s", err, o)
	}
	lines := strings.Fields(strings.TrimSpace(string(o)))
	keys := 0
	for _, l := range lines {
		if strings.HasPrefix(l, "1") {
			keys++
		}
	}
	if keys < len(picks) || !strings.Contains(string(o), strconv.Itoa(len(picks))) {
		t.Fatalf("timelapse frames:\n%s", o)
	}
}

func TestParseTfraPicksSyncBefore(t *testing.T) {
	s := &segFile{syncs: []syncSample{{0, 100}, {time.Second, 200}, {2 * time.Second, 300}}}
	for _, c := range []struct {
		at   time.Duration
		moof int64
	}{{0, 100}, {1500 * time.Millisecond, 200}, {9 * time.Second, 300}, {-time.Second, 100}} {
		if got := s.at(c.at).moof; got != c.moof {
			t.Errorf("at(%v) = %d, want %d", c.at, got, c.moof)
		}
	}
}
