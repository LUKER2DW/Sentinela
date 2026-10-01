package nas

import (
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestOwnerFollowsTheItem(t *testing.T) {
	s, dir := newShare(t)
	if err := unix.Setxattr(dir, ownerAttr, []byte("x"), 0); err != nil {
		t.Skip("no user extended attributes here:", err)
	}
	write(t, dir, Shared+"/.keep", "")
	if err := s.Mkdir(ana, Shared+"/Fotos"); err != nil {
		t.Fatal(err)
	}
	if _, done, err := s.WriteChunk(ana, Shared+"/nota.txt", 0, 3, strings.NewReader("abc")); err != nil || !done {
		t.Fatal(done, err)
	}
	got := s.Owners(Shared, []string{"Fotos", "nota.txt", "nada"})
	if got["Fotos"] != "ana" || got["nota.txt"] != "ana" || len(got) != 2 {
		t.Fatalf("owners = %v", got)
	}
	// Moves and edits keep it; someone else editing does not take it over.
	if err := s.Move(ana, Shared+"/nota.txt", Shared+"/Fotos/nota.txt"); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceText(Access{User: "bia", Write: true}, Shared+"/Fotos/nota.txt", []byte("novo")); err != nil {
		t.Fatal(err)
	}
	if u := s.Owner(Shared + "/Fotos/nota.txt"); u != "ana" {
		t.Fatalf("owner after move+edit = %q", u)
	}
	// Made outside the web (Samba): unknown.
	write(t, dir, Shared+"/smb.txt", "x")
	if u := s.Owner(filepath.ToSlash(Shared + "/smb.txt")); u != "" {
		t.Fatalf("samba file owner = %q", u)
	}
}
