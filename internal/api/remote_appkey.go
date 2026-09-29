package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Tailscale API used to mint auth keys for the app's embedded node (P2,
// ADR-0019). Outbound HTTPS only.
const tailscaleAPI = "https://api.tailscale.com"

var appHostPattern = regexp.MustCompile(`[^a-z0-9-]+`)

// appKey mints a single-use, pre-approved, 10-minute auth key tagged
// remote.app_tag and hands it to a signed-in user at home. The app joins
// the tailnet with it once; the key is useless afterwards.
func (s *Server) appKey(w http.ResponseWriter, r *http.Request) {
	rc := s.Cfg.Remote
	if rc.OAuthClientID == "" || rc.OAuthClientSecret == "" {
		writeErr(w, http.StatusServiceUnavailable, "app keys are not configured on the server")
		return
	}
	u := userFrom(r)
	if !s.loginRL.Allow(fmt.Sprintf("appkey:%d", u.ID)) {
		writeErr(w, http.StatusTooManyRequests, "too many attempts, wait a minute")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	key, err := mintTailscaleKey(ctx, s.tsHTTP, s.tsAPI, rc.OAuthClientID, rc.OAuthClientSecret, rc.AppTag,
		"Sentinela app ("+u.Username+")")
	s.audit(r, "remote.app_key", u.Username, err == nil, "")
	if err != nil {
		s.Log.Warn("tailscale app key", "err", err)
		writeErr(w, http.StatusBadGateway, "Tailscale refused the key request")
		return
	}
	host := "sentinela-app-" + strings.Trim(appHostPattern.ReplaceAllString(strings.ToLower(u.Username), "-"), "-")
	writeJSON(w, http.StatusOK, map[string]string{"key": key, "hostname": host})
}

// mintTailscaleKey exchanges the OAuth client for a token, then creates the
// auth key. Errors never include the client secret or the key.
func mintTailscaleKey(ctx context.Context, hc *http.Client, base, id, secret, tag, desc string) (string, error) {
	form := url.Values{"client_id": {id}, "client_secret": {secret}, "grant_type": {"client_credentials"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/v2/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := doJSON(hc, req, &tok); err != nil {
		return "", fmt.Errorf("oauth token: %w", err)
	}
	if tok.AccessToken == "" {
		return "", errors.New("oauth token: empty")
	}
	body, _ := json.Marshal(map[string]any{
		"capabilities": map[string]any{"devices": map[string]any{"create": map[string]any{
			"reusable": false, "ephemeral": false, "preauthorized": true, "tags": []string{tag},
		}}},
		"expirySeconds": 600,
		"description":   truncate(desc, 50),
	})
	req, err = http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/v2/tailnet/-/keys", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	var key struct {
		Key string `json:"key"`
	}
	if err := doJSON(hc, req, &key); err != nil {
		return "", fmt.Errorf("create key: %w", err)
	}
	if !strings.HasPrefix(key.Key, "tskey-") {
		return "", errors.New("create key: unexpected answer")
	}
	return key.Key, nil
}

func doJSON(hc *http.Client, req *http.Request, v any) error {
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		// The error body names the problem (scope, tag); it holds no secret.
		return fmt.Errorf("%s: %s", resp.Status, truncate(strings.TrimSpace(string(b)), 200))
	}
	return json.Unmarshal(b, v)
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
