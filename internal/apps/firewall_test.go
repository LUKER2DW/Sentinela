package apps

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseFirewallMarker(t *testing.T) {
	for _, c := range []struct {
		name   string
		in     string
		boot   string
		public []string
		ok     bool
	}{
		{"boot only", "boot b-1\n", "b-1", nil, true},
		{"ports", "boot b-1\npublic 20001/tcp\npublic 20002/udp\n", "b-1", []string{"20001/tcp", "20002/udp"}, true},
		{"empty", "", "", nil, false},
		{"no boot", "public 20001/tcp\n", "", nil, false},
		{"two boots", "boot b-1\nboot b-2\n", "", nil, false},
		{"bad port", "boot b-1\npublic 20001/sctp\n", "", nil, false},
		{"unknown line", "boot b-1\nallow all\n", "", nil, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			st, err := parseFirewallMarker(c.in)
			if (err == nil) != c.ok {
				t.Fatalf("err %v", err)
			}
			if !c.ok {
				return
			}
			if st.boot != c.boot || len(st.public) != len(c.public) {
				t.Fatalf("%+v", st)
			}
			for _, p := range c.public {
				if !st.public[p] {
					t.Errorf("port %s missing", p)
				}
			}
		})
	}
}

func TestReadFirewallMarker(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "ready")
	if err := os.WriteFile(good, []byte("boot b-1\npublic 20001/tcp\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if st, err := readFirewallMarker(good); err != nil || st.boot != "b-1" || !st.public["20001/tcp"] {
		t.Fatalf("%+v %v", st, err)
	}
	big := filepath.Join(dir, "big")
	if err := os.WriteFile(big, []byte("boot b-1\n"+strings.Repeat("public 20001/tcp\n", maxFirewallMarker/16)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readFirewallMarker(big); !errors.Is(err, errTooLarge) {
		t.Fatalf("large marker: %v", err)
	}
	if _, err := readFirewallMarker(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing marker read")
	}
	if _, err := readFirewallMarker(dir); err == nil {
		t.Fatal("a folder read as the marker")
	}
}

// Supervision starts no service while the firewall is not verified for this
// boot, or while a port of a non-internet service is still open to the
// internet; waiting does not count toward the crash-loop pause, and the agent
// asks the host to apply the firewall again (at most once per firewallRetry).
func TestFirewallGateHoldsSupervisedStarts(t *testing.T) {
	for _, c := range []struct {
		name   string
		marker func() (firewallState, error)
		starts bool
	}{
		{"ready", func() (firewallState, error) { return firewallState{boot: "boot-1"}, nil }, true},
		{"missing", func() (firewallState, error) { return firewallState{}, os.ErrNotExist }, false},
		{"previous boot", func() (firewallState, error) { return firewallState{boot: "boot-0"}, nil }, false},
		{"port still public", func() (firewallState, error) {
			return firewallState{boot: "boot-1", public: map[string]bool{"20000/tcp": true}}, nil
		}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			a, eng, _ := newTestAgent(t)
			v, _ := a.Create(nodeApp())
			a.Enqueue(v.ID, "deploy")
			runQueued(t, a)
			if got, _ := a.viewOf(v.ID); got.Status.Job.Phase != "done" || len(got.Ports) == 0 || got.Ports[0].Host != 20000 {
				t.Fatalf("deploy: %+v %+v", got.Status.Job, got.Ports)
			}
			eng.containers["sapp-meu-bot"].running = false
			a.firewall = c.marker
			starts := eng.exact("start sapp-meu-bot")
			pp := a.publicPortsFile()
			old := time.Now().Add(-time.Hour)
			if err := os.Chtimes(pp, old, old); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < crashLimit+2; i++ {
				a.Reconcile(context.Background())
			}
			got, _ := a.viewOf(v.ID)
			started := eng.exact("start sapp-meu-bot") > starts
			if started != c.starts {
				t.Fatalf("started %v, want %v (%s)", started, c.starts, got.Status.Error)
			}
			st, _ := os.Stat(pp)
			if c.starts {
				return
			}
			if got.Status.State != "waiting" || !strings.Contains(got.Status.Error, "firewall") || got.Status.Restarts != 0 {
				t.Fatalf("while waiting: state %s, error %q, restarts %d", got.Status.State, got.Status.Error, got.Status.Restarts)
			}
			if !st.ModTime().After(old) {
				t.Fatal("the host was not asked to apply the firewall again")
			}
			// Once in place, the next check starts the service and clears the wait.
			a.firewall = func() (firewallState, error) { return firewallState{boot: "boot-1"}, nil }
			a.Reconcile(context.Background())
			got, _ = a.viewOf(v.ID)
			if eng.exact("start sapp-meu-bot") == starts || got.Status.Error != "" {
				t.Fatalf("not started once the firewall is in place: %q", got.Status.Error)
			}
		})
	}
}

// A deploy waits for the firewall and fails without running the setup step
// or creating the service container; so does applying new settings.
func TestFirewallGateRefusesDeploys(t *testing.T) {
	a, eng, _ := newTestAgent(t)
	a.firewall = func() (firewallState, error) { return firewallState{}, os.ErrNotExist }
	v, _ := a.Create(nodeApp())
	a.Enqueue(v.ID, "deploy")
	runQueued(t, a)
	got, _ := a.viewOf(v.ID)
	if got.Status.Job.Phase != "failed" || !strings.Contains(got.Status.Job.Error, "firewall") {
		t.Fatalf("deploy without the firewall: %+v", got.Status.Job)
	}
	if eng.called("create") != 0 || eng.called("start") != 0 {
		t.Fatalf("containers started: %v", eng.calls)
	}
	// Deployed with the firewall, then settings applied without it.
	a.firewall = func() (firewallState, error) { return firewallState{boot: "boot-1"}, nil }
	a.Enqueue(v.ID, "deploy")
	runQueued(t, a)
	a.firewall = func() (firewallState, error) { return firewallState{boot: "boot-0"}, nil }
	creates := eng.called("create")
	if err := a.recreate(context.Background(), v.ID, func(string, ...any) {}); err == nil || !errors.Is(err, errFirewall) {
		t.Fatalf("recreate without the firewall: %v", err)
	}
	if eng.called("create") != creates {
		t.Fatal("a container was created without the firewall")
	}
}

// Compose: no `up` and no restart of a crashed member while the firewall is
// not in place; running members are left alone.
func TestFirewallGateHoldsCompose(t *testing.T) {
	a, eng, src := newTestAgent(t)
	got := deployCompose(t, a, eng, src, App{Name: "Site"}, func(string) string { return `{"services":{"web":{"image":"nginx"}}}` })
	if got.Status.Job.Phase != "done" {
		t.Fatalf("deploy: %+v", got.Status.Job)
	}
	a.firewall = func() (firewallState, error) { return firewallState{}, os.ErrNotExist }
	eng.members[0].Running = false // crashed (exit 1)
	starts, helpers := eng.called("start sapp-"+got.ID+"-web"), eng.called("create sapp-"+got.ID+"-compose")
	a.Reconcile(context.Background())
	a.mu.Lock()
	a.rt(got.ID).composeUp = false // as after an agent restart
	a.mu.Unlock()
	a.Reconcile(context.Background())
	if eng.called("start sapp-"+got.ID+"-web") != starts || eng.called("create sapp-"+got.ID+"-compose") != helpers {
		t.Fatalf("compose started without the firewall: %v", eng.calls)
	}
	if v, _ := a.viewOf(got.ID); !strings.Contains(v.Status.Error, "firewall") {
		t.Fatalf("error %q", v.Status.Error)
	}
	a.firewall = func() (firewallState, error) { return firewallState{boot: "boot-1"}, nil }
	a.Reconcile(context.Background())
	if eng.called("create sapp-"+got.ID+"-compose") == helpers {
		t.Fatal("no `up` once the firewall is in place")
	}
}

// An agent restart does not rewrite an unchanged public-ports file (each
// rewrite makes the host apply the firewall again).
func TestPublicPortsKeptAcrossAgentRestart(t *testing.T) {
	a, _, _ := newTestAgent(t)
	in := nodeApp()
	in.Exposure = ExposePublic
	v, _ := a.Create(in)
	a.Enqueue(v.ID, "deploy")
	runQueued(t, a)
	a.Reconcile(context.Background())
	pp := a.publicPortsFile()
	b, err := os.ReadFile(pp)
	if err != nil || strings.TrimSpace(string(b)) != "20000/tcp" {
		t.Fatalf("public-ports %q %v", b, err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(pp, old, old); err != nil {
		t.Fatal(err)
	}
	r := NewAgent(a.Cfg, a.Eng, a.Sources, quiet)
	r.bootID, r.uptime, r.memFreeMB, r.diskSpace = a.bootID, a.uptime, a.memFreeMB, a.diskSpace
	r.firewall = a.firewall
	if err := r.Load(); err != nil {
		t.Fatal(err)
	}
	r.Reconcile(context.Background())
	if st, _ := os.Stat(pp); st.ModTime().After(old) {
		t.Fatal("unchanged public-ports rewritten at agent start")
	}
}
