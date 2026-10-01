package recording

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"time"
)

// Segments are fragmented MP4 (frag_keyframe+empty_moov+default_base_moof):
// ftyp, moov, then one moof+mdat per video keyframe, and mfra once closed.
// A browser playing one from the middle has to walk the fragments one HTTP
// range at a time, which over a slow link takes many seconds. Instead the
// server hands it the init boxes followed by the fragments from the wanted
// keyframe on: the same bytes, no remux, no CPU (AGENTS.md §2.1).
//
// The served file also gets a segment index (sidx) after the moov. Without
// one, a seekable fragmented MP4 makes Chrome's demuxer (FFmpeg) read every
// moof before it plays, one range request per fragment: ~100 round trips per
// 5-minute file, which over the phone's remote path took ~10 s. The sidx
// lists every fragment's size and duration, so it plays after the first one.

// Fragment is one moof box and the video decode time it starts at.
type Fragment struct {
	Offset int64
	Start  time.Duration
	Ticks  uint64 // Start in the video timescale
}

// FragIndex describes a segment file for splicing.
type FragIndex struct {
	InitEnd   int64 // end of the boxes before the first moof (ftyp, moov)
	End       int64 // end of the last complete fragment (mfra excluded)
	Fragments []Fragment
	VideoID   uint32
	Timescale uint32 // of the video track
}

const maxBox = 1 << 20 // moov/moof are a few KB; refuse anything absurd

// ReadFragIndex scans the top-level boxes of an fMP4 file of the given size
// (a growing file is read up to its last complete box).
func ReadFragIndex(r io.ReaderAt, size int64) (*FragIndex, error) {
	var (
		idx       FragIndex
		videoID   uint32
		timescale uint32
		off       int64
	)
	for off+8 <= size {
		typ, hdr, n, err := boxHeader(r, off)
		if err != nil {
			return nil, err
		}
		if n < hdr || off+n > size {
			break // incomplete tail of a file still being written
		}
		switch typ {
		case "moov":
			body, err := readBody(r, off+hdr, n-hdr)
			if err != nil {
				return nil, err
			}
			videoID, timescale = videoTrack(body)
			if videoID == 0 || timescale == 0 {
				return nil, errors.New("fmp4: no video track")
			}
			idx.InitEnd = off + n
			idx.VideoID = videoID
			idx.Timescale = timescale
		case "moof":
			if idx.InitEnd == 0 {
				return nil, errors.New("fmp4: moof before moov")
			}
			body, err := readBody(r, off+hdr, n-hdr)
			if err != nil {
				return nil, err
			}
			if t, ok := fragmentTime(body, videoID); ok {
				idx.Fragments = append(idx.Fragments, Fragment{Offset: off, Start: ticks(t, timescale), Ticks: t})
			}
		case "mfra":
			return &idx, nil
		}
		off += n
		if typ == "mdat" {
			idx.End = off
		}
	}
	return &idx, nil
}

// Splice returns the file as seen from the last keyframe at or before at:
// the init boxes, a sidx indexing what follows, then every complete
// fragment from there on (a closed file's mfra is left out: its offsets no
// longer match). start is that keyframe's time in the segment. Nil when the
// file has no complete fragment yet.
func (idx *FragIndex) Splice(r io.ReaderAt, at time.Duration) (rs io.ReadSeeker, size int64, start time.Duration) {
	n, i := 0, 0
	for j, f := range idx.Fragments {
		if f.Offset >= idx.End {
			break
		}
		n = j + 1
		if f.Start <= at {
			i = j
		}
	}
	if n == 0 {
		return nil, 0, 0
	}
	frags := idx.Fragments[i:n]
	// The last fragment's length is not written anywhere cheap to read:
	// take the one before it (in the whole file).
	var last uint64
	if n >= 2 && idx.Fragments[n-1].Ticks > idx.Fragments[n-2].Ticks {
		last = idx.Fragments[n-1].Ticks - idx.Fragments[n-2].Ticks
	}
	parts := []section{{r, 0, idx.InitEnd}}
	if sidx := idx.sidx(frags, last); sidx != nil {
		parts = append(parts, section{bytes.NewReader(sidx), 0, int64(len(sidx))})
	}
	parts = append(parts, section{r, frags[0].Offset, idx.End - frags[0].Offset})
	m := &multiSection{parts: parts}
	for _, p := range parts {
		m.size += p.n
	}
	return m, m.size, frags[0].Start
}

