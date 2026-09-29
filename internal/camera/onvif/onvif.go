// Package onvif implements the small ONVIF subset Sentinela needs: a
// PullPoint event subscription with WS-Security UsernameToken digest auth.
package onvif

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client talks SOAP 1.2 to one device.
type Client struct {
	Base     string // e.g. http://192.168.1.20:8899
	User     string
	Password string
	HTTP     *http.Client
}

// NewClient returns a client with a sane timeout.
func NewClient(host string, port int, user, pw string) *Client {
	return &Client{Base: fmt.Sprintf("http://%s:%d", host, port), User: user, Password: pw,
		HTTP: &http.Client{Timeout: 20 * time.Second}}
}

func (c *Client) security() string {
	nonce := make([]byte, 16)
	_, _ = rand.Read(nonce)
	created := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	h := sha1.New()
	h.Write(nonce)
	h.Write([]byte(created))
	h.Write([]byte(c.Password))
	digest := base64.StdEncoding.EncodeToString(h.Sum(nil))
	return `<Security s:mustUnderstand="1" xmlns="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd">` +
		`<UsernameToken><Username>` + html.EscapeString(c.User) + `</Username>` +
		`<Password Type="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-username-token-profile-1.0#PasswordDigest">` + digest + `</Password>` +
		`<Nonce EncodingType="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-soap-message-security-1.0#Base64Binary">` +
		base64.StdEncoding.EncodeToString(nonce) + `</Nonce>` +
		`<Created xmlns="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-utility-1.0.xsd">` + created + `</Created>` +
		`</UsernameToken></Security>`
}

// call posts a SOAP envelope to url with an optional WS-Addressing header.
func (c *Client) call(ctx context.Context, url, action, body string) ([]byte, error) {
	hdr := c.security()
	if action != "" {
		hdr += `<a:Action s:mustUnderstand="1">` + action + `</a:Action><a:To s:mustUnderstand="1">` + html.EscapeString(url) + `</a:To>`
	}
	env := `<?xml version="1.0" encoding="UTF-8"?><s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:a="http://www.w3.org/2005/08/addressing">` +
		`<s:Header>` + hdr + `</s:Header><s:Body>` + body + `</s:Body></s:Envelope>`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(env))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", `application/soap+xml; charset=utf-8`)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK || bytes.Contains(data, []byte("Fault>")) {
		return data, fmt.Errorf("onvif %s: HTTP %d: %s", action, resp.StatusCode, faultReason(data))
	}
	return data, nil
}

func faultReason(data []byte) string {
	var f struct {
		Text []string `xml:"Body>Fault>Reason>Text"`
	}
	if xml.Unmarshal(data, &f) == nil && len(f.Text) > 0 {
		return f.Text[0]
	}
	s := string(data)
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}

const evNS = "http://www.onvif.org/ver10/events/wsdl"

// Subscription is an active PullPoint subscription.
type Subscription struct {
	c   *Client
	URL string
}

// Subscribe creates a PullPoint subscription.
func (c *Client) Subscribe(ctx context.Context) (*Subscription, error) {
	body := `<CreatePullPointSubscription xmlns="` + evNS + `"><InitialTerminationTime>PT120S</InitialTerminationTime></CreatePullPointSubscription>`
	data, err := c.call(ctx, c.Base+"/onvif/event_service", evNS+"/EventPortType/CreatePullPointSubscriptionRequest", body)
	if err != nil {
		return nil, err
	}
	var r struct {
		Addr string `xml:"Body>CreatePullPointSubscriptionResponse>SubscriptionReference>Address"`
	}
	if err := xml.Unmarshal(data, &r); err != nil || strings.TrimSpace(r.Addr) == "" {
		return nil, errors.New("onvif: no subscription address")
	}
	return &Subscription{c: c, URL: strings.TrimSpace(r.Addr)}, nil
}

// Notification is one decoded event message.
type Notification struct {
	Topic string
	Items map[string]string // SimpleItem Name → Value (Source and Data)
}

// Pull waits up to timeout for messages.
func (s *Subscription) Pull(ctx context.Context, timeout time.Duration) ([]Notification, error) {
	body := fmt.Sprintf(`<PullMessages xmlns="%s"><Timeout>PT%dS</Timeout><MessageLimit>32</MessageLimit></PullMessages>`, evNS, int(timeout.Seconds()))
	data, err := s.c.call(ctx, s.URL, evNS+"/PullPointSubscription/PullMessagesRequest", body)
	if err != nil {
		return nil, err
	}
	return ParseNotifications(data)
}

