package publiccert

import (
	"crypto/x509"
	"testing"
)

func TestSelfSigned(t *testing.T) {
	dir := t.TempDir()
	tc, fp, err := SelfSigned(dir, "203.0.113.7")
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(tc.Certificates[0].Certificate[0])
	if err != nil || len(leaf.IPAddresses) != 1 || leaf.IPAddresses[0].String() != "203.0.113.7" || len(fp) != 64 {
		t.Fatalf("certificate %v %v fp=%q", err, leaf.IPAddresses, fp)
	}
	// Kept across restarts: the app pins this fingerprint.
	if _, again, err := SelfSigned(dir, "203.0.113.7"); err != nil || again != fp {
		t.Fatalf("reloaded fingerprint %q, want %q (%v)", again, fp, err)
	}
	// A different public IP gets a new certificate.
	if _, other, _ := SelfSigned(dir, "203.0.113.8"); other == fp {
		t.Fatal("same certificate for another IP")
	}
	if _, _, err := SelfSigned(dir, "casa.example.com"); err == nil {
		t.Fatal("self-signed for a name accepted")
	}
}
