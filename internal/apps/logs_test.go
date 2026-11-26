package apps

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"strings"
	"testing"
)

// frame is one multiplexed Docker log frame.
func frame(stream byte, s string) []byte {
	b := make([]byte, 8, 8+len(s))
	b[0] = stream
	binary.BigEndian.PutUint32(b[4:], uint32(len(s)))
	return append(b, s...)
}

type logLine struct {
	stream int
	text   string
}

func TestDemuxBoundsLines(t *testing.T) {
	long := strings.Repeat("x", 16<<10)
	for _, c := range []struct {
		name    string
		in      [][]byte
		maxLine int
		want    []logLine
		err     bool
	}{
		{"lines", [][]byte{frame(1, "a\nb\r\n"), frame(2, "c\n")}, 0, []logLine{{1, "a"}, {1, "b"}, {2, "c"}}, false},
		{"line across frames", [][]byte{frame(1, "ab"), frame(1, "cd\n")}, 0, []logLine{{1, "abcd"}}, false},
		{"streams kept apart", [][]byte{frame(1, "out"), frame(2, "err\n"), frame(1, "put\n")}, 0, []logLine{{2, "err"}, {1, "output"}}, false},
		{"unterminated last line", [][]byte{frame(1, "tail")}, 0, []logLine{{1, "tail"}}, false},
		{"long line cut", [][]byte{frame(1, long+"\nnext\n")}, 10, []logLine{{1, "xxxxxxxxxx…"}, {1, "next"}}, false},
		{"long line across frames cut once", [][]byte{frame(1, long), frame(1, long), frame(1, "end\nok\n")}, 10, []logLine{{1, "xxxxxxxxxx…"}, {1, "ok"}}, false},
		{"cut between characters", [][]byte{frame(1, "aaaaaaaaaé\n")}, 10, []logLine{{1, "aaaaaaaaa…"}}, false},
		{"default cut", [][]byte{frame(1, long+"\n")}, 0, []logLine{{1, long[:maxLogLine] + "…"}}, false},
		{"not multiplexed", [][]byte{[]byte("hello world, this is a tty\n")}, 0, nil, true},
		{"frame too large", [][]byte{{1, 0, 0, 0, 0x7f, 0, 0, 0}}, 0, nil, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			var got []logLine
			err := demux(bytes.NewReader(bytes.Join(c.in, nil)), c.maxLine, func(s int, l string) { got = append(got, logLine{s, l}) })
			if (err != nil) != c.err {
				t.Fatalf("err %v", err)
			}
			if c.err {
				return
			}
			if fmt.Sprint(got) != fmt.Sprint(c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

// endless streams frames of output without a newline.
type endless struct {
	frames, sent int
	buf          []byte
}

func (e *endless) Read(p []byte) (int, error) {
	if len(e.buf) == 0 {
		if e.sent == e.frames {
			return 0, io.EOF
		}
		e.sent++
		e.buf = frame(1, strings.Repeat("y", 16<<10))
	}
	n := copy(p, e.buf)
	e.buf = e.buf[n:]
	return n, nil
}

// 64 MiB printed without a newline (more than the agent's memory limit)
// come out as one cut line.
func TestDemuxUnterminatedOutputStaysSmall(t *testing.T) {
	var lines []string
	if err := demux(&endless{frames: 4096}, 0, func(_ int, l string) { lines = append(lines, l) }); err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || len(lines[0]) > maxLogLine+len("…") {
		t.Fatalf("%d lines, first %d bytes", len(lines), len(lines[0]))
	}
}

func TestLogTailBounds(t *testing.T) {
	for _, c := range []struct {
		name      string
		n, max    int
		lines     []string
		wantFirst string
		wantLen   int
	}{
		{"count", 3, 1 << 20, []string{"a", "b", "c", "d", "e"}, "c", 3},
		{"bytes", 100, 10, []string{"aaaa", "bbbb", "cccc", "dddd"}, "cccc", 2},
		{"one line over the size", 100, 3, []string{"aaaa"}, "", 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			l := &logTail{n: c.n, max: c.max}
			for _, s := range c.lines {
				l.add(LogLine{Text: s})
			}
			l.trim()
			if len(l.lines) != c.wantLen || c.wantLen > 0 && l.lines[0].Text != c.wantFirst {
				t.Fatalf("%+v", l.lines)
			}
			if l.size > c.max {
				t.Fatalf("size %d", l.size)
			}
		})
	}
}

// The logs answer of a service is bounded in size, whatever it printed.
func TestServiceLogsAnswerIsBounded(t *testing.T) {
	a, eng, _ := newTestAgent(t)
	v, _ := a.Create(nodeApp())
	a.Enqueue(v.ID, "deploy")
	runQueued(t, a)
	eng.output = strings.Repeat("z", maxLogBytes+1)
	lines, err := a.Logs(context.Background(), v.ID, 300)
	if err != nil {
		t.Fatal(err)
	}
	size := 0
	for _, l := range lines {
		size += len(l.Text)
	}
	if size > maxLogBytes || lines == nil {
		t.Fatalf("%d bytes in %d lines", size, len(lines))
	}
}

// The Compose helper's output is not held beyond maxComposeOutput.
func TestComposeHelperOutputIsBounded(t *testing.T) {
	a, eng, src := newTestAgent(t)
	big := strings.Repeat(`{"services":{"web":{"image":"nginx"}}}`+"\n", maxComposeOutput/38+10)
	got := deployCompose(t, a, eng, src, App{Name: "Site"}, func(string) string { return big })
	if got.Status.Job.Phase != "failed" || !strings.Contains(got.Status.Job.Error, "printed more than") {
		t.Fatalf("deploy with a huge rendered project: %+v", got.Status.Job)
	}
}
