package apps

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A crashed Compose member whose bind folder became a link since `up` is not
// started again: the project stops until the next deploy.
func TestComposeCrashRestartRechecksBinds(t *testing.T) {
	a, eng, src := newTestAgent(t)
	var bind string
	got := deployCompose(t, a, eng, src, App{Name: "Site"}, func(id string) string {
		bind = filepath.Join(a.appDir(id), "data", "uploads")
		return `{"services":{"web":{"image":"nginx","volumes":[{"type":"bind","source":"` + filepath.ToSlash(bind) + `","target":"/u"}]}}}`
	})
	if got.Status.Job.Phase != "done" {
		t.Fatalf("deploy: %+v", got.Status.Job)
	}
	if err := os.Remove(bind); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), bind); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	eng.members[0].Running = false // crashed (exit 1)
	starts := eng.called("start sapp-" + got.ID + "-web")
	a.Reconcile(context.Background())
	if eng.called("start sapp-"+got.ID+"-web") != starts {
		t.Fatal("a member was started through a planted link")
	}
	app := a.snapshot(got.ID)
	if app.Desired != Stopped || !app.NeedsDeploy {
		t.Fatalf("project not stopped: desired %s, needs deploy %v", app.Desired, app.NeedsDeploy)
	}
}

// A stopped single-container service whose data folder became a link is not
// started again.
func TestSingleRestartRechecksBinds(t *testing.T) {
	a, eng, _ := newTestAgent(t)
	v, _ := a.Create(nodeApp())
	a.Enqueue(v.ID, "deploy")
	runQueued(t, a)
	data := filepath.Join(a.appDir(v.ID), "data")
	if err := os.RemoveAll(data); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), data); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	eng.containers["sapp-meu-bot"].running = false
	starts := eng.exact("start sapp-meu-bot")
	a.Reconcile(context.Background())
	got, _ := a.viewOf(v.ID)
	if eng.exact("start sapp-meu-bot") != starts || !strings.Contains(got.Status.Error, "symbolic link") {
		t.Fatalf("started through a link: %q", got.Status.Error)
	}
}
