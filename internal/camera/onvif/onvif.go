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
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Client talks SOAP 1.2 to one device.
type Client struct {
	Base     string // e.g. http://192.168.1.20:8899
	User     string
	Password string
	HTTP     *http.Client
	// DevicePath is the device service path ("" = /onvif/device_service).
	DevicePath string

	mu sync.Mutex
	// caps are the service addresses the device reported (nil until read).
	caps map[string]string
	// offset is the camera clock minus ours: the UsernameToken digest
	// carries a creation time and many cameras refuse one that is minutes
	// off their own clock.
	offset time.Duration
}

// DefaultDevicePath is the device service path nearly every camera uses.
const DefaultDevicePath = "/onvif/device_service"

// NewClient returns a client with a sane timeout.
func NewClient(host string, port int, user, pw string) *Client {
	return &Client{Base: "http://" + net.JoinHostPort(host, strconv.Itoa(port)), User: user, Password: pw,
		HTTP: &http.Client{Timeout: 20 * time.Second}}
}

// WithDevicePath sets the device service path (empty keeps the default).
func (c *Client) WithDevicePath(p string) *Client {
	c.DevicePath = p
	return c
}

// device is the device service URL. The configured path is appended after
// a "/" on the fixed scheme and host, so it can only name a path there.
func (c *Client) device() string {
	p := strings.TrimLeft(c.DevicePath, "/")
	if p == "" {
		p = strings.TrimLeft(DefaultDevicePath, "/")
	}
	return c.Base + "/" + p
}

// service is the address of one service ("media", "events", "ptz") from
// GetCapabilities, read once per client; fallback (a path) when the device
// does not say.
func (c *Client) service(ctx context.Context, name, fallback string) string {
	c.mu.Lock()
	caps := c.caps
	c.mu.Unlock()
	if caps == nil {
		caps = c.readCapabilities(ctx)
	}
	if a := caps[name]; a != "" {
		return a
	}
	return c.Base + fallback
}

// HasService reports whether the device lists a service ("ptz", ...).
func (c *Client) HasService(ctx context.Context, name string) bool {
	c.mu.Lock()
	caps := c.caps
	c.mu.Unlock()
	if caps == nil {
		caps = c.readCapabilities(ctx)
	}
	return caps[name] != ""
}

func (c *Client) readCapabilities(ctx context.Context) map[string]string {
	c.syncClock(ctx)
	body := `<GetCapabilities xmlns="` + devNS + `"><Category>All</Category></GetCapabilities>`
	data, err := c.call(ctx, c.device(), "", body)
	if err != nil {
		return nil // not cached: asked again next time
	}
	caps, err := ParseCapabilities(data)
	if err != nil {
		return nil
	}
	for k, v := range caps {
		caps[k] = c.rebase(v)
	}
	c.mu.Lock()
	c.caps = caps
	c.mu.Unlock()
	return caps
}

// ParseCapabilities reads the service addresses of a GetCapabilities
// response (lower-case names: media, events, ptz, imaging).
func ParseCapabilities(data []byte) (map[string]string, error) {
	var r struct {
		Media   string `xml:"Body>GetCapabilitiesResponse>Capabilities>Media>XAddr"`
		Events  string `xml:"Body>GetCapabilitiesResponse>Capabilities>Events>XAddr"`
		PTZ     string `xml:"Body>GetCapabilitiesResponse>Capabilities>PTZ>XAddr"`
		Imaging string `xml:"Body>GetCapabilitiesResponse>Capabilities>Imaging>XAddr"`
	}
	if err := xml.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("onvif: parse capabilities: %w", err)
	}
	out := map[string]string{}
	for k, v := range map[string]string{"media": r.Media, "events": r.Events, "ptz": r.PTZ, "imaging": r.Imaging} {
		if v = strings.TrimSpace(v); v != "" {
			out[k] = v
		}
	}
	return out, nil
}

