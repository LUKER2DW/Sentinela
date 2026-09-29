package nas

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Thumbnail limits (ADR-0012): JPEG/PNG only, never video (no transcoding).
const (
	thumbMaxBytes  = 20 << 20
	thumbMaxPixels = 24_000_000 // a 24 MP JPEG decodes to ~36 MB of YCbCr
	thumbPNGPixels = 12_000_000 // PNG decodes to 4 bytes per pixel
	thumbSize      = 320
)

// ErrNoThumb means the file has no thumbnail (type, size or decode).
var ErrNoThumb = errors.New("no thumbnail for this file")

// Thumbs makes JPEG thumbnails cached under Dir. Only one decode runs at a
// time (the old i5 also records and serves video).
type Thumbs struct {
	Dir  string
	gate chan struct{}
}

// NewThumbs returns a thumbnail maker caching in dir.
func NewThumbs(dir string) *Thumbs { return &Thumbs{Dir: dir, gate: make(chan struct{}, 1)} }

// Thumbable reports whether a name can get a thumbnail.
func Thumbable(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jpg", ".jpeg", ".png":
		return true
	}
	return false
}

// Get returns the path of a cached thumbnail for share path p, making it if needed.
func (t *Thumbs) Get(s *Share, a Access, p string) (string, error) {
	f, info, err := s.OpenFile(a, p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if !Thumbable(info.Name()) || info.Size() > thumbMaxBytes {
		return "", ErrNoThumb
	}
	clean, _ := Clean(p)
	sum := sha256.Sum256(fmt.Appendf(nil, "%s|%d|%d", clean, info.Size(), info.ModTime().UnixNano()))
	out := filepath.Join(t.Dir, hex.EncodeToString(sum[:16])+".jpg")
	if _, err := os.Stat(out); err == nil {
		return out, nil
	}
	t.gate <- struct{}{}
	defer func() { <-t.gate }()
	if _, err := os.Stat(out); err == nil { // made while we waited
		return out, nil
	}
	img, err := decodeLimited(f, info.Name())
	if err != nil {
		return "", ErrNoThumb
	}
	if err := os.MkdirAll(t.Dir, 0o750); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(t.Dir, ".thumb-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if err := jpeg.Encode(tmp, scaleDown(img, thumbSize), &jpeg.Options{Quality: 78}); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	return out, os.Rename(tmp.Name(), out)
}

func decodeLimited(r io.ReadSeeker, name string) (image.Image, error) {
	cfg, format, err := image.DecodeConfig(r)
	if err != nil {
		return nil, err
	}
	limit := thumbMaxPixels
	if format == "png" {
		limit = thumbPNGPixels
	}
	if cfg.Width*cfg.Height > limit || cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, ErrNoThumb
	}
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	if format == "png" {
		return png.Decode(r)
	}
	return jpeg.Decode(r)
}

// scaleDown fits img into size×size, averaging a 3×3 grid of samples per
// output pixel: cheap (a few hundred thousand reads), good enough for a
// thumbnail, no dependency.
func scaleDown(img image.Image, size int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= size && h <= size {
		return img
	}
	tw, th := size, h*size/w
	if h > w {
		tw, th = w*size/h, size
	}
	tw, th = max(tw, 1), max(th, 1)
	dst := image.NewRGBA(image.Rect(0, 0, tw, th))
	const n = 3
	for y := 0; y < th; y++ {
		for x := 0; x < tw; x++ {
			var r, g, bl, cnt uint32
			for sy := 0; sy < n; sy++ {
				for sx := 0; sx < n; sx++ {
					px := b.Min.X + (x*n+sx)*w/(tw*n)
					py := b.Min.Y + (y*n+sy)*h/(th*n)
					cr, cg, cb, _ := img.At(px, py).RGBA()
					r, g, bl, cnt = r+cr, g+cg, bl+cb, cnt+1
				}
			}
			dst.Set(x, y, color.RGBA{uint8(r / cnt >> 8), uint8(g / cnt >> 8), uint8(bl / cnt >> 8), 255})
		}
	}
	return dst
}
