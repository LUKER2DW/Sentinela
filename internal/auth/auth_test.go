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

func TestPasswordPolicyMinimum(t *testing.T) {
	if err := CheckPasswordPolicy("admin123"); err != nil {
		t.Fatalf("8 chars must be accepted: %v", err)
	}
	if err := CheckPasswordPolicy("admin12"); err == nil {
		t.Fatal("7 chars must be refused")
	}
}

func TestTOTPRFC6238(t *testing.T) {
	// RFC 6238 appendix B, SHA1 key "12345678901234567890" (8-digit values,
	// last 6 digits here).
	secret := b32.EncodeToString([]byte("12345678901234567890"))
	cases := map[int64]string{59: "287082", 1111111109: "081804", 1234567890: "005924", 2000000000: "279037"}
	for ts, want := range cases {
		got, err := TOTPCode(secret, TOTPStep(time.Unix(ts, 0)))
		if err != nil || got != want {
			t.Errorf("t=%d: got %q, want %q (%v)", ts, got, want, err)
		}
	}
}

func TestVerifyTOTP(t *testing.T) {
	secret := NewTOTPSecret()
	now := time.Unix(1_800_000_000, 0)
	prev, _ := TOTPCode(secret, TOTPStep(now)-1)
	cur, _ := TOTPCode(secret, TOTPStep(now))
	old, _ := TOTPCode(secret, TOTPStep(now)-3)
	if st, ok := VerifyTOTP(secret, cur, now, 0); !ok || st != TOTPStep(now) {
		t.Fatal("current code refused")
	}
	if _, ok := VerifyTOTP(secret, prev, now, 0); !ok {
		t.Fatal("previous step refused")
	}
	if _, ok := VerifyTOTP(secret, cur, now, TOTPStep(now)); ok {
		t.Fatal("code reused")
	}
	if _, ok := VerifyTOTP(secret, old, now, 0); ok {
		t.Fatal("stale code accepted")
	}
	if _, ok := VerifyTOTP(secret, "12345", now, 0); ok {
		t.Fatal("short code accepted")
	}
}
