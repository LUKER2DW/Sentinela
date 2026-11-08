package apps

import (
	"path/filepath"
	"strings"
	"testing"
)

// An app id can never name a path outside the apps folder, whatever reaches
// appDir (and so releaseDir and the secrets vault path built on it).
func TestAppDirStaysInsideTheAppsFolder(t *testing.T) {
	a, _, _ := newTestAgent(t)
	base := filepath.Join(a.Cfg.StateDir, "apps")
	for _, id := range []string{"my-bot", "../../etc", "a/b", `..\x`, "", "UPPER", "/abs", "x\x00y"} {
		dir := a.appDir(id)
		if rel, err := filepath.Rel(base, dir); err != nil || strings.HasPrefix(rel, "..") || strings.ContainsAny(rel, `/\`) {
			t.Errorf("appDir(%q) = %q escapes %q (rel %q)", id, dir, base, rel)
		}
	}
	if got := a.appDir("my-bot"); got != filepath.Join(base, "my-bot") {
		t.Errorf("a valid id must keep its own folder, got %q", got)
	}
}