// syncClock reads the camera clock without credentials (the ONVIF core
// spec requires GetSystemDateAndTime to answer unauthenticated) and keeps
// the offset when it is large enough to break the UsernameToken.
func (c *Client) syncClock(ctx context.Context) {
	t0 := time.Now()
	data, err := c.post(ctx, c.device(), "", `<GetSystemDateAndTime xmlns="`+devNS+`"/>`, false)
	if err != nil {
		return
	}
	cam, err := ParseSystemTime(data)
	if err != nil {
		return
	}
	off := cam.Sub(t0.Add(time.Since(t0) / 2))
	if off > -5*time.Second && off < 5*time.Second {
		off = 0
	}
	c.mu.Lock()
	c.offset = off
	c.mu.Unlock()
}

func (c *Client) security() string {
	nonce := make([]byte, 16)
	_, _ = rand.Read(nonce)
	c.mu.Lock()
	off := c.offset
	c.mu.Unlock()
	created := time.Now().Add(off).UTC().Format("2006-01-02T15:04:05Z")
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
func (c *Client) call(ctx context.Context, endpoint, action, body string) ([]byte, error) {
	return c.post(ctx, endpoint, action, body, true)
}

// post sends one envelope; auth adds the WS-Security UsernameToken.
func (c *Client) post(ctx context.Context, endpoint, action, body string, auth bool) ([]byte, error) {
	hdr := ""
	if auth {
		hdr = c.security()
	}
	if action != "" {
		hdr += `<a:Action s:mustUnderstand="1">` + action + `</a:Action><a:To s:mustUnderstand="1">` + html.EscapeString(endpoint) + `</a:To>`
	}
	env := `<?xml version="1.0" encoding="UTF-8"?><s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:a="http://www.w3.org/2005/08/addressing">` +
		`<s:Header>` + hdr + `</s:Header><s:Body>` + body + `</s:Body></s:Envelope>`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(env))
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
	// The event service address comes from the device: Hikvision answers on
	// /onvif/Events, Axis on /onvif/services, XMEye and Dahua on the default.
	data, err := c.call(ctx, c.service(ctx, "events", "/onvif/event_service"), evNS+"/EventPortType/CreatePullPointSubscriptionRequest", body)
	if err != nil {
		return nil, err
	}
	var r struct {
		Addr string `xml:"Body>CreatePullPointSubscriptionResponse>SubscriptionReference>Address"`
	}
	if err := xml.Unmarshal(data, &r); err != nil || strings.TrimSpace(r.Addr) == "" {
		return nil, errors.New("onvif: no subscription address")
	}
	return &Subscription{c: c, URL: c.rebase(strings.TrimSpace(r.Addr))}, nil
}

// rebase points a device-reported service address at the host Sentinela
// actually reaches. This camera advertises the wired static address from
// NetWork.NetCommon (192.168.1.10) even while it answers on Wi-Fi/DHCP, so
// using the reported host verbatim made every pull time out (2026-11-03).
// Only scheme, host and port are replaced; the path and query are kept.
func (c *Client) rebase(addr string) string {
	base, err := url.Parse(c.Base)
	if err != nil || base.Host == "" {
		return addr
	}
	u, err := url.Parse(addr)
	if err != nil {
		return addr
	}
	if u.Host == "" {
		// A relative reference: resolve it against the base.
		return base.ResolveReference(u).String()
	}
	u.Scheme, u.Host, u.User = base.Scheme, base.Host, nil
	return u.String()
}

// Notification is one decoded event message.
type Notification struct {
	Topic string
	Items map[string]string // SimpleItem Name → Value (Source and Data)
}

// pullLimit is the most messages one Pull returns.
const pullLimit = 32

// Pull waits up to timeout for messages.
func (s *Subscription) Pull(ctx context.Context, timeout time.Duration) ([]Notification, error) {
	body := fmt.Sprintf(`<PullMessages xmlns="%s"><Timeout>PT%dS</Timeout><MessageLimit>%d</MessageLimit></PullMessages>`, evNS, int(timeout.Seconds()), pullLimit)
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
	data, err := c.call(ctx, c.device(), "", `<GetSystemDateAndTime xmlns="`+devNS+`"/>`)
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
