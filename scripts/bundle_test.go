package scripts

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestDeployBundleNames: servers that update themselves install the
// release's deploy bundle (git ls-files deploy scripts) only when every
// member is a plain file or folder whose name auto-update.sh accepts. A file
// that breaks the rule fails here, on the pull request, instead of on every
// server at the next release. The release workflow checks the same before
// packing.
func TestDeployBundleNames(t *testing.T) {
	src, err := os.ReadFile("auto-update.sh")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`name = re\.compile\(r"([^"]+)"\)`).FindSubmatch(src)
	if m == nil {
		t.Fatal("the bundle's name pattern was not found in auto-update.sh")
	}
	// Python's re.fullmatch; this pattern is valid RE2 as written.
	name, err := regexp.Compile(`^(?:` + string(m[1]) + `)$`)
	if err != nil {
		t.Fatalf("auto-update.sh's pattern %q: %v", m[1], err)
	}
	for _, bad := range []string{"scripts/a b.sh", "scripts/ç.sh", "deploy/x\n", "other/x", "scripts"} {
		if name.MatchString(bad) {
			t.Errorf("pattern accepts %q", bad)
		}
	}

	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not found")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, git, "ls-files", "-s", "-z", "--", "deploy", "scripts")
	cmd.Dir = ".."
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("not a git checkout: %v", err)
	}
	n := 0
	for _, rec := range bytes.Split(out, []byte{0}) {
		if len(rec) == 0 {
			continue
		}
		// "<mode> <object> <stage>\t<path>"
		meta, path, ok := strings.Cut(string(rec), "\t")
		mode, _, _ := strings.Cut(meta, " ")
		switch {
		case !ok:
			t.Errorf("unexpected ls-files record %q", rec)
		case mode != "100644" && mode != "100755":
			t.Errorf("%s: mode %s (a link or submodule): auto-update.sh refuses the bundle", path, mode)
		case !name.MatchString(path) || strings.Contains("/"+path+"/", "/../") || strings.Contains("/"+path+"/", "/./"):
			t.Errorf("%q: auto-update.sh refuses this name in the bundle", path)
		}
		n++
	}
	if n == 0 {
		t.Skip("no files listed (not a git checkout)")
	}
}
