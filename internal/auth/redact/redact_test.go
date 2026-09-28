package redact

import "testing"

func TestURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"rtsp://admin:p@ss@10.0.0.2:554/x", "rtsp://REDACTED@10.0.0.2:554/x"},
		{"rtsp://10.0.0.2:554/user=admin&password=s3cr3t&channel=1", "rtsp://10.0.0.2:554/user=admin&password=REDACTED&channel=1"},
		{"rtsp://h/user=admin_password=abc_channel=0", "rtsp://h/user=admin_password=REDACTED_channel=0"},
		{"/stream/ws?ticket=abcdef&x=1", "/stream/ws?ticket=REDACTED&x=1"},
		{"plain text", "plain text"},
	}
	for _, c := range cases {
		if got := URL(c.in); got != c.want {
			t.Errorf("URL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSecrets(t *testing.T) {
	got := Secrets("dial failed: bad password Pa@ss for admin", "Pa@ss", "")
	if got != "dial failed: bad password REDACTED for admin" {
		t.Fatalf("got %q", got)
	}
}
