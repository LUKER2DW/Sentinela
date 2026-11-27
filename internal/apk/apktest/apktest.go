// Package apktest builds small APK-shaped files for tests: a ZIP with an
// APK Signing Block whose v2/v3 blocks name the given certificates (the
// signatures are dummies; package apk does not verify them).
package apktest

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"testing"
)

// Block IDs of the signature schemes.
const (
	V2  = 0x7109871a
	V3  = 0xf05368c0
	V31 = 0x1b93ad61
)

func u32(n int) []byte { return binary.LittleEndian.AppendUint32(nil, uint32(n)) }

// LP length-prefixes the concatenation of parts.
func LP(parts ...[]byte) []byte {
	b := bytes.Join(parts, nil)
	return append(u32(len(b)), b...)
}

// Signer is one signer of a v2 (or, with v3, a v3) block.
func Signer(v3 bool, certs ...[]byte) []byte {
	var cs [][]byte
	for _, c := range certs {
		cs = append(cs, LP(c))
	}
	signed := [][]byte{LP(LP(u32(0x0103), LP([]byte("digest")))), LP(cs...)}
	if v3 {
		signed = append(signed, u32(24), u32(0x7fffffff))
	}
	signed = append(signed, LP())
	parts := [][]byte{LP(signed...)}
	if v3 {
		parts = append(parts, u32(24), u32(0x7fffffff))
	}
	parts = append(parts, LP(LP(u32(0x0103), LP([]byte("sig")))), LP([]byte("public key")))
	return LP(parts...)
}

// Scheme is a v2/v3 block value holding signers.
func Scheme(signers ...[]byte) []byte { return LP(signers...) }

// WithBlock is a small ZIP with an APK Signing Block holding the given
// ID-value pairs (in order) before its central directory.
func WithBlock(t testing.TB, pairs map[uint32][]byte, order ...uint32) []byte {
	t.Helper()
	var z bytes.Buffer
	w := zip.NewWriter(&z)
	f, err := w.Create("AndroidManifest.xml")
	if err == nil {
		_, err = f.Write([]byte("manifest"))
	}
	if err == nil {
		err = w.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	b := z.Bytes()
	eocd := len(b) - 22
	cdOff := int(binary.LittleEndian.Uint32(b[eocd+16:]))
	var entries []byte
	for _, id := range order {
		v := pairs[id]
		entries = binary.LittleEndian.AppendUint64(entries, uint64(len(v)+4))
		entries = binary.LittleEndian.AppendUint32(entries, id)
		entries = append(entries, v...)
	}
	size := uint64(len(entries) + 24)
	block := binary.LittleEndian.AppendUint64(nil, size)
	block = append(block, entries...)
	block = binary.LittleEndian.AppendUint64(block, size)
	block = append(block, "APK Sig Block 42"...)
	out := append(append(append([]byte{}, b[:cdOff]...), block...), b[cdOff:]...)
	binary.LittleEndian.PutUint32(out[len(out)-22+16:], uint32(cdOff+len(block)))
	return out
}

// Signed is an APK whose v2 and v3 blocks name cert.
func Signed(t testing.TB, cert []byte) []byte {
	return WithBlock(t, map[uint32][]byte{V2: Scheme(Signer(false, cert)), V3: Scheme(Signer(true, cert))}, V2, V3)
}
