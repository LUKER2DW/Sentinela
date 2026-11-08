// Package redact removes credentials from strings before they reach logs,
// errors or API responses.
package redact

import (
	"regexp"
	"strings"
)

var (
	userinfo = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^/\s]*@`)
	// Password-like values end at "&", a space or "_" (the underscore-separated
	// camera path form: user=a_password=b_channel=0).
	passCred = regexp.MustCompile(`(?i)(password|pass|pwd|passwd)=[^&\s_]*`)
	// Tokens and tickets are base64url, so "_" and "-" belong to the value:
	// they end only at "&", a space, a quote or a bracket.
	tokenCred = regexp.MustCompile(`(?i)(token|ticket)=[^&\s"')\]]*`)
)

// URL hides userinfo and password-like query/path parameters in any URL-ish string.
func URL(s string) string {
	s = userinfo.ReplaceAllString(s, "${1}REDACTED@")
	s = passCred.ReplaceAllString(s, "${1}=REDACTED")
	return tokenCred.ReplaceAllString(s, "${1}=REDACTED")
}

// Secrets replaces every occurrence of each non-empty secret, then applies URL.
func Secrets(s string, secrets ...string) string {
	for _, sec := range secrets {
		if sec != "" {
			s = strings.ReplaceAll(s, sec, "REDACTED")
		}
	}
	return URL(s)
}
