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
	if len(idx.Fragments) != 10 || idx.Fragments[3].Start != 6*time.Second || idx.Timescale != 90000 {
		t.Fatalf("index = %+v", idx)
	}
	tests := []struct {
		at        time.Duration
		wantStart time.Duration
	}{
		{0, 0},
		{1500 * time.Millisecond, 0}, // inside the first fragment
		{7 * time.Second, 6 * time.Second},
		{8 * time.Second, 8 * time.Second},
		{time.Hour, 18 * time.Second},
	}
	for _, tt := range tests {
		rs, size, start := idx.Splice(bytes.NewReader(seg), tt.at)
		if start != tt.wantStart {
			t.Errorf("at %v: start %v, want %v", tt.at, start, tt.wantStart)
		}
		got, _ := io.ReadAll(rs)
		first := int(tt.wantStart / (2 * time.Second))
		f := idx.Fragments[first]
		if int64(len(got)) != size || !bytes.Equal(got[:idx.InitEnd], seg[:idx.InitEnd]) {
			t.Fatalf("at %v: %d bytes of %d, init differs", tt.at, len(got), size)
		}
		sidx := got[idx.InitEnd:]
		sidx = sidx[:binary.BigEndian.Uint32(sidx)]
		if rest := got[idx.InitEnd+int64(len(sidx)):]; !bytes.Equal(rest, seg[f.Offset:idx.End]) {
			t.Errorf("at %v: fragments after the sidx differ from the segment from %d", tt.at, f.Offset)
		}
		checkSidx(t, sidx, idx, idx.Fragments[first:])
	}
}

// checkSidx: one reference per fragment, sizes covering exactly the bytes
// that follow it, 2 s each at 90 kHz, starting at the first fragment's time.
func checkSidx(t *testing.T, b []byte, idx *FragIndex, frags []Fragment) {
	t.Helper()
	be := binary.BigEndian
	if string(b[4:8]) != "sidx" || b[8] != 1 || be.Uint32(b[12:]) != idx.VideoID || be.Uint32(b[16:]) != 90000 {
		t.Fatalf("sidx header % x", b[:20])
	}
	if be.Uint64(b[20:]) != frags[0].Ticks || be.Uint64(b[28:]) != 0 || int(be.Uint16(b[38:])) != len(frags) {
		t.Fatalf("sidx times/count % x", b[20:40])
	}
	var total int64
	for i := range frags {
		ref := b[40+12*i:]
		total += int64(be.Uint32(ref) & 0x7fffffff)
		if be.Uint32(ref[4:]) != 180000 || be.Uint32(ref[8:]) != 0x90000000 {
			t.Errorf("reference %d: duration %d, sap %x", i, be.Uint32(ref[4:]), be.Uint32(ref[8:]))
		}
	}
	if total != idx.End-frags[0].Offset {
		t.Errorf("sidx covers %d bytes, want %d", total, idx.End-frags[0].Offset)
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
	if want := idx.InitEnd + (8 + 32 + 12) + idx.End - idx.Fragments[2].Offset; size != want {
		t.Fatalf("size %d, want %d", size, want)
	}
	// Nothing complete yet: nothing to splice.
	if rs, _, _ := (&FragIndex{InitEnd: idx.InitEnd, End: 0, Fragments: idx.Fragments}).Splice(bytes.NewReader(seg), 0); rs != nil {
		t.Fatal("spliced a file without a complete fragment")
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
