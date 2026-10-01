package recording

import (
	"encoding/binary"
	"errors"
	"io"
	"time"
)

// KeyFrame builds the smallest file a browser can show a picture from: the
// init boxes with only the video track, then one moof+mdat holding just the
// keyframe that starts the fragment at `at` (no audio, no other frames of
// the GOP). It is the timeline's hover preview: a fraction of the GOP's
// bytes, and the browser decodes a single frame. The samples are copied as
// they are, nothing is decoded or re-encoded here (AGENTS.md §2.1).
func (idx *FragIndex) KeyFrame(r io.ReaderAt, at time.Duration) ([]byte, time.Duration, error) {
	i := -1
	for j, f := range idx.Fragments {
		if f.Start <= at && f.Offset < idx.End {
			i = j
		}
	}
	if i < 0 || idx.VideoID == 0 {
		return nil, 0, errors.New("fmp4: no fragment")
	}
	f := idx.Fragments[i]

	// init: ftyp etc. as they are, moov without the non-video tracks.
	head, err := readBody(r, 0, idx.InitEnd)
	if err != nil {
		return nil, 0, err
	}
	var init []byte
	for off := 0; off+8 <= len(head); {
		n := int(binary.BigEndian.Uint32(head[off:]))
		if n < 8 || off+n > len(head) {
			return nil, 0, errors.New("fmp4: bad init box")
		}
		if string(head[off+4:off+8]) == "moov" {
			init = append(init, videoOnlyMoov(head[off+8:off+n], idx.VideoID)...)
		} else {
			init = append(init, head[off:off+n]...)
		}
		off += n
	}

	// the fragment's moof: keep mfhd and the video traf, trimmed to one sample.
	_, hdr, n, err := boxHeader(r, f.Offset)
	if err != nil || n <= hdr {
		return nil, 0, errors.New("fmp4: bad moof")
	}
	moof, err := readBody(r, f.Offset+hdr, n-hdr)
	if err != nil {
		return nil, 0, err
	}
	mfhd := child(moof, "mfhd")
	var traf []byte
	children(moof, func(t string, body []byte) bool {
		if t == "traf" {
			if tfhd := child(body, "tfhd"); len(tfhd) >= 8 && binary.BigEndian.Uint32(tfhd[4:]) == idx.VideoID {
				traf = body
				return false
			}
		}
		return true
	})
	if mfhd == nil || traf == nil {
		return nil, 0, errors.New("fmp4: no video traf")
	}
	tfhd, tfdt, trun := child(traf, "tfhd"), child(traf, "tfdt"), child(traf, "trun")
	if len(trun) < 8 {
		return nil, 0, errors.New("fmp4: no trun")
	}
	tfFlags := binary.BigEndian.Uint32(tfhd) & 0xffffff
	if tfFlags&0x1 != 0 || tfFlags&0x20000 == 0 {
		return nil, 0, errors.New("fmp4: data not relative to moof") // not how the recorder writes
	}
	// tfhd optional fields: base data offset (0x1, excluded), sample
	// description index (0x2), default duration (0x8), size (0x10), flags (0x20).
	defSize, p := uint32(0), 8
	for _, fl := range []uint32{0x2, 0x8, 0x10, 0x20} {
		if tfFlags&fl == 0 {
			continue
		}
		if p+4 > len(tfhd) {
			return nil, 0, errors.New("fmp4: short tfhd")
		}
		if fl == 0x10 {
			defSize = binary.BigEndian.Uint32(tfhd[p:])
		}
		p += 4
	}

	trFlags := binary.BigEndian.Uint32(trun) & 0xffffff
	count := binary.BigEndian.Uint32(trun[4:])
	q := 8
	var dataOff int32
	if trFlags&0x1 != 0 {
		dataOff = int32(binary.BigEndian.Uint32(trun[q:]))
		q += 4
	}
	var firstFlags []byte
	if trFlags&0x4 != 0 {
		firstFlags = trun[q : q+4]
		q += 4
	}
	entry := 0
	for _, fl := range []uint32{0x100, 0x200, 0x400, 0x800} {
		if trFlags&fl != 0 {
			entry += 4
		}
	}
	if count == 0 || q+entry > len(trun) || trFlags&0x1 == 0 {
		return nil, 0, errors.New("fmp4: unusable trun")
	}
	first := trun[q : q+entry]
	size := defSize
	if trFlags&0x200 != 0 {
		off := 0
		if trFlags&0x100 != 0 {
			off = 4
		}
		size = binary.BigEndian.Uint32(first[off:])
	}
	if size == 0 || size > 8<<20 {
		return nil, 0, errors.New("fmp4: bad sample size")
	}
	sample := make([]byte, size)
	if _, err := r.ReadAt(sample, f.Offset+int64(dataOff)); err != nil {
		return nil, 0, err
	}

	newTrun := func(dataOffset uint32) []byte {
		b := append([]byte{}, trun[0:4]...) // version and flags as they were
		b = binary.BigEndian.AppendUint32(b, 1)
		b = binary.BigEndian.AppendUint32(b, dataOffset)
		b = append(b, firstFlags...)
		return append(b, first...)
	}
	build := func(dataOffset uint32) []byte {
		t := mkbox("traf", mkbox("tfhd", tfhd), mkbox("tfdt", tfdt), mkbox("trun", newTrun(dataOffset)))
		return mkbox("moof", mkbox("mfhd", mfhd), t)
	}
	m := build(0)
	m = build(uint32(len(m) + 8)) // the sample starts right after the mdat header
	out := append(init, m...)
	out = append(out, mkbox("mdat", sample)...)
	return out, f.Start, nil
}

func mkbox(typ string, parts ...[]byte) []byte {
	n := 8
	for _, p := range parts {
		n += len(p)
	}
	b := binary.BigEndian.AppendUint32(make([]byte, 0, n), uint32(n))
	b = append(b, typ...)
	for _, p := range parts {
		b = append(b, p...)
	}
	return b
}

// videoOnlyMoov rebuilds a moov box keeping only the video trak (and its
// trex in mvex), so the browser does not wait for audio that is not there.
func videoOnlyMoov(moov []byte, videoID uint32) []byte {
	var body []byte
	children(moov, func(t string, b []byte) bool {
		switch t {
		case "trak":
			if tkhd := child(b, "tkhd"); trackID(tkhd) != videoID {
				return true
			}
		case "mvex":
			var keep [][]byte
			children(b, func(t2 string, b2 []byte) bool {
				if t2 != "trex" || len(b2) >= 8 && binary.BigEndian.Uint32(b2[4:]) == videoID {
					keep = append(keep, mkbox(t2, b2))
				}
				return true
			})
			body = append(body, mkbox("mvex", keep...)...)
			return true
		}
		body = append(body, mkbox(t, b)...)
		return true
	})
	return mkbox("moov", body)
}

func trackID(tkhd []byte) uint32 {
	if len(tkhd) >= 24 && tkhd[0] == 1 {
		return binary.BigEndian.Uint32(tkhd[20:])
	}
	if len(tkhd) >= 16 {
		return binary.BigEndian.Uint32(tkhd[12:])
	}
	return 0
}
