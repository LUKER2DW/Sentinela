package dvrip

import (
	"bytes"
	"testing"
)

// A camera (or anything answering on its port) may send any bytes: the
// packet reader never panics, never allocates past maxPayload and returns
// exactly the payload the header announces.
// Run longer with: go test -fuzz=FuzzReadPacket ./internal/camera/dvrip
func FuzzReadPacket(f *testing.F) {
	f.Add(append(EncodeHeader(1, 2, 1000, 4), "{}\n\x00"...))
	f.Add(EncodeHeader(1, 2, 1000, maxPayload+1))
	f.Add([]byte{0xFF})
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		h, payload, err := ReadPacket(bytes.NewReader(data))
		if err != nil {
			return
		}
		if uint32(len(payload)) != h.Len || h.Len > maxPayload {
			t.Fatalf("payload %d bytes, header says %d", len(payload), h.Len)
		}
	})
}
