package recording

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"time"
)

// Keyframe extraction for timelapses (AGENTS.md §2.1: bytes are copied,
// nothing is decoded). A closed segment ends with an mfra box whose tfra
// lists every video sync sample and its moof. For each sampled instant the
// writer reads that moof and the keyframe's bytes only (~220 KB), instead of
// letting ffmpeg open and walk the segment (HDD seeks dominated: ~55 ms per
// frame). The frames come out as a fragmented MP4 stream with evenly spaced
// timestamps, which ffmpeg only re-wraps (-c copy).

// Pick is one timelapse frame: the keyframe at or before At in a segment.
type Pick struct {
	Path string
	At   time.Duration
}

// ErrNoIndex means a segment cannot be read this way (no mfra, e.g. the
// one still being written, or different codec settings): use the fallback.
var ErrNoIndex = errors.New("fmp4: no keyframe index")

type syncSample struct {
	time time.Duration
	moof int64
}

type segFile struct {
	f       *os.File
	init    []byte // ftyp+moov
	stsd    []byte // video sample description: must match across segments
	videoID uint32
	scale   uint32
	syncs   []syncSample
}

func openSeg(path string) (*segFile, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	s, err := readSeg(f)
	if err != nil {
		f.Close()
		return nil, err
	}
	s.f = f
	return s, nil
}

// readSeg parses the init boxes at the start and the mfra at the end.
func readSeg(f *os.File) (*segFile, error) {
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := st.Size()
	s := &segFile{}
	var off int64
	for off+8 <= size {
		typ, hdr, n, err := boxHeader(f, off)
		if err != nil || n < hdr {
			return nil, ErrNoIndex
		}
		if typ == "moov" {
			if n > maxBox {
				return nil, ErrNoIndex
			}
			s.init = make([]byte, off+n)
			if _, err := f.ReadAt(s.init, 0); err != nil {
				return nil, err
			}
			s.videoID, s.scale = videoTrack(s.init[off+hdr:])
			s.stsd = videoStsd(s.init[off+hdr:])
			break
		}
		off += n
	}
	if s.init == nil || s.videoID == 0 || s.scale == 0 {
		return nil, ErrNoIndex
	}
	// mfro (last 16 bytes) holds the size of the mfra box. A segment still
	// being written has none: scan its fragments (recent, so in the page cache).
	var tail [16]byte
	if size < 16 {
		return nil, ErrNoIndex
	}
	if _, err := f.ReadAt(tail[:], size-16); err != nil || string(tail[4:8]) != "mfro" {
		idx, err := ReadFragIndex(f, size)
		if err != nil || len(idx.Fragments) == 0 {
			return nil, ErrNoIndex
		}
		for _, fr := range idx.Fragments {
			if fr.Offset < idx.End {
				s.syncs = append(s.syncs, syncSample{time: fr.Start, moof: fr.Offset})
			}
		}
		if len(s.syncs) == 0 {
			return nil, ErrNoIndex
		}
		return s, nil
	}
	mfraSize := int64(binary.BigEndian.Uint32(tail[12:]))
	if mfraSize < 16 || mfraSize > maxBox || mfraSize > size {
		return nil, ErrNoIndex
	}
	mfra := make([]byte, mfraSize)
	if _, err := f.ReadAt(mfra, size-mfraSize); err != nil || string(mfra[4:8]) != "mfra" {
		return nil, ErrNoIndex
	}
	children(mfra[8:], func(typ string, body []byte) bool {
		if typ == "tfra" {
			if id, syncs := parseTfra(body, s.scale); id == s.videoID {
				s.syncs = syncs
				return false
			}
		}
		return true
	})
	if len(s.syncs) == 0 {
		return nil, ErrNoIndex
	}
	return s, nil
}

