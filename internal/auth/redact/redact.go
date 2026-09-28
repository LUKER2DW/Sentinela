// Package redact removes credentials from strings before they reach logs,
// errors or API responses.
package redact

import (
	"regexp"
	"strings"
)

var (
	userinfo  = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^/\s]*@`)
	queryCred = regexp.MustCompile(`(?i)(password|pass|pwd|passwd|token|ticket)=[^&\s_]*`)
)

// URL hides userinfo and password-like query/path parameters in any URL-ish string.
func URL(s string) string {
	s = userinfo.ReplaceAllString(s, "${1}REDACTED@")
	return queryCred.ReplaceAllString(s, "${1}=REDACTED")
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
