package tailnet

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"
)

// PairResult is what the server hands a new app node.
type PairResult struct {
	Key      string
	Hostname string
}

// Pair signs in to the Sentinela server on the home network (lanURL, e.g.
// http://192.168.1.10:8080) and asks for a single-use tailnet key
// (POST /api/remote/app-key, home only). The session it creates is only
// used for this and then signed out.
func Pair(lanURL, username, password string) (*PairResult, error) {
	u, err := url.Parse(strings.TrimRight(lanURL, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("invalid home address")
	}
	jar, _ := cookiejar.New(nil)
	hc := &http.Client{Jar: jar, Timeout: 20 * time.Second}
	base := u.String()

	var login struct {
		CSRF  string `json:"csrf"`
		Error string `json:"error"`
	}
	code, err := postJSON(hc, base+"/api/login", "", map[string]string{"username": username, "password": password}, &login)
	if err != nil {
		return nil, fmt.Errorf("sem resposta do servidor da casa: conecte-se ao Wi-Fi de casa (%w)", err)
	}
	if code != http.StatusOK {
		return nil, errors.New(pairError(code, login.Error))
	}
	defer func() { _, _ = postJSON(hc, base+"/api/logout", login.CSRF, nil, nil) }()

	var key struct {
		Key      string `json:"key"`
		Hostname string `json:"hostname"`
		Error    string `json:"error"`
	}
	code, err = postJSON(hc, base+"/api/remote/app-key", login.CSRF, nil, &key)
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK || key.Key == "" {
		return nil, errors.New(pairError(code, key.Error))
	}
	return &PairResult{Key: key.Key, Hostname: key.Hostname}, nil
}

func postJSON(hc *http.Client, u, csrf string, body, out any) (int, error) {
	var rd io.Reader = http.NoBody
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(http.MethodPost, u, rd)
	if err != nil {
		return 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if out != nil {
		_ = json.Unmarshal(raw, out)
	}
	return resp.StatusCode, nil
}

// pairError turns API errors into the app's language (pt-BR).
func pairError(code int, msg string) string {
	switch {
	case code == http.StatusUnauthorized:
		return "wrong username or password"
	case code == http.StatusTooManyRequests:
		return "muitas tentativas, espere um minuto"
	case code == http.StatusServiceUnavailable:
		return "the server does not have the Tailscale key configured yet (TS_OAUTH_CLIENT_*)"
	case code == http.StatusForbidden:
		return "pairing is only possible when at home"
	case code == http.StatusBadGateway:
		return "o Tailscale recusou criar a chave (confira a tag e o escopo do cliente OAuth)"
	case msg != "":
		return msg
	}
	return fmt.Sprintf("erro %d", code)
}
