package push

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func testServiceAccount(t *testing.T, tokenURI string) []byte {
	t.Helper()
	k, _ := rsa.GenerateKey(rand.Reader, 2048)
	der, _ := x509.MarshalPKCS8PrivateKey(k)
	pemKey := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	b, _ := json.Marshal(map[string]string{"type": "service_account", "project_id": "casa-test", "client_email": "x@casa-test.iam",
		"private_key": string(pemKey), "token_uri": tokenURI})
	return b
}

func TestFCM(t *testing.T) {
	tokens, sends := 0, 0
	var last map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			tokens++
			_ = r.ParseForm()
			if parts := strings.Split(r.Form.Get("assertion"), "."); len(parts) != 3 {
				w.WriteHeader(400)
				return
			}
			_, _ = io.WriteString(w, `{"access_token":"tok","expires_in":3600}`)
		case "/send":
			sends++
			if r.Header.Get("Authorization") != "Bearer tok" {
				w.WriteHeader(401)
				return
			}
			_ = json.NewDecoder(r.Body).Decode(&last)
			if strings.Contains(r.URL.RawQuery, "gone") {
				w.WriteHeader(404)
				_, _ = io.WriteString(w, `{"error":{"status":"NOT_FOUND","details":[{"errorCode":"UNREGISTERED"}]}}`)
				return
			}
			_, _ = io.WriteString(w, `{"name":"projects/casa-test/messages/1"}`)
		}
	}))
	defer srv.Close()
	f, err := ParseFCM(testServiceAccount(t, srv.URL+"/token"))
	if err != nil || f.ProjectID != "casa-test" {
		t.Fatalf("parse: %v", err)
	}
	f.sendURL = srv.URL + "/send"
	ctx := context.Background()
	for range 2 {
		if err := f.Send(ctx, "device-token", map[string]string{"id": "abc"}); err != nil {
			t.Fatal(err)
		}
	}
	if tokens != 1 || sends != 2 {
		t.Fatalf("token fetched %d times, %d sends", tokens, sends)
	}
	msg := last["message"].(map[string]any)
	if msg["token"] != "device-token" || msg["data"].(map[string]any)["id"] != "abc" || msg["notification"] != nil {
		t.Fatalf("message = %v (must be data-only)", msg)
	}
	if err := f.Validate(ctx, "device-token"); err != nil {
		t.Fatal(err)
	}
	if last["validate_only"] != true {
		t.Fatalf("validation must not deliver: %v", last)
	}
	f.sendURL = srv.URL + "/send?gone=1"
	if err := f.Send(ctx, "old", nil); !errors.Is(err, ErrUnregistered) {
		t.Fatalf("unregistered: %v", err)
	}
	if _, err := ParseFCM([]byte(`{"type":"authorized_user"}`)); err == nil {
		t.Fatal("non service-account key accepted")
	}
	if f, err := LoadFCM(t.TempDir() + "/none.json"); f != nil || err != nil {
		t.Fatal("missing file must disable FCM quietly")
	}
}

// Run manually with SENTINELA_FCM_TEST_KEY to verify the installed service
// account and Google's OAuth endpoint without exposing the credential.
func TestFCMServiceAccountOptIn(t *testing.T) {
	path := os.Getenv("SENTINELA_FCM_TEST_KEY")
	if path == "" {
		t.Skip("set SENTINELA_FCM_TEST_KEY for the live credential check")
	}
	f, err := LoadFCM(path)
	if err != nil || f == nil {
		t.Fatalf("service account unavailable: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := f.Ready(ctx); err != nil {
		t.Fatalf("Firebase OAuth unavailable: %v", err)
	}
}
