package nas

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newShare(t *testing.T) (*Share, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, dir
}

func write(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

var (
	ana   = Access{User: "ana", Write: true}
	guest = Access{User: "vera"} // viewer
	root  = Access{User: "admin", Admin: true, Write: true}
)

func TestAccessRules(t *testing.T) {
	cases := []struct {
		a          Access
		path       string
		read, edit bool
	}{
		{ana, "", true, false},
		{ana, "ana", true, false}, // top level belongs to the admin
		{ana, "ana/foto.jpg", true, true},
		{ana, "Compartilhado/lista.txt", true, true},
		{ana, "bia/segredo.txt", false, false},
		{ana, ".lixeira", true, false},
		{ana, ".lixeira/ana/x.txt", true, false},
		{ana, ".lixeira/bia/x.txt", false, false},
		{ana, "lost+found", false, false},
		{ana, "ana/.x.txt.sentinela-part", false, false},
		{guest, "Compartilhado/lista.txt", true, false},
		{root, "bia/segredo.txt", true, true},
		{root, "Nova", true, true},
		{root, "lost+found/x", false, false},
		{Access{User: ".lixeira", Write: true}, ".lixeira/x", false, false}, // no own folder, no bin
	}
	for _, c := range cases {
		p, err := Clean(c.path)
		if err != nil {
			t.Fatal(err)
		}
		if got := c.a.canRead(p); got != c.read {
			t.Errorf("%s canRead(%q) = %v", c.a.User, c.path, got)
		}
		if got := c.a.canChange(p); got != c.edit {
			t.Errorf("%s canChange(%q) = %v", c.a.User, c.path, got)
		}
	}
}

func TestCleanStaysInside(t *testing.T) {
	for in, want := range map[string]string{
		"../../etc/passwd": "etc/passwd", "/ana/../bia": "bia", "": "", "ana//x/": "ana/x",
	} {
		if got, err := Clean(in); err != nil || got != want {
			t.Errorf("Clean(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := Clean("ana\\..\\x"); err == nil {
		t.Error("backslash accepted")
	}
}

func TestListHidesOthersAndCreatesFolders(t *testing.T) {
	s, dir := newShare(t)
	write(t, dir, "bia/segredo.txt", "x")
	write(t, dir, "Compartilhado/b.txt", "bb")
	write(t, dir, "Compartilhado/A pasta/x", "")
	write(t, dir, "Compartilhado/.oculto", "")
	top, err := s.List(ana, "")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range top {
		names = append(names, e.Name)
	}
	if strings.Join(names, ",") != "ana,Compartilhado" {
		t.Fatalf("top = %v", names)
	}
	shared, _ := s.List(ana, "Compartilhado")
	if len(shared) != 2 || !shared[0].Dir || shared[1].Name != "b.txt" || shared[1].Size != 2 {
		t.Fatalf("shared = %+v", shared)
	}
	if _, err := s.List(ana, "bia"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("list bia: %v", err)
	}
}

func TestSymlinkEscapeBlocked(t *testing.T) {
	s, dir := newShare(t)
	outside := t.TempDir()
	write(t, outside, "secret.txt", "s")
	write(t, dir, "ana/ok.txt", "")
	if err := os.Symlink(outside, filepath.Join(dir, "ana", "fora")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	if _, _, err := s.OpenFile(ana, "ana/fora/secret.txt"); err == nil {
		t.Fatal("followed a symlink out of the share")
	}
	list, err := s.List(ana, "ana")
	if err != nil || len(list) != 1 {
		t.Fatalf("escaping link listed: %+v %v", list, err)
	}
}

func TestResumableUpload(t *testing.T) {
	s, dir := newShare(t)
	if err := s.Ensure(ana); err != nil {
		t.Fatal(err)
	}
	body := strings.Repeat("0123456789", 10)
	have, done, err := s.WriteChunk(ana, "ana/v.bin", 0, 100, strings.NewReader(body[:40]))
	if err != nil || done || have != 40 {
		t.Fatalf("chunk 1: %d %v %v", have, done, err)
	}
	if n, _ := s.PartSize(ana, "ana/v.bin"); n != 40 {
		t.Fatalf("part size %d", n)
	}
	if _, _, err := s.WriteChunk(ana, "ana/v.bin", 10, 100, strings.NewReader("x")); !errors.Is(err, ErrOffset) {
		t.Fatalf("wrong offset: %v", err)
	}
	if list, _ := s.List(ana, "ana"); len(list) != 0 {
		t.Fatalf("partial file visible: %+v", list)
	}
	have, done, err = s.WriteChunk(ana, "ana/v.bin", 40, 100, strings.NewReader(body[40:]))
	if err != nil || !done || have != 100 {
		t.Fatalf("chunk 2: %d %v %v", have, done, err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "ana", "v.bin"))
	if string(got) != body {
		t.Fatal("content mismatch")
	}
	if _, _, err := s.WriteChunk(ana, "ana/v.bin", 0, 1, strings.NewReader("x")); !errors.Is(err, ErrExists) {
		t.Fatalf("overwrite: %v", err)
	}
	if _, _, err := s.WriteChunk(ana, "ana/big", 0, 3, strings.NewReader("toolong")); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("too long: %v", err)
	}
	if _, _, err := s.WriteChunk(guest, "Compartilhado/x", 0, 1, strings.NewReader("x")); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer upload: %v", err)
	}
	if _, _, err := s.WriteChunk(ana, "ana/sem-pasta/x", 0, 1, strings.NewReader("x")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing folder: %v", err)
	}
}

func TestDeleteMovesToBinAndMove(t *testing.T) {
	s, dir := newShare(t)
	write(t, dir, "ana/Fotos/a.jpg", "a")
	s.now = func() time.Time { return time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC) }
	dst, err := s.Delete(ana, "ana/Fotos/a.jpg")
	if err != nil || dst != ".lixeira/ana/ana/Fotos/a.jpg" {
		t.Fatalf("delete: %q %v", dst, err)
	}
	write(t, dir, "ana/Fotos/a.jpg", "again")
	if dst, _ = s.Delete(ana, "ana/Fotos/a.jpg"); dst != ".lixeira/ana/ana/Fotos/a (2).jpg" {
		t.Fatalf("second delete: %q", dst)
	}
	if _, err := s.Delete(ana, dst); !errors.Is(err, ErrForbidden) {
		t.Fatalf("delete inside bin: %v", err)
	}
	if err := s.Mkdir(ana, "ana/Docs"); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "ana/n.txt", "n")
	if err := s.Ensure(ana); err != nil {
		t.Fatal(err)
	}
	if err := s.Move(ana, "ana/n.txt", "Compartilhado/n.txt"); err != nil {
		t.Fatalf("move: %v", err)
	}
	if err := s.Move(ana, "ana/Docs", "ana/Docs/sub"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("move into itself: %v", err)
	}
	if err := s.Move(ana, "Compartilhado/n.txt", "bia/n.txt"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("move to another user: %v", err)
	}
	if err := s.Mkdir(ana, "ana/bad:name"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("invalid name: %v", err)
	}
}

func TestThumbnail(t *testing.T) {
	s, dir := newShare(t)
	img := image.NewRGBA(image.Rect(0, 0, 1000, 500))
	for x := 0; x < 1000; x++ {
		for y := 0; y < 500; y++ {
			img.Set(x, y, color.RGBA{uint8(x), 100, 200, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "ana/p.png", buf.String())
	write(t, dir, "ana/v.mp4", "not an image")
	th := NewThumbs(t.TempDir())
	out, err := th.Get(s, ana, "ana/p.png")
	if err != nil {
		t.Fatal(err)
	}
	f, _ := os.Open(out)
	defer f.Close()
	cfg, format, err := image.DecodeConfig(f)
	if err != nil || format != "jpeg" || cfg.Width != thumbSize || cfg.Height != thumbSize/2 {
		t.Fatalf("thumb %v %s %v", cfg, format, err)
	}
	if again, _ := th.Get(s, ana, "ana/p.png"); again != out {
		t.Fatal("cache miss")
	}
	if _, err := th.Get(s, ana, "ana/v.mp4"); !errors.Is(err, ErrNoThumb) {
		t.Fatalf("video thumb: %v", err)
	}
	_, _ = io.Copy(io.Discard, f)
}
