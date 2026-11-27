package apk

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"testing"

	"github.com/LUKER2DW/sentinela/internal/apk/apktest"
)

func sum(c string) string {
	s := sha256.Sum256([]byte(c))
	return hex.EncodeToString(s[:])
}

func TestSigners(t *testing.T) {
	key, other := []byte("release cert"), []byte("other cert")
	const pad = 0x42726577 // a padding block, as apksigner writes
	cases := []struct {
		name  string
		apk   func(t *testing.T) []byte
		want  []string
		error bool
	}{
		{"v2 and v3, one key", func(t *testing.T) []byte {
			return apktest.WithBlock(t, map[uint32][]byte{
				apktest.V2: apktest.Scheme(apktest.Signer(false, key, []byte("ca"))),
				apktest.V3: apktest.Scheme(apktest.Signer(true, key)),
				pad:        []byte("padding"),
			}, pad, apktest.V2, apktest.V3)
		}, []string{sum("release cert")}, false},
		{"v3.1 names another key", func(t *testing.T) []byte {
			return apktest.WithBlock(t, map[uint32][]byte{
				apktest.V2:  apktest.Scheme(apktest.Signer(false, key)),
				apktest.V31: apktest.Scheme(apktest.Signer(true, other)),
			}, apktest.V2, apktest.V31)
		}, []string{sum("release cert"), sum("other cert")}, false},
		{"two signers", func(t *testing.T) []byte {
			return apktest.WithBlock(t, map[uint32][]byte{apktest.V2: apktest.Scheme(apktest.Signer(false, key), apktest.Signer(false, other))}, apktest.V2)
		}, []string{sum("release cert"), sum("other cert")}, false},
		{"only another block", func(t *testing.T) []byte {
			return apktest.WithBlock(t, map[uint32][]byte{pad: []byte("x")}, pad)
		}, nil, true},
		{"no signing block (v1 only)", func(t *testing.T) []byte {
			var z bytes.Buffer
			w := zip.NewWriter(&z)
			if f, err := w.Create("a"); err == nil {
				_, _ = f.Write([]byte("a"))
			}
			_ = w.Close()
			return z.Bytes()
		}, nil, true},
		{"signer without certificate", func(t *testing.T) []byte {
			return apktest.WithBlock(t, map[uint32][]byte{apktest.V2: apktest.Scheme(apktest.Signer(false))}, apktest.V2)
		}, nil, true},
		{"truncated scheme value", func(t *testing.T) []byte {
			return apktest.WithBlock(t, map[uint32][]byte{apktest.V2: apktest.Scheme(apktest.Signer(false, key))[:9]}, apktest.V2)
		}, nil, true},
		{"not a zip", func(*testing.T) []byte { return []byte("hello, world, this is not an apk at all") }, nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := c.apk(t)
			got, err := Signers(bytes.NewReader(b), int64(len(b)))
			if c.error {
				if err == nil {
					t.Fatalf("accepted: %v", got)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %v %v, want %v", got, err, c.want)
			}
		})
	}
}

func FuzzSigners(f *testing.F) {
	f.Add(apktest.Signed(f, []byte("c")))
	f.Add([]byte("PK\x05\x06"))
	f.Fuzz(func(t *testing.T, b []byte) {
		got, err := Signers(bytes.NewReader(b), int64(len(b)))
		if err == nil && len(got) == 0 {
			t.Fatal("no error and no signer")
		}
	})
}