// sidx builds a version-1 segment index box with one reference per
// fragment, the fragments following it back to back up to idx.End; last is
// the final fragment's duration.
func (idx *FragIndex) sidx(frags []Fragment, last uint64) []byte {
	if len(frags) > 0xffff || idx.Timescale == 0 {
		return nil
	}
	n := 8 + 4 + 4 + 4 + 8 + 8 + 2 + 2 + 12*len(frags)
	b := make([]byte, 0, n)
	b = binary.BigEndian.AppendUint32(b, uint32(n))
	b = append(b, "sidx"...)
	b = binary.BigEndian.AppendUint32(b, 1<<24) // version 1, no flags
	b = binary.BigEndian.AppendUint32(b, idx.VideoID)
	b = binary.BigEndian.AppendUint32(b, idx.Timescale)
	b = binary.BigEndian.AppendUint64(b, frags[0].Ticks) // earliest presentation time
	b = binary.BigEndian.AppendUint64(b, 0)              // first fragment right after
	b = binary.BigEndian.AppendUint16(b, 0)              // reserved
	b = binary.BigEndian.AppendUint16(b, uint16(len(frags)))
	for i, f := range frags {
		end, dur := idx.End, last
		if i+1 < len(frags) {
			end = frags[i+1].Offset
			if frags[i+1].Ticks > f.Ticks {
				dur = frags[i+1].Ticks - f.Ticks
			}
		}
		if end-f.Offset >= 1<<31 || dur >= 1<<32 {
			return nil
		}
		b = binary.BigEndian.AppendUint32(b, uint32(end-f.Offset)) // media reference
		b = binary.BigEndian.AppendUint32(b, uint32(dur))
		b = binary.BigEndian.AppendUint32(b, 0x90000000) // starts with a SAP of type 1
	}
	return b
}

// GOP returns a tiny playable file: the init boxes and only the fragment
// holding at (it starts on a keyframe). The timeline's hover preview shows
// its first frame; the bytes are copied, nothing is decoded here.
func (idx *FragIndex) GOP(r io.ReaderAt, at time.Duration) (rs io.ReadSeeker, start time.Duration) {
	i := -1
	for j, f := range idx.Fragments {
		if f.Start <= at && f.Offset < idx.End {
			i = j
		}
	}
	if i < 0 {
		return nil, 0
	}
	f := idx.Fragments[i]
	end := idx.End
	if i+1 < len(idx.Fragments) && idx.Fragments[i+1].Offset < end {
		end = idx.Fragments[i+1].Offset
	}
	m := &multiSection{parts: []section{{r, 0, idx.InitEnd}, {r, f.Offset, end - f.Offset}}}
	for _, p := range m.parts {
		m.size += p.n
	}
	return m, f.Start
}

func ticks(t uint64, timescale uint32) time.Duration {
	return time.Duration(float64(t) / float64(timescale) * float64(time.Second))
}

func boxHeader(r io.ReaderAt, off int64) (typ string, hdr, n int64, err error) {
	var b [16]byte
	if _, err = r.ReadAt(b[:8], off); err != nil {
		return "", 0, 0, fmt.Errorf("fmp4: box at %d: %w", off, err)
	}
	n, typ, hdr = int64(binary.BigEndian.Uint32(b[:4])), string(b[4:8]), 8
	if n == 1 {
		if _, err = r.ReadAt(b[8:16], off+8); err != nil {
			return "", 0, 0, fmt.Errorf("fmp4: box at %d: %w", off, err)
		}
		n, hdr = int64(binary.BigEndian.Uint64(b[8:16])), 16
	}
	if n == 0 {
		n = -1 // "to end of file": only ever the mdat being written
	}
	return typ, hdr, n, nil
}