// parseTfra reads a tfra body (ISO 14496-12 §8.8.10).
func parseTfra(b []byte, scale uint32) (uint32, []syncSample) {
	if len(b) < 16 {
		return 0, nil
	}
	v := b[0]
	id := binary.BigEndian.Uint32(b[4:])
	sizes := binary.BigEndian.Uint32(b[8:])
	skip := int((sizes>>4)&3+1) + int((sizes>>2)&3+1) + int(sizes&3+1)
	n := int(binary.BigEndian.Uint32(b[12:]))
	b = b[16:]
	step := 8 + skip
	if v == 1 {
		step = 16 + skip
	}
	if n < 0 || n > len(b)/step {
		return id, nil
	}
	out := make([]syncSample, 0, n)
	for i := 0; i < n; i++ {
		e := b[i*step:]
		var t, moof uint64
		if v == 1 {
			t, moof = binary.BigEndian.Uint64(e), binary.BigEndian.Uint64(e[8:])
		} else {
			t, moof = uint64(binary.BigEndian.Uint32(e)), uint64(binary.BigEndian.Uint32(e[4:]))
		}
		out = append(out, syncSample{time: ticks(t, scale), moof: int64(moof)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].time < out[j].time })
	return id, out
}

// keyframe returns the bytes of the first video sample of the moof at off
// (a fragment starts at a keyframe: frag_keyframe).
func (s *segFile) keyframe(off int64) ([]byte, error) {
	typ, hdr, n, err := boxHeader(s.f, off)
	if err != nil || typ != "moof" || n > maxBox {
		return nil, ErrNoIndex
	}
	moof, err := readBody(s.f, off+hdr, n-hdr)
	if err != nil {
		return nil, err
	}
	var pos, size int64 = -1, -1
	children(moof, func(t string, traf []byte) bool {
		if t != "traf" {
			return true
		}
		tfhd, trun := child(traf, "tfhd"), child(traf, "trun")
		if len(tfhd) < 8 || binary.BigEndian.Uint32(tfhd[4:]) != s.videoID || len(trun) < 8 {
			return true
		}
		flags := binary.BigEndian.Uint32(tfhd) & 0xffffff
		base := off // default-base-is-moof (and ffmpeg's default)
		p := 8
		var defSize int64 = -1
		if flags&0x01 != 0 && len(tfhd) >= p+8 {
			base = int64(binary.BigEndian.Uint64(tfhd[p:]))
			p += 8
		}
		for _, f := range []uint32{0x02, 0x08} {
			if flags&f != 0 {
				p += 4
			}
		}
		if flags&0x10 != 0 && len(tfhd) >= p+4 {
			defSize = int64(binary.BigEndian.Uint32(tfhd[p:]))
		}
		tf := binary.BigEndian.Uint32(trun) & 0xffffff
		q := 8
		var dataOff int64
		if tf&0x01 != 0 && len(trun) >= q+4 {
			dataOff = int64(int32(binary.BigEndian.Uint32(trun[q:])))
			q += 4
		}
		if tf&0x04 != 0 {
			q += 4
		}
		if tf&0x100 != 0 {
			q += 4
		}
		size = defSize
		if tf&0x200 != 0 && len(trun) >= q+4 {
			size = int64(binary.BigEndian.Uint32(trun[q:]))
		}
		pos = base + dataOff
		return false
	})
	if pos < 0 || size <= 0 || size > 16<<20 {
		return nil, ErrNoIndex
	}
	b := make([]byte, size)
	if _, err := s.f.ReadAt(b, pos); err != nil {
		return nil, fmt.Errorf("fmp4: keyframe: %w", err)
	}
	return b, nil
}

// videoStsd returns the video trak's stsd body (codec and parameter sets).
func videoStsd(moov []byte) []byte {
	var out []byte
	children(moov, func(t string, trak []byte) bool {
		if t != "trak" {
			return true
		}
		mdia := child(trak, "mdia")
		if hdlr := child(mdia, "hdlr"); len(hdlr) < 12 || string(hdlr[8:12]) != "vide" {
			return true
		}
		out = child(child(child(mdia, "minf"), "stbl"), "stsd")
		return false
	})
	return out
}

