package apps

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// The services firewall (scripts/apps-firewall.sh, ADR-0021) lives in the
// host's kernel, out of the agent's reach. Once the script has applied the
// rules and checked that DOCKER-USER jumps to them, it writes firewallMarker
// on the host's /run (a tmpfs: a reboot clears it; mounted read-only into the
// agent): the boot id, then the ports it opened to the internet. A failed
// apply, a stopped firewall unit (also when Docker stops) and
// `setup-apps.sh disable` remove it. No container on the services' networks
// starts without a marker of the current boot, so neither a reboot, a Docker
// restart nor an agent restart can start services ahead of the rules.
const (
	firewallMarker    = "/run/sentinela-apps/firewall-ready"
	maxFirewallMarker = 64 << 10
	// firewallRetry: while the marker is missing or out of date, the agent
	// asks the host to apply the firewall again (by rewriting public-ports,
	// which the path unit follows) at most this often.
	firewallRetry = time.Minute
	// firewallWait: how long a deploy or a settings change waits for the
	// firewall before it fails.
	firewallWait = 30 * time.Second
)

// errFirewall marks a start refused because the services firewall is not
// (yet) in place; supervision waits instead of counting it as a crash.
var errFirewall = errors.New("the services firewall is not in place")

var markerPort = regexp.MustCompile(`^[0-9]{1,5}/(tcp|udp)$`)

// firewallState is what the firewall script last verified.
type firewallState struct {
	boot   string
	public map[string]bool // "20001/tcp"
}

// readFirewallMarker reads the marker without following a link or blocking
// on a FIFO, and only from a small regular file.
func readFirewallMarker(p string) (firewallState, error) {
	f, err := os.OpenFile(p, os.O_RDONLY|openNonblock|openNoFollow, 0)
	if err != nil {
		return firewallState{}, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return firewallState{}, err
	}
	if !st.Mode().IsRegular() {
		return firewallState{}, errNotRegular
	}
	if st.Size() > maxFirewallMarker {
		return firewallState{}, errTooLarge
	}
	b, err := io.ReadAll(io.LimitReader(f, maxFirewallMarker+1))
	if err != nil {
		return firewallState{}, err
	}
	if len(b) > maxFirewallMarker {
		return firewallState{}, errTooLarge
	}
	return parseFirewallMarker(string(b))
}

// parseFirewallMarker reads "boot <id>" and "public <port>/<proto>" lines.
func parseFirewallMarker(s string) (firewallState, error) {
	st := firewallState{public: map[string]bool{}}
	for _, l := range strings.Split(s, "\n") {
		k, v, _ := strings.Cut(strings.TrimSpace(l), " ")
		switch {
		case k == "":
		case k == "boot" && st.boot == "" && v != "" && !strings.ContainsAny(v, " \t"):
			st.boot = v
		case k == "public" && markerPort.MatchString(v):
			st.public[v] = true
		default:
			return firewallState{}, fmt.Errorf("firewall marker: unexpected line %q", firstLine(l))
		}
	}
	if st.boot == "" {
		return firewallState{}, errors.New("firewall marker: no boot id")
	}
	return st, nil
}

func portKey(p Port) string {
	proto := p.Proto
	if proto == "" {
		proto = "tcp"
	}
	return fmt.Sprintf("%d/%s", p.Host, proto)
}

// firewallGate returns nil when a container on the services' networks may
// start: the firewall was verified for this boot, and none of ports (on an
// exposure other than the internet) is still open to the internet from an
// earlier list (a port a deleted public service freed, say).
func (a *Agent) firewallGate(exposure string, ports []Port) error {
	st, err := a.firewall()
	if err != nil {
		return fmt.Errorf("%w yet: waiting for scripts/apps-firewall.sh on the host", errFirewall)
	}
	if boot := a.bootID(); boot == "" || st.boot != boot {
		return fmt.Errorf("%w since the last boot: waiting for scripts/apps-firewall.sh on the host", errFirewall)
	}
	if exposure != ExposePublic {
		for _, p := range ports {
			if p.Host != 0 && st.public[portKey(p)] {
				return fmt.Errorf("%w for port %d yet: it is still open to the internet until the firewall applies the new list", errFirewall, p.Host)
			}
		}
	}
	return nil
}

// awaitFirewall is firewallGate for deploys: it publishes the current list
// of internet ports, asks the host to apply the firewall again if needed,
// and waits up to a.fwWait.
func (a *Agent) awaitFirewall(ctx context.Context, exposure string, ports []Port) error {
	a.writePublicPorts(false)
	err := a.firewallGate(exposure, ports)
	if err == nil {
		return nil
	}
	a.requestFirewall()
	deadline := time.Now().Add(a.fwWait)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
		if err = a.firewallGate(exposure, ports); err == nil {
			return nil
		}
	}
	return err
}

// requestFirewall rewrites public-ports (same content) so the host's path
// unit applies the firewall again, at most every firewallRetry.
func (a *Agent) requestFirewall() {
	a.mu.Lock()
	now := a.now()
	if !a.fwAsked.IsZero() && now.Sub(a.fwAsked) < firewallRetry {
		a.mu.Unlock()
		return
	}
	a.fwAsked = now
	a.mu.Unlock()
	a.Log.Warn("apps: services firewall not in place; asking the host to apply it")
	a.writePublicPorts(true)
}

// holdForFirewall records that supervision did not start a service because
// of the firewall (shown on the service; one event per wait).
func (a *Agent) holdForFirewall(id string, err error) {
	a.requestFirewall()
	a.mu.Lock()
	defer a.mu.Unlock()
	r := a.rt(id)
	msg := a.redactLocked(id, err.Error())
	if !r.fwHeld || r.err != msg {
		a.eventLocked(id, "waiting", msg)
	}
	r.err, r.fwHeld = msg, true
}

// releaseFirewallHold clears the waiting message once the gate is open.
func (a *Agent) releaseFirewallHold(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if r := a.run[id]; r != nil && r.fwHeld {
		r.err, r.fwHeld = "", false
	}
}

// loadPublicPorts reads the list the agent last published, so an agent
// restart does not rewrite (and make the host re-apply) an unchanged list.
func (a *Agent) loadPublicPorts() {
	root, err := os.OpenRoot(a.Cfg.StateDir)
	if err != nil {
		return
	}
	defer root.Close()
	b, err := readCapped(root, filepath.Base(a.publicPortsFile()), maxFirewallMarker)
	if err != nil {
		return
	}
	a.lastPublic = strings.TrimSuffix(string(b), "\n")
}

func (a *Agent) publicPortsFile() string { return filepath.Join(a.Cfg.StateDir, "public-ports") }
