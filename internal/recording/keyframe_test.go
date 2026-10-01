package recording

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"
)

// realFragment is shaped like ffmpeg's frag_keyframe+default_base_moof
// output: tfhd with default-base-is-moof, trun with data offset, first
// sample flags and per-sample sizes; video samples first, then audio.
func realFragment(t uint32, video [][]byte, audio []byte) []byte {
	vtrun := func(off uint32) []byte {
		b := u32(0x000205) // data offset, first-sample flags, sample sizes
		b = append(b, u32(uint32(len(video)))...)
		b = append(b, u32(off)...)
		b = append(b, u32(0x02000000)...) // first sample: sync
		for _, s := range video {
			b = append(b, u32(uint32(len(s)))...)
		}
		return b
	}
	atrun := func(off uint32) []byte {
		return append(append(append(u32(0x000201), u32(1)...), u32(off)...), u32(uint32(len(audio)))...)
	}
	mk := func(voff, aoff uint32) []byte {
		v := box("traf", box("tfhd", u32(0x020000), u32(2)), box("tfdt", make([]byte, 4), u32(t)), box("trun", vtrun(voff)))
		a := box("traf", box("tfhd", u32(0x020000), u32(1)), box("tfdt", make([]byte, 4), u32(t)), box("trun", atrun(aoff)))
		return box("moof", box("mfhd", make([]byte, 8)), v, a)
	}
	moofLen := uint32(len(mk(0, 0)))
	vlen := 0
	for _, s := range video {
		vlen += len(s)
	}
	moof := mk(moofLen+8, moofLen+8+uint32(vlen))
	return append(moof, box("mdat", bytes.Join(video, nil), audio)...)
}

func TestKeyFrame(t *testing.T) {
	seg := box("ftyp", []byte("isom"))
	seg = append(seg, box("moov", box("mvhd", make([]byte, 20)), trak(1, 48000, "soun"), trak(2, 90000, "vide"),
		box("mvex", box("trex", u32(0), u32(1)), box("trex", u32(0), u32(2))))...)
	key := bytes.Repeat([]byte{0xAB}, 40)
	for i := range 4 {
		seg = append(seg, realFragment(uint32(i)*180000, [][]byte{key, bytes.Repeat([]byte{1}, 30), bytes.Repeat([]byte{2}, 30)}, bytes.Repeat([]byte{9}, 20))...)
	}
	idx, err := ReadFragIndex(bytes.NewReader(seg), int64(len(seg)))
	if err != nil || len(idx.Fragments) != 4 {
		t.Fatalf("index: %v %+v", err, idx)
	}
	out, start, err := idx.KeyFrame(bytes.NewReader(seg), 5*time.Second)
	if err != nil || start != 4*time.Second {
		t.Fatalf("keyframe: %v start %v", err, start)
	}

	// The init keeps only the video track.
	var gotMoov, gotMoof []byte
	var moofOff int
	for off := 0; off+8 <= len(out); {
		n := int(binary.BigEndian.Uint32(out[off:]))
		switch string(out[off+4 : off+8]) {
		case "moov":
			gotMoov = out[off+8 : off+n]
		case "moof":
			gotMoof, moofOff = out[off+8:off+n], off
		}
		off += n
	}
	traks := 0
	children(gotMoov, func(t string, b []byte) bool {
		if t == "trak" {
			traks++
		}
		return true
	})
	if traks != 1 || len(child(child(gotMoov, "mvex"), "trex")) == 0 {
		t.Fatalf("moov kept %d traks", traks)
	}
	// One traf, one sample, and its data offset lands on the keyframe bytes.
	trafs := 0
	children(gotMoof, func(t string, b []byte) bool {
		if t == "traf" {
			trafs++
		}
		return true
	})
	trun := child(child(gotMoof, "traf"), "trun")
	if trafs != 1 || binary.BigEndian.Uint32(trun[4:]) != 1 {
		t.Fatalf("trafs %d, samples %d", trafs, binary.BigEndian.Uint32(trun[4:]))
	}
	at := moofOff + int(binary.BigEndian.Uint32(trun[8:]))
	if !bytes.Equal(out[at:at+40], key) || len(out) != at+40 {
		t.Fatal("the sample is not the keyframe, or more than it was sent")
	}
}

// A trun shorter than its flags announce is refused, not a panic.
func TestKeyFrameShortTrun(t *testing.T) {
	seg := box("ftyp", []byte("isom"))
	seg = append(seg, box("moov", box("mvhd", make([]byte, 20)), trak(2, 90000, "vide"), box("mvex", box("trex", u32(0), u32(2))))...)
	// flags: data offset + first-sample flags + sizes, but only the count.
	moof := box("moof", box("mfhd", make([]byte, 8)),
		box("traf", box("tfhd", u32(0x020000), u32(2)), box("tfdt", make([]byte, 4), u32(0)), box("trun", u32(0x000205), u32(1))))
	seg = append(seg, moof...)
	seg = append(seg, box("mdat", make([]byte, 16))...)
	idx, err := ReadFragIndex(bytes.NewReader(seg), int64(len(seg)))
	if err != nil || len(idx.Fragments) != 1 {
		t.Fatalf("index: %v %+v", err, idx)
	}
	if _, _, err := idx.KeyFrame(bytes.NewReader(seg), 0); err == nil {
		t.Fatal("expected an error for a short trun")
	}
}