func readBody(r io.ReaderAt, off, n int64) ([]byte, error) {
	if n > maxBox {
		return nil, fmt.Errorf("fmp4: %d-byte box", n)
	}
	b := make([]byte, n)
	if _, err := r.ReadAt(b, off); err != nil {
		return nil, fmt.Errorf("fmp4: %w", err)
	}
	return b, nil
}

// children calls fn for each box inside b, stopping when fn returns false.
func children(b []byte, fn func(typ string, body []byte) bool) {
	for len(b) >= 8 {
		n := int(binary.BigEndian.Uint32(b))
		if n < 8 || n > len(b) {
			return
		}
		if !fn(string(b[4:8]), b[8:n]) {
			return
		}
		b = b[n:]
	}
}

func child(b []byte, typ string) []byte {
	var out []byte
	children(b, func(t string, body []byte) bool {
		if t == typ {
			out = body
			return false
		}
		return true
	})
	return out
}

// videoTrack finds the video trak's ID and timescale in a moov body.
func videoTrack(moov []byte) (id, timescale uint32) {
	children(moov, func(t string, trak []byte) bool {
		if t != "trak" {
			return true
		}
		mdia := child(trak, "mdia")
		if hdlr := child(mdia, "hdlr"); len(hdlr) < 12 || string(hdlr[8:12]) != "vide" {
			return true
		}
		tkhd, mdhd := child(trak, "tkhd"), child(mdia, "mdhd")
		if len(tkhd) < 24 || len(mdhd) < 24 {
			return true
		}
		if tkhd[0] == 1 { // version 1: 64-bit times
			id = binary.BigEndian.Uint32(tkhd[20:])
		} else {
			id = binary.BigEndian.Uint32(tkhd[12:])
		}
		if mdhd[0] == 1 {
			timescale = binary.BigEndian.Uint32(mdhd[20:])
		} else {
			timescale = binary.BigEndian.Uint32(mdhd[12:])
		}
		return false
	})
	return id, timescale
}

// fragmentTime is the video traf's base media decode time in a moof body.
func fragmentTime(moof []byte, videoID uint32) (t uint64, ok bool) {
	children(moof, func(typ string, traf []byte) bool {
		if typ != "traf" {
			return true
		}
		tfhd, tfdt := child(traf, "tfhd"), child(traf, "tfdt")
		if len(tfhd) < 8 || binary.BigEndian.Uint32(tfhd[4:]) != videoID || len(tfdt) < 8 {
			return true
		}
		if tfdt[0] == 1 && len(tfdt) >= 12 {
			t = binary.BigEndian.Uint64(tfdt[4:])
		} else {
			t = uint64(binary.BigEndian.Uint32(tfdt[4:]))
		}
		ok = true
		return false
	})
	return t, ok
}

type section struct {
	r   io.ReaderAt
	off int64
	n   int64
}

// multiSection is a seekable view of byte ranges of a file laid end to end,
// so http.ServeContent can answer range requests on the spliced file.
type multiSection struct {
	parts []section
	size  int64
	pos   int64
}

func (m *multiSection) Read(p []byte) (int, error) {
	if m.pos >= m.size {
		return 0, io.EOF
	}
	base := int64(0)
	for _, s := range m.parts {
		if m.pos < base+s.n {
			rel := m.pos - base
			if max := s.n - rel; int64(len(p)) > max {
				p = p[:max]
			}
			n, err := s.r.ReadAt(p, s.off+rel)
			m.pos += int64(n)
			if err == io.EOF && n > 0 {
				err = nil
			}
			return n, err
		}
		base += s.n
	}
	return 0, io.EOF
}

func (m *multiSection) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		offset += m.pos
	case io.SeekEnd:
		offset += m.size
	default:
		return 0, errors.New("fmp4: bad whence")
	}
	if offset < 0 {
		return 0, errors.New("fmp4: negative position")
	}
	m.pos = offset
	return offset, nil
}