// at is the sync sample at or before t (the first one when t is earlier).
func (s *segFile) at(t time.Duration) syncSample {
	i := sort.Search(len(s.syncs), func(i int) bool { return s.syncs[i].time > t }) - 1
	if i < 0 {
		i = 0
	}
	return s.syncs[i]
}

// WriteKeyframes writes the picked keyframes to w as one fragmented MP4 at
// fps: the first segment's init boxes, then one single-sample fragment per
// pick. It fails with ErrNoIndex (before writing anything past the init, if
// possible) when a segment has no index or other codec settings. progress
// is called after each frame.
func WriteKeyframes(w io.Writer, picks []Pick, fps int, progress func(done int)) error {
	if len(picks) == 0 || fps <= 0 {
		return ErrNoIndex
	}
	var (
		cur     *segFile
		curPath string
		init    []byte
		stsd    []byte
		scale   uint32
		videoID uint32
	)
	defer func() {
		if cur != nil {
			cur.f.Close()
		}
	}()
	for i, p := range picks {
		if p.Path != curPath {
			if cur != nil {
				cur.f.Close()
				cur = nil
			}
			s, err := openSeg(p.Path)
			if err != nil {
				return err
			}
			cur, curPath = s, p.Path
			if init == nil {
				init, stsd, scale, videoID = s.init, s.stsd, s.scale, s.videoID
				if _, err := w.Write(init); err != nil {
					return err
				}
			} else if !bytes.Equal(s.stsd, stsd) || s.scale != scale || s.videoID != videoID {
				return ErrNoIndex // resolution or codec changed during the range
			}
		}
		frame, err := cur.keyframe(cur.at(p.At).moof)
		if err != nil {
			return err
		}
		dur := scale / uint32(fps)
		if err := writeFragment(w, uint32(i+1), videoID, uint64(i)*uint64(dur), dur, frame); err != nil {
			return err
		}
		if progress != nil {
			progress(i + 1)
		}
	}
	return nil
}

// writeFragment writes moof(mfhd, traf(tfhd, tfdt, trun)) + mdat for one
// sync sample.
func writeFragment(w io.Writer, seq, trackID uint32, decodeTime uint64, dur uint32, sample []byte) error {
	be := binary.BigEndian
	box := func(typ string, body ...[]byte) []byte {
		n := 8
		for _, b := range body {
			n += len(b)
		}
		out := make([]byte, 8, n)
		be.PutUint32(out, uint32(n))
		copy(out[4:], typ)
		for _, b := range body {
			out = append(out, b...)
		}
		return out
	}
	u32 := func(v uint32) []byte { b := make([]byte, 4); be.PutUint32(b, v); return b }
	u64 := func(v uint64) []byte { b := make([]byte, 8); be.PutUint64(b, v); return b }
	mfhd := box("mfhd", u32(0), u32(seq))
	tfhd := box("tfhd", u32(0x020000), u32(trackID)) // default-base-is-moof
	tfdt := box("tfdt", u32(1<<24), u64(decodeTime))
	// trun: data-offset, sample duration, size and flags; one sample.
	trunLen := 8 + 4 + 4 + 4 + 4 + 4 + 4
	moofLen := 8 + len(mfhd) + 8 + len(tfhd) + len(tfdt) + trunLen
	trun := box("trun", u32(0x000701), u32(1), u32(uint32(moofLen+8)), u32(dur), u32(uint32(len(sample))), u32(0x02000000))
	traf := box("traf", tfhd, tfdt, trun)
	moof := box("moof", mfhd, traf)
	if len(moof) != moofLen {
		return fmt.Errorf("fmp4: moof size %d != %d", len(moof), moofLen)
	}
	hdr := make([]byte, 8)
	be.PutUint32(hdr, uint32(8+len(sample)))
	copy(hdr[4:], "mdat")
	for _, b := range [][]byte{moof, hdr, sample} {
		if _, err := w.Write(b); err != nil {
			return err
		}
	}
	return nil
}
