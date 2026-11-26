package config

import (
	"errors"
	"fmt"
	"net"
	"path"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// dashboardKeys are the top-level keys a change applied from the dashboard
// may touch (ADR-0024, ADR-0025). Every other key, the services platform
// (apps) and the update source (app_updates) above all, stays as installed.
var dashboardKeys = map[string]bool{
	"timezone":  true,
	"nas_host":  true,
	"recording": true,
	"cameras":   true,
	"volumes":   true,
	"remote":    true,
}

// NASRoot is the only directory the containers mount read-write for the
// file API (deploy/docker-compose.yml); a "nas" volume may not point elsewhere.
const NASRoot = "/srv/nas"

// FunnelListenDefault is the loopback address the dashboard gives Tailscale
// Funnel (internal/api/settings.go).
const FunnelListenDefault = "127.0.0.1:8081"

// reservedPorts are ports other parts of the stack listen on; a public
// listener may not be moved onto them.
var reservedPorts = map[int]bool{1984: true, 8554: true, 8555: true, 7091: true, 8081: true}

// CheckDashboardChange is the root-side policy for a change the dashboard
// asked the host to apply (scripts/apply-config.sh): the API enforces it too,
// but the host does not trust a file the unprivileged API container could
// have written without going through that code. It compares the raw YAML of
// the installed config with the candidate and refuses when
//   - a key the dashboard cannot edit differs (apps, app_updates, listen,
//     data_dir, host file paths...);
//   - a public, HTTP-01 or tunnel listener port is outside 1024..65535 or is
//     one the stack itself uses;
//   - remote.funnel_listen changes to anything but the loopback default;
//   - a "nas" volume points outside NASRoot (unless it was already installed);
//   - a camera's user_env/password_env is not <NAME>_USER/<NAME>_PASS, the
//     names the dashboard gives (unless the camera had them installed).
func CheckDashboardChange(installed, candidate []byte) error {
	var old, next Config
	if err := yaml.Unmarshal(installed, &old); err != nil {
		return fmt.Errorf("installed config: %w", err)
	}
	if err := yaml.Unmarshal(candidate, &next); err != nil {
		return fmt.Errorf("candidate config: %w", err)
	}
	var errs []error

	ov, nv := reflect.ValueOf(old), reflect.ValueOf(next)
	for i := 0; i < ov.NumField(); i++ {
		f := ov.Type().Field(i)
		key := strings.Split(f.Tag.Get("yaml"), ",")[0]
		if key == "" || key == "-" || dashboardKeys[key] {
			continue
		}
		if !reflect.DeepEqual(normalize(ov.Field(i).Interface()), normalize(nv.Field(i).Interface())) {
			errs = append(errs, fmt.Errorf("%s cannot be changed from the dashboard", key))
		}
	}

	errs = append(errs, checkRemotePolicy(old, next)...)
	errs = append(errs, checkCredentialNames(old, next)...)

	oldNAS := map[string]bool{}
	for _, v := range old.Volumes {
		if v.Kind == "nas" {
			oldNAS[path.Clean(v.Path)] = true
		}
	}
	for _, v := range next.Volumes {
		if v.Kind != "nas" {
			continue
		}
		p := path.Clean(v.Path)
		if oldNAS[p] || p == NASRoot || strings.HasPrefix(p, NASRoot+"/") {
			continue
		}
		errs = append(errs, fmt.Errorf("volume %q: a nas volume must be %s or inside it", v.ID, NASRoot))
	}
	return errors.Join(errs...)
}

// Credential names the dashboard gives a camera (internal/api credEnv). The
// root secrets merge (scripts/apply-config.sh) writes only names a camera of
// the candidate uses, so a candidate naming, say, LD_PRELOAD as a password
// could otherwise set any variable of the containers.
var (
	dashboardUserEnv = regexp.MustCompile(`^[A-Z0-9_]{1,59}_USER$`)
	dashboardPassEnv = regexp.MustCompile(`^[A-Z0-9_]{1,59}_PASS$`)
)

func checkCredentialNames(old, next Config) []error {
	installed := map[string]Camera{}
	for _, c := range old.Cameras {
		installed[c.ID] = c
	}
	var errs []error
	for _, c := range next.Cameras {
		if o, ok := installed[c.ID]; ok && o.UserEnv == c.UserEnv && o.PasswordEnv == c.PasswordEnv {
			continue // as installed (names written by hand stay usable)
		}
		if !dashboardUserEnv.MatchString(c.UserEnv) || !dashboardPassEnv.MatchString(c.PasswordEnv) {
			errs = append(errs, fmt.Errorf("camera %s: user_env/password_env must be <NAME>_USER and <NAME>_PASS", c.ID))
		}
	}
	return errs
}

// secretForbidden are the printable characters a credential set from the
// dashboard may not carry (see SecretValueOK).
const secretForbidden = "\"#$'\\`{}"

// SecretValueOK reports whether v may be stored in secrets.env as a camera
// credential from the dashboard: 1 to 256 printable ASCII characters other
// than space, quotes, backslash, '$', '{', '}' and '#'. secrets.env has
// several readers: the host checks it with Docker's --env-file (literal
// values), the containers get it through Compose's env_file (which expands
// ${...} and strips quotes and comments) and go2rtc pastes values into its
// YAML. With these characters, line breaks and control characters out, all
// of them read the same text. scripts/apply-config.sh applies the same rule
// on the root side.
func SecretValueOK(v string) bool {
	if v == "" || len(v) > 256 {
		return false
	}
	for i := 0; i < len(v); i++ {
		if c := v[i]; c <= ' ' || c > '~' || strings.IndexByte(secretForbidden, c) >= 0 {
			return false
		}
	}
	return true
}

// normalize makes "absent" and "empty" the same for the comparison.
func normalize(v any) any {
	if a, ok := v.(Apps); ok && len(a.Runtimes) == 0 {
		a.Runtimes = nil
		return a
	}
	return v
}

func checkRemotePolicy(old, next Config) []error {
	var errs []error
	first, last := next.Apps.PortFirst, next.Apps.PortLast
	if first == 0 {
		first = 20000
	}
	if last == 0 {
		last = 20099
	}
	r, o := next.Remote, old.Remote
	for _, l := range []struct{ what, listen, was string }{
		{"remote.public.listen", r.Public.Listen, o.Public.Listen},
		{"remote.public.http01_listen", r.Public.HTTP01Listen, o.Public.HTTP01Listen},
		{"remote.proxy.listen", r.Proxy.Listen, o.Proxy.Listen},
	} {
		if l.listen == "" {
			continue
		}
		_, ps, err := net.SplitHostPort(l.listen)
		port, perr := strconv.Atoi(ps)
		if err != nil || perr != nil || port < 1024 || port > 65535 {
			errs = append(errs, fmt.Errorf("%s: the port must be between 1024 and 65535", l.what))
			continue
		}
		if l.listen == l.was {
			continue // as installed: only the range above is enforced
		}
		switch {
		case reservedPorts[port]:
			errs = append(errs, fmt.Errorf("%s: port %d is used by another part of Sentinela", l.what, port))
		case portInMainListen(next.Listen, port):
			errs = append(errs, fmt.Errorf("%s: port %d is the dashboard's own port", l.what, port))
		case port >= first && port <= last:
			errs = append(errs, fmt.Errorf("%s: port %d is in the services range", l.what, port))
		}
	}
	if f := r.FunnelListen; f != "" && f != o.FunnelListen && f != FunnelListenDefault {
		errs = append(errs, fmt.Errorf("remote.funnel_listen can only be %s", FunnelListenDefault))
	}
	return errs
}

// portInMainListen reports whether listen (":8080", "0.0.0.0:8080", "")
// names port; an empty listen is the default :8080.
func portInMainListen(listen string, port int) bool {
	if listen == "" {
		listen = ":8080"
	}
	_, ps, err := net.SplitHostPort(listen)
	if err != nil {
		return false
	}
	n, err := strconv.Atoi(ps)
	return err == nil && n == port
}