// Renew extends the subscription.
func (s *Subscription) Renew(ctx context.Context) error {
	body := `<Renew xmlns="http://docs.oasis-open.org/wsn/b-2"><TerminationTime>PT120S</TerminationTime></Renew>`
	_, err := s.c.call(ctx, s.URL, "http://docs.oasis-open.org/wsn/bw-2/SubscriptionManager/RenewRequest", body)
	return err
}

// Unsubscribe ends the subscription (best effort).
func (s *Subscription) Unsubscribe(ctx context.Context) error {
	_, err := s.c.call(ctx, s.URL, "http://docs.oasis-open.org/wsn/bw-2/SubscriptionManager/UnsubscribeRequest",
		`<Unsubscribe xmlns="http://docs.oasis-open.org/wsn/b-2"/>`)
	return err
}

// ParseNotifications decodes a PullMessagesResponse.
func ParseNotifications(data []byte) ([]Notification, error) {
	var r struct {
		Msgs []struct {
			Topic string `xml:"Topic"`
			Items []struct {
				Name  string `xml:"Name,attr"`
				Value string `xml:"Value,attr"`
			} `xml:"Message>Message>Data>SimpleItem"`
			Source []struct {
				Name  string `xml:"Name,attr"`
				Value string `xml:"Value,attr"`
			} `xml:"Message>Message>Source>SimpleItem"`
		} `xml:"Body>PullMessagesResponse>NotificationMessage"`
	}
	if err := xml.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	out := make([]Notification, 0, len(r.Msgs))
	for _, m := range r.Msgs {
		n := Notification{Topic: strings.TrimSpace(m.Topic), Items: map[string]string{}}
		for _, it := range m.Source {
			n.Items[it.Name] = it.Value
		}
		for _, it := range m.Items {
			n.Items[it.Name] = it.Value
		}
		out = append(out, n)
	}
	return out, nil
}

// Classify maps a notification to (event type, active). ok=false for
// topics Sentinela ignores.
func Classify(n Notification) (typ string, active bool, ok bool) {
	t := n.Topic
	switch {
	case strings.Contains(t, "CellMotionDetector/Motion"), strings.Contains(t, "VideoSource/MotionAlarm"):
		typ = "motion"
	case strings.Contains(t, "TamperDetector"):
		typ = "tamper"
	case strings.Contains(t, "LineDetector"):
		typ = "line"
	case strings.Contains(t, "FieldDetector"):
		typ = "intrusion"
	default:
		return "", false, false
	}
	for _, key := range []string{"IsMotion", "State", "IsTamper", "IsInside", "Value"} {
		if v, found := n.Items[key]; found {
			return typ, strings.EqualFold(v, "true") || v == "1", true
		}
	}
	return "", false, false
}

const devNS = "http://www.onvif.org/ver10/device/wsdl"

// SystemTime reads the device clock (GetSystemDateAndTime, read-only). The
// camera burns this clock into the video, so its offset from the server
// explains an OSD that disagrees with real time.
func (c *Client) SystemTime(ctx context.Context) (time.Time, error) {
	data, err := c.call(ctx, c.Base+"/onvif/device_service", "", `<GetSystemDateAndTime xmlns="`+devNS+`"/>`)
	if err != nil {
		return time.Time{}, err
	}
	return ParseSystemTime(data)
}

// ParseSystemTime extracts UTCDateTime from a GetSystemDateAndTime response.
func ParseSystemTime(data []byte) (time.Time, error) {
	var r struct {
		UTC struct {
			Y  int `xml:"Date>Year"`
			Mo int `xml:"Date>Month"`
			D  int `xml:"Date>Day"`
			H  int `xml:"Time>Hour"`
			Mi int `xml:"Time>Minute"`
			S  int `xml:"Time>Second"`
		} `xml:"Body>GetSystemDateAndTimeResponse>SystemDateAndTime>UTCDateTime"`
	}
	if err := xml.Unmarshal(data, &r); err != nil {
		return time.Time{}, fmt.Errorf("onvif: parse time: %w", err)
	}
	u := r.UTC
	if u.Y < 2000 {
		return time.Time{}, errors.New("onvif: no UTCDateTime in response")
	}
	return time.Date(u.Y, time.Month(u.Mo), u.D, u.H, u.Mi, u.S, 0, time.UTC), nil
}
