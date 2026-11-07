package push

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// FCM sends data-only messages to the Android app through Firebase Cloud
// Messaging's HTTP v1 API (ADR-0020). The messages carry an opaque id only;
// the app fetches the notification itself from the house.
type FCM struct {
	ProjectID   string
	clientEmail string
	tokenURI    string
	key         *rsa.PrivateKey
	hc          *http.Client
	sendURL     string // tests point it at a fake server

	mu      sync.Mutex
	access  string
	expires time.Time
}

// ErrUnregistered: the token is no longer valid (app removed, data cleared).
var ErrUnregistered = errors.New("fcm: token unregistered")

type serviceAccount struct {
	Type        string `json:"type"`
	ProjectID   string `json:"project_id"`
	PrivateKey  string `json:"private_key"`
	ClientEmail string `json:"client_email"`
	TokenURI    string `json:"token_uri"`
}

// LoadFCM reads a Firebase service account key file. A missing file is not
// an error: it returns nil, nil and the app keeps its own connection.
func LoadFCM(path string) (*FCM, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("fcm: %w", err)
	}
	return ParseFCM(b)
}

// ParseFCM builds a sender from the service account JSON.
func ParseFCM(b []byte) (*FCM, error) {
	var sa serviceAccount
	if err := json.Unmarshal(b, &sa); err != nil {
		return nil, errors.New("fcm: service account file is not JSON")
	}
	if sa.Type != "service_account" || sa.ProjectID == "" || sa.ClientEmail == "" {
		return nil, errors.New("fcm: not a service account key")
	}
	blk, _ := pem.Decode([]byte(sa.PrivateKey))
	if blk == nil {
		return nil, errors.New("fcm: no private key")
	}
	k, err := x509.ParsePKCS8PrivateKey(blk.Bytes)
	if err != nil {
		return nil, errors.New("fcm: unreadable private key")
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("fcm: private key is not RSA")
	}
	if sa.TokenURI == "" {
		sa.TokenURI = "https://oauth2.googleapis.com/token"
	}
	return &FCM{
		ProjectID: sa.ProjectID, clientEmail: sa.ClientEmail, tokenURI: sa.TokenURI, key: rk,
		hc:      &http.Client{Timeout: 10 * time.Second},
		sendURL: "https://fcm.googleapis.com/v1/projects/" + url.PathEscape(sa.ProjectID) + "/messages:send",
	}, nil
}

// accessToken returns a cached OAuth2 token, fetching a new one with a
// signed JWT assertion when it is about to expire.
func (f *FCM) accessToken(ctx context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.access != "" && time.Until(f.expires) > 2*time.Minute {
		return f.access, nil
	}
	now := time.Now()
	head := b64.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, _ := json.Marshal(map[string]any{
		"iss": f.clientEmail, "scope": "https://www.googleapis.com/auth/firebase.messaging",
		"aud": f.tokenURI, "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	})
	unsigned := head + "." + b64.EncodeToString(claims)
	sum := sha256.Sum256([]byte(unsigned))
	sig, err := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, sum[:])
	if err != nil {
		return "", fmt.Errorf("fcm: sign: %w", err)
	}
	form := url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"}, "assertion": {unsigned + "." + b64.EncodeToString(sig)}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, f.tokenURI, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := f.hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("fcm: token: %w", err)
	}
	defer res.Body.Close()
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if res.StatusCode != http.StatusOK || json.NewDecoder(res.Body).Decode(&tok) != nil || tok.AccessToken == "" {
		return "", fmt.Errorf("fcm: token request failed (HTTP %d)", res.StatusCode)
	}
	f.access, f.expires = tok.AccessToken, now.Add(time.Duration(tok.ExpiresIn)*time.Second)
	return f.access, nil
}

// Ready confirms the service-account credentials can obtain an OAuth token.
func (f *FCM) Ready(ctx context.Context) error {
	_, err := f.accessToken(ctx)
	return err
}

// Validate checks the device token and send permission without delivering.
func (f *FCM) Validate(ctx context.Context, token string) error {
	return f.send(ctx, token, map[string]string{"id": "registration-check"}, true)
}

// Send delivers a data-only, high-priority message to one app token.
func (f *FCM) Send(ctx context.Context, token string, data map[string]string) error {
	return f.send(ctx, token, data, false)
}

func (f *FCM) send(ctx context.Context, token string, data map[string]string, validateOnly bool) error {
	at, err := f.accessToken(ctx)
	if err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]any{"validate_only": validateOnly, "message": map[string]any{
		"token":   token,
		"data":    data,
		"android": map[string]any{"priority": "HIGH", "ttl": "600s"},
	}})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, f.sendURL, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+at)
	req.Header.Set("Content-Type", "application/json")
	res, err := f.hc.Do(req)
	if err != nil {
		return fmt.Errorf("fcm: send: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusOK {
		return nil
	}
	// A 404 can also mean an unknown Firebase project. Only FCM's explicit
	// UNREGISTERED error proves that the device token should be removed.
	var failure struct {
		Error struct {
			Status  string `json:"status"`
			Details []struct {
				ErrorCode string `json:"errorCode"`
			} `json:"details"`
		} `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&failure)
	unregistered := failure.Error.Status == "UNREGISTERED"
	for _, detail := range failure.Error.Details {
		unregistered = unregistered || detail.ErrorCode == "UNREGISTERED"
	}
	if unregistered {
		return ErrUnregistered
	}
	if res.StatusCode == http.StatusUnauthorized {
		f.mu.Lock()
		f.access = "" // fetch a new token next time
		f.mu.Unlock()
	}
	return fmt.Errorf("fcm: send failed (HTTP %d)", res.StatusCode)
}
