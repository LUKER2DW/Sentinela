package auth

import (
	"testing"
	"time"
)

func TestHashVerify(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(h, "correct horse battery") {
		t.Fatal("valid password rejected")
	}
	if VerifyPassword(h, "wrong password!!") {
		t.Fatal("invalid password accepted")
	}
	if VerifyPassword("garbage", "x") {
		t.Fatal("garbage hash accepted")
	}
	if _, err := HashPassword("short"); err == nil {
		t.Fatal("short password accepted")
	}
}

func TestRoleAtLeast(t *testing.T) {
	cases := []struct {
		have, need string
		want       bool
	}{
		{RoleAdmin, RoleOperator, true},
		{RoleOperator, RoleOperator, true},
		{RoleViewer, RoleOperator, false},
		{"", RoleViewer, false},
		{RoleAdmin, "bogus", false},
	}
	for _, c := range cases {
		if got := RoleAtLeast(c.have, c.need); got != c.want {
			t.Errorf("RoleAtLeast(%q,%q)=%v", c.have, c.need, got)
		}
	}
}

func TestLimiter(t *testing.T) {
	l := NewLimiter(2, time.Minute)
	now := time.Unix(0, 0)
	l.now = func() time.Time { return now }
	for i, want := range []bool{true, true, false} {
		if got := l.Allow("a"); got != want {
			t.Fatalf("attempt %d: Allow=%v want %v", i+1, got, want)
		}
	}
	if !l.Allow("b") {
		t.Fatal("keys must be independent")
	}
	now = now.Add(61 * time.Second)
	if !l.Allow("a") {
		t.Fatal("window did not reset")
	}
}

func TestTicketsSingleUseAndExpiry(t *testing.T) {
	tk := NewTickets(time.Minute)
	now := time.Unix(0, 0)
	tk.now = func() time.Time { return now }
	tok, _ := tk.Issue(1, "cam1", "main")
	got, ok := tk.Redeem(tok)
	if !ok || got.CameraID != "cam1" || got.UserID != 1 {
		t.Fatalf("redeem failed: %+v", got)
	}
	if _, ok := tk.Redeem(tok); ok {
		t.Fatal("ticket reused")
	}
	tok2, _ := tk.Issue(1, "cam1", "sub")
	now = now.Add(2 * time.Minute)
	if _, ok := tk.Redeem(tok2); ok {
		t.Fatal("expired ticket accepted")
	}
}
