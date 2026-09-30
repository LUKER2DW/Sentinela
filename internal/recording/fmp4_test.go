package recording

import (
	"bytes"
	"encoding/binary"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func box(typ string, parts ...[]byte) []byte {
	body := bytes.Join(parts, nil)
	b := binary.BigEndian.AppendUint32(nil, uint32(8+len(body)))
	return append(append(b, typ...), body...)
}

func u32(v uint32) []byte { return binary.BigEndian.AppendUint32(nil, v) }

// trak with a version-0 tkhd/mdhd: track id and timescale at offset 12.
func trak(id, timescale uint32, handler string) []byte {
	tkhd := append(make([]byte, 12), u32(id)...)
	tkhd = append(tkhd, make([]byte, 8)...)
	mdhd := append(make([]byte, 12), u32(timescale)...)
	mdhd = append(mdhd, make([]byte, 8)...)
	hdlr := append(make([]byte, 8), handler...)
	hdlr = append(hdlr, make([]byte, 12)...)
	return box("trak", box("tkhd", tkhd), box("mdia", box("mdhd", mdhd), box("hdlr", hdlr)))
}

func traf(id uint32, t uint32) []byte {
	return box("traf", box("tfhd", make([]byte, 4), u32(id)), box("tfdt", make([]byte, 4), u32(t)))
}

// testSegment: audio track 1 (48 kHz), video track 2 (90 kHz), one
// fragment every 2 s, mfra at the end like a closed ffmpeg segment.
func testSegment(frags int) []byte {
	out := box("ftyp", []byte("isom"))
	out = append(out, box("moov", trak(1, 48000, "soun"), trak(2, 90000, "vide"))...)
	for i := range frags {
		out = append(out, box("moof", box("mfhd", make([]byte, 8)), traf(1, uint32(i)*96000), traf(2, uint32(i)*180000))...)
		out = append(out, box("mdat", bytes.Repeat([]byte{byte(i)}, 100))...)
	}
	return append(out, box("mfra", make([]byte, 16))...)
}

func TestFragIndexSplice(t *testing.T) {
	seg := testSegment(10)
	idx, err := ReadFragIndex(bytes.NewReader(seg), int64(len(seg)))
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Fragments) != 10 || idx.Fragments[3].Start != 6*time.Second {
		t.Fatalf("fragments = %+v", idx.Fragments)
	}
	tests := []struct {
		at        time.Duration
		wantStart time.Duration
		whole     bool
	}{
		{0, 0, true},
		{1500 * time.Millisecond, 0, true}, // inside the first fragment
		{7 * time.Second, 6 * time.Second, false},
		{8 * time.Second, 8 * time.Second, false},
		{time.Hour, 18 * time.Second, false},
	}
	for _, tt := range tests {
		rs, size, start := idx.Splice(bytes.NewReader(seg), tt.at)
		if tt.whole {
			if rs != nil {
				t.Errorf("at %v: spliced, want the whole file", tt.at)
			}
			continue
		}
		if start != tt.wantStart {
			t.Errorf("at %v: start %v, want %v", tt.at, start, tt.wantStart)
		}
		got, _ := io.ReadAll(rs)
		f := idx.Fragments[int(tt.wantStart/(2*time.Second))]
		want := append(append([]byte{}, seg[:idx.InitEnd]...), seg[f.Offset:idx.End]...)
		if int64(len(got)) != size || !bytes.Equal(got, want) {
			t.Errorf("at %v: %d bytes, want init + fragments from %d (%d bytes)", tt.at, len(got), f.Offset, len(want))
		}
	}
}

func TestFragIndexGrowingFile(t *testing.T) {
	seg := testSegment(4)
	cut := len(seg) - 16 - 8 - 50 // mid-way through the last mdat, no mfra
	idx, err := ReadFragIndex(bytes.NewReader(seg[:cut]), int64(cut))
	if err != nil {
		t.Fatal(err)
	}
	rs, size, start := idx.Splice(bytes.NewReader(seg[:cut]), time.Hour)
	if rs == nil || start != 4*time.Second {
		t.Fatalf("start %v: the incomplete last fragment must be skipped", start)
	}
	if size != idx.InitEnd+idx.End-idx.Fragments[2].Offset {
		t.Fatalf("size %d", size)
	}
}

func TestSpliceServesRanges(t *testing.T) {
	seg := testSegment(10)
	idx, _ := ReadFragIndex(bytes.NewReader(seg), int64(len(seg)))
	rs, size, _ := idx.Splice(bytes.NewReader(seg), 10*time.Second)
	whole, _ := io.ReadAll(rs)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Range", "bytes=20-119")
	http.ServeContent(rec, req, "", time.Time{}, rs)
	if rec.Code != http.StatusPartialContent || !bytes.Equal(rec.Body.Bytes(), whole[20:120]) || int64(len(whole)) != size {
		t.Fatalf("range: %d, %d bytes", rec.Code, rec.Body.Len())
	}
}

func TestFragIndexGOP(t *testing.T) {
	seg := testSegment(10)
	idx, _ := ReadFragIndex(bytes.NewReader(seg), int64(len(seg)))
	rs, start := idx.GOP(bytes.NewReader(seg), 7*time.Second)
	if rs == nil || start != 6*time.Second {
		t.Fatalf("gop at 7 s: %v %v", rs != nil, start)
	}
	b, _ := io.ReadAll(rs)
	frag := idx.Fragments[4].Offset - idx.Fragments[3].Offset
	if int64(len(b)) != idx.InitEnd+frag {
		t.Fatalf("gop size %d, want init %d + one fragment %d", len(b), idx.InitEnd, frag)
	}
	if !bytes.Equal(b[:idx.InitEnd], seg[:idx.InitEnd]) || !bytes.Equal(b[idx.InitEnd:], seg[idx.Fragments[3].Offset:idx.Fragments[4].Offset]) {
		t.Fatal("gop bytes differ from the segment")
	}
	if rs, start := idx.GOP(bytes.NewReader(seg), 0); rs == nil || start != 0 {
		t.Fatal("first fragment must work too")
	}
}
