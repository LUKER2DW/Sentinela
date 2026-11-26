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
	data, err := c.post(ctx, c.device(), "", `<GetSystemDateAndTime xmlns="`+devNS+`"/>`, false, replyMax)
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

// httpClient is c.HTTP with redirects refused. Whatever answers at the
// camera address must not be able to send a request, with its SOAP body
// and digest, to another host (go2rtc's API on loopback, for one): only
// the configured scheme, host and port are ever contacted. Applied per
// request, so a client set up by a caller (wizard, tests) obeys it too.
func (c *Client) httpClient() *http.Client {
	hc := http.Client{Timeout: 20 * time.Second}
	if c.HTTP != nil {
		hc = *c.HTTP
	}
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &hc
}

// Reply size limits. Decoding XML costs about ten times its size in heap,
// so the call made every second (PullMessages: 32 real messages are about
// 32 KiB) reads far less than the rare ones (profiles, capabilities).
const (
	replyMax = 4 << 20
	pullMax  = 256 << 10
)

// call posts a SOAP envelope to url with an optional WS-Addressing header.
func (c *Client) call(ctx context.Context, endpoint, action, body string) ([]byte, error) {
	return c.post(ctx, endpoint, action, body, true, replyMax)
}

// post sends one envelope; auth adds the WS-Security UsernameToken. A reply
// longer than limit is an error (a cut one would not parse anyway).
func (c *Client) post(ctx context.Context, endpoint, action, body string, auth bool, limit int64) ([]byte, error) {
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
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return nil, fmt.Errorf("onvif %s: HTTP %d: the camera redirected the request (not followed)", action, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("onvif %s: reply over %d bytes", action, limit)
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

// onBase reports whether addr is an absolute URL on the client's own
// scheme, host and port (no userinfo): rebasing it would change nothing.
func (c *Client) onBase(addr string) bool {
	base, err := url.Parse(c.Base)
	if err != nil || base.Host == "" {
		return false
	}
	u, err := url.Parse(addr)
	if err != nil || u.User != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Scheme, base.Scheme) && strings.EqualFold(u.Host, base.Host)
}

// Notification is one decoded event message.
type Notification struct {
	Topic string
	Items map[string]string // SimpleItem Name → Value (Source and Data)
}

// pullLimit is the most messages one Pull asks for, and the most it
// returns: a camera that sends more (MessageLimit is only a request) is
// cut to its last pullLimit messages, the newest state of each topic.
const pullLimit = 32

// Pull waits up to timeout for messages.
func (s *Subscription) Pull(ctx context.Context, timeout time.Duration) ([]Notification, error) {
	body := fmt.Sprintf(`<PullMessages xmlns="%s"><Timeout>PT%dS</Timeout><MessageLimit>%d</MessageLimit></PullMessages>`, evNS, int(timeout.Seconds()), pullLimit)
	data, err := s.c.post(ctx, s.URL, evNS+"/PullPointSubscription/PullMessagesRequest", body, true, pullMax)
	if err != nil {
		return nil, err
	}
	return parseNotifications(data, pullLimit)
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

// notificationXML is one NotificationMessage of a PullMessagesResponse.
type notificationXML struct {
	Topic string `xml:"Topic"`
	Items []struct {
		Name  string `xml:"Name,attr"`
		Value string `xml:"Value,attr"`
	} `xml:"Message>Message>Data>SimpleItem"`
	Source []struct {
		Name  string `xml:"Name,attr"`
		Value string `xml:"Value,attr"`
	} `xml:"Message>Message>Source>SimpleItem"`
}

// What one notification keeps of the camera's strings. Real topics are
// tens of bytes ("tns1:RuleEngine/CellMotionDetector/Motion") and items are
// a few short names and values; a reply is up to 4 MiB, and the watcher
// keeps active topics between pulls, so nothing longer is kept.
const (
	maxTopicLen     = 256
	maxItems        = 16
	maxItemNameLen  = 64
	maxItemValueLen = 256
)

// notification converts one message; ok is false for a topic longer than
// maxTopicLen (no camera sends one: the message is dropped). Items longer
// than their limits are left out, and at most maxItems are kept, Data
// first: a Data item wins over a Source item of the same name.
func (m *notificationXML) notification() (n Notification, ok bool) {
	n = Notification{Topic: strings.TrimSpace(m.Topic), Items: map[string]string{}}
	if len(n.Topic) > maxTopicLen {
		return Notification{}, false
	}
	add := func(name, value string) {
		if _, dup := n.Items[name]; dup || len(n.Items) == maxItems || len(name) > maxItemNameLen || len(value) > maxItemValueLen {
			return
		}
		n.Items[name] = value
	}
	for i := len(m.Items) - 1; i >= 0; i-- { // the last of a repeated name wins, as before
		add(m.Items[i].Name, m.Items[i].Value)
	}
	for i := len(m.Source) - 1; i >= 0; i-- {
		add(m.Source[i].Name, m.Source[i].Value)
	}
	return n, true
}

// ParseNotifications decodes a PullMessagesResponse.
func ParseNotifications(data []byte) ([]Notification, error) {
	return parseNotifications(data, 0)
}

// parseNotifications decodes the NotificationMessages of a
// PullMessagesResponse (Envelope>Body>PullMessagesResponse, matched by
// local name as xml.Unmarshal does) one at a time, keeping only the last
// limit of them (0 = all): a reply crammed with messages costs time in
// proportion to its size, never memory. A message with an oversized topic
// is dropped (notification).
func parseNotifications(data []byte, limit int) ([]Notification, error) {
	d := xml.NewDecoder(bytes.NewReader(data))
	var path []string // open elements, the root first
	out := []Notification{}
	for {
		tok, err := d.Token()
		if err != nil {
			// Like xml.Unmarshal: no root element is an error (io.EOF).
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if len(path) == 3 && path[1] == "Body" && path[2] == "PullMessagesResponse" && t.Name.Local == "NotificationMessage" {
				var m notificationXML
				if err := d.DecodeElement(&m, &t); err != nil {
					return nil, err
				}
				n, ok := m.notification()
				if !ok {
					continue
				}
				if limit > 0 && len(out) == limit {
					copy(out, out[1:])
					out = out[:limit-1]
				}
				out = append(out, n)
				continue
			}
			path = append(path, t.Name.Local)
		case xml.EndElement:
			path = path[:len(path)-1]
			if len(path) == 0 {
				return out, nil // the end of the root element, as xml.Unmarshal stops
			}
		}
	}
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
