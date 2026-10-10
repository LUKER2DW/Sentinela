package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Downloads through the logging wrapper must keep the connection's
// ReadFrom (sendfile on plain HTTP) and still count status and bytes.
func TestStatusWriterReadFrom(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f.bin")
	if err := os.WriteFile(p, []byte(strings.Repeat("x", 100_000)), 0o644); err != nil {
		t.Fatal(err)
	}
	var sw *statusWriter
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		if _, ok := w.(io.ReaderFrom); !ok {
			t.Error("server writer has no ReadFrom")
		}
		sw = &statusWriter{ResponseWriter: w}
		f, _ := os.Open(p)
		defer f.Close()
		http.ServeContent(sw, r, "f.bin", time.Time{}, f)
	}))
	defer srv.Close()
	res, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	n, _ := io.Copy(io.Discard, res.Body)
	res.Body.Close()
	<-done // the client can receive the body before ReadFrom updates its counters
	if n != 100_000 || sw.code != http.StatusOK || sw.bytes != 100_000 {
		t.Fatalf("got %d bytes, logged code %d bytes %d", n, sw.code, sw.bytes)
	}
}
