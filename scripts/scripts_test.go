//go:build linux

// Package scripts holds regression tests for the operator scripts that run
// as root (Linux only: they need python3, symlinks and FIFOs). The scripts
// themselves are not Go; `go test ./...` runs these in CI.
package scripts

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func needPython(t *testing.T) string {
	t.Helper()
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not found")
	}
	return py
}

// datafs runs lib/datafs.py with the data owner set to the test's own ids
// (production uses 1000, which only root could chown to).
func datafs(t *testing.T, stdin []byte, args ...string) (string, int) {
	t.Helper()
	return datafsTokens(t, "", stdin, args...)
}

// datafsTokens is datafs with the random part of temporary names taken
// from tokens (comma-separated, in order) instead of secrets.token_hex.
func datafsTokens(t *testing.T, tokens string, stdin []byte, args ...string) (string, int) {
	t.Helper()
	py := needPython(t)
	lib, err := filepath.Abs("lib")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// The module's main() with UID/GID patched; Refused/OSError map to exit 1
	// as in the script's own __main__ block.
	runner := "import os, sys\nsys.path.insert(0, " + strconv.Quote(lib) + ")\nimport datafs\n" +
		"datafs.UID, datafs.GID = os.getuid(), os.getgid()\n" +
		"if os.environ.get('DATAFS_TOKENS'):\n    it = iter(os.environ['DATAFS_TOKENS'].split(','))\n" +
		"    datafs.secrets.token_hex = lambda n: next(it)\n" +
		"try:\n    sys.exit(datafs.main(['datafs.py'] + sys.argv[1:]))\n" +
		"except datafs.Refused as e:\n    print('refusing:', e, file=sys.stderr); sys.exit(1)\n" +
		"except (OSError, ValueError) as e:\n    print('datafs.py:', e, file=sys.stderr); sys.exit(1)\n"
	cmd := exec.CommandContext(ctx, py, append([]string{"-I", "-B", "-c", runner}, args...)...)
	cmd.Stdin = bytes.NewReader(stdin)
	if tokens != "" {
		cmd.Env = append(os.Environ(), "DATAFS_TOKENS="+tokens)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err = cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("datafs %v: hung (%s)", args, errb.String())
	}
	var ee *exec.ExitError
	switch {
	case err == nil:
		return out.String(), 0
	case errors.As(err, &ee):
		return out.String(), ee.ExitCode()
	default:
		t.Fatalf("datafs %v: %v", args, err)
	}
	return "", -1
}

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDatafsRead(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	write(t, filepath.Join(outside, "secret"), "probe-SECRET-1")
	if err := os.Mkdir(filepath.Join(root, "updates"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "updates", "version"), "v1.2.3\n")
	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(root, "updates", "link")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(root, "updates", "fifo"), 0o600); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "updates", "big"), strings.Repeat("x", 65))
	if err := os.Link(filepath.Join(root, "updates", "version"), filepath.Join(root, "updates", "hard")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "dirlink")); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		path string
		out  string
		code int
	}{
		{root + "/updates/version", "", 1}, // two links now (hard below): not plain
		{root + "/updates/link", "", 1},    // a symbolic link is never followed
		{root + "/updates/fifo", "", 1},    // a FIFO neither hangs nor is read
		{root + "/updates/big", "", 1},     // over the cap
		{root + "/updates/missing", "", 2}, // missing
		{root + "/nodir/missing", "", 2},   // missing directory
		{root + "/dirlink/secret", "", 1},  // a linked directory is not walked
		{root + "/updates/../updates/x", "", 1},
		{"/etc/passwd", "", 1}, // not below the root
	}
	for _, c := range cases {
		out, code := datafs(t, nil, "read", root, c.path, "64")
		if code != c.code || out != c.out {
			t.Errorf("read %s: %q, status %d; want %q, %d", c.path, out, code, c.out, c.code)
		}
	}
	if err := os.Remove(filepath.Join(root, "updates", "hard")); err != nil {
		t.Fatal(err)
	}
	if out, code := datafs(t, nil, "read", root, "updates/version", "64"); code != 0 || out != "v1.2.3\n" {
		t.Errorf("plain file: %q, %d", out, code)
	}
}

func TestDatafsTakeRemovesWhatItRefuses(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	write(t, filepath.Join(outside, "request"), "keep me")
	write(t, filepath.Join(root, "request"), "check\n")
	if out, code := datafs(t, nil, "take", root, "request", "64"); code != 0 || out != "check\n" {
		t.Fatalf("take: %q %d", out, code)
	}
	if _, err := os.Lstat(filepath.Join(root, "request")); !os.IsNotExist(err) {
		t.Fatal("request not removed")
	}
	// A link in place of the request: refused and removed (the link, not
	// its target), so the path unit stops firing.
	if err := os.Symlink(filepath.Join(outside, "request"), filepath.Join(root, "request")); err != nil {
		t.Fatal(err)
	}
	if _, code := datafs(t, nil, "take", root, "request", "64"); code != 1 {
		t.Fatalf("link taken: %d", code)
	}
	if _, err := os.Lstat(filepath.Join(root, "request")); !os.IsNotExist(err) {
		t.Fatal("link left in place")
	}
	if b, _ := os.ReadFile(filepath.Join(outside, "request")); string(b) != "keep me" {
		t.Fatal("the link's target changed")
	}
	if err := syscall.Mkfifo(filepath.Join(root, "request"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, code := datafs(t, nil, "take", root, "request", "64"); code != 1 {
		t.Fatalf("fifo taken: %d", code)
	}
	if _, err := os.Lstat(filepath.Join(root, "request")); !os.IsNotExist(err) {
		t.Fatal("fifo left in place")
	}
	// A directory, a link to one, a socket: refused and gone (a directory
	// with entries is renamed aside, its contents untouched).
	plant := []struct {
		what string
		make func(p string) error
	}{
		{"empty directory", func(p string) error { return os.Mkdir(p, 0o755) }},
		{"directory with entries", func(p string) error {
			if err := os.Mkdir(p, 0o755); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(p, "x"), []byte("x"), 0o600)
		}},
		{"link to a directory", func(p string) error { return os.Symlink(outside, p) }},
		{"socket", func(p string) error { return syscall.Bind(mustSocket(t), &syscall.SockaddrUnix{Name: p}) }},
	}
	for _, c := range plant {
		p := filepath.Join(root, "request")
		if err := c.make(p); err != nil {
			t.Fatalf("%s: %v", c.what, err)
		}
		if _, code := datafs(t, nil, "take", root, "request", "64"); code != 1 {
			t.Errorf("%s taken: %d", c.what, code)
		}
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Errorf("%s left in place", c.what)
		}
	}
	aside, _ := filepath.Glob(filepath.Join(root, ".request.*.refused"))
	if len(aside) != 1 {
		t.Fatalf("renamed aside: %v", aside)
	}
	if b, err := os.ReadFile(filepath.Join(aside[0], "x")); err != nil || string(b) != "x" {
		t.Errorf("the directory's contents changed: %q %v", b, err)
	}
	if b, _ := os.ReadFile(filepath.Join(outside, "request")); string(b) != "keep me" {
		t.Fatal("a linked directory's contents changed")
	}
	// rm removes a directory at the name the same way, never its target.
	if err := os.Symlink(outside, filepath.Join(root, "pending.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "pending-secrets.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, code := datafs(t, nil, "rm", root, "pending.yaml", "pending-secrets.json", "missing"); code != 0 {
		t.Fatalf("rm: %d", code)
	}
	for _, n := range []string{"pending.yaml", "pending-secrets.json"} {
		if _, err := os.Lstat(filepath.Join(root, n)); !os.IsNotExist(err) {
			t.Errorf("rm left %s", n)
		}
	}
	if _, err := os.Stat(filepath.Join(outside, "request")); err != nil {
		t.Fatal("rm removed a file through a link")
	}
}

func mustSocket(t *testing.T) int {
	t.Helper()
	fd, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { syscall.Close(fd) })
	return fd
}

func TestDatafsRemovalsStayInTheTree(t *testing.T) {
	root := t.TempDir()
	victim := t.TempDir()
	write(t, filepath.Join(victim, "request"), "x")
	if err := os.Symlink(victim, filepath.Join(root, "tailscale")); err != nil {
		t.Fatal(err)
	}
	if _, code := datafs(t, nil, "rm", root, root+"/tailscale/request"); code != 1 {
		t.Errorf("rm through a linked directory: status %d", code)
	}
	if _, err := os.Stat(filepath.Join(victim, "request")); err != nil {
		t.Fatal("a file outside the tree was removed")
	}
}

func TestDatafsMkdirTouchWrite(t *testing.T) {
	root := t.TempDir()
	victim := t.TempDir()
	if err := os.Chmod(victim, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, code := datafs(t, nil, "mkdir", root, "0700", root+"/a", root+"/b/c"); code != 0 {
		t.Fatalf("mkdir: %d", code)
	}
	for _, d := range []string{"a", "b", "b/c"} {
		st, err := os.Lstat(filepath.Join(root, d))
		if err != nil || !st.IsDir() || st.Mode().Perm() != 0o700 {
			t.Errorf("%s: %v %v", d, st, err)
		}
	}
	// A link at the name: refused, and its target keeps its mode.
	if err := os.Symlink(victim, filepath.Join(root, "x")); err != nil {
		t.Fatal(err)
	}
	if _, code := datafs(t, nil, "mkdir", root, "0700", root+"/x"); code != 1 {
		t.Errorf("mkdir over a link: %d", code)
	}
	if st, _ := os.Stat(victim); st.Mode().Perm() != 0o755 {
		t.Errorf("the link's target changed mode: %v", st.Mode())
	}

	// touch: creates once, refuses a dangling link and creates nothing at its target.
	if out, code := datafs(t, nil, "touch", root, root+"/.sentinela-volume"); code != 0 || !strings.HasPrefix(out, "created") {
		t.Errorf("touch: %q %d", out, code)
	}
	if out, code := datafs(t, nil, "touch", root, root+"/.sentinela-volume"); code != 0 || out != "" {
		t.Errorf("touch again: %q %d", out, code)
	}
	if err := os.Symlink(filepath.Join(victim, "masked.service"), filepath.Join(root, "marker")); err != nil {
		t.Fatal(err)
	}
	if _, code := datafs(t, nil, "touch", root, root+"/marker"); code != 1 {
		t.Errorf("touch through a dangling link: %d", code)
	}
	if _, err := os.Lstat(filepath.Join(victim, "masked.service")); !os.IsNotExist(err) {
		t.Fatal("touch created a file at the link's target")
	}

	// write: replaces a link at the name instead of writing through it.
	write(t, filepath.Join(victim, "settings.json"), "original")
	if err := os.Symlink(filepath.Join(victim, "settings.json"), filepath.Join(root, "a", "settings.json")); err != nil {
		t.Fatal(err)
	}
	if _, code := datafs(t, []byte(`{"enabled":true}`), "write", root, root+"/a/settings.json", "0644"); code != 0 {
		t.Fatalf("write: %d", code)
	}
	st, err := os.Lstat(filepath.Join(root, "a", "settings.json"))
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm() != 0o644 {
		t.Fatalf("written file: %v %v", st, err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "a", "settings.json")); string(b) != `{"enabled":true}` {
		t.Errorf("content %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(victim, "settings.json")); string(b) != "original" {
		t.Error("written through the link")
	}

	// A FIFO planted at the temporary name write tries first: it takes
	// another name and leaves the FIFO alone.
	planted := filepath.Join(root, "a", ".settings.json.0123.tmp")
	if err := syscall.Mkfifo(planted, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, code := datafsTokens(t, "0123,4567", []byte(`{"at":"03:00"}`), "write", root, root+"/a/settings.json", "0644"); code != 0 {
		t.Fatalf("write past a planted temporary name: %d", code)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "a", "settings.json")); string(b) != `{"at":"03:00"}` {
		t.Errorf("content %q", b)
	}
	if st, err := os.Lstat(planted); err != nil || st.Mode()&os.ModeNamedPipe == 0 {
		t.Errorf("the planted FIFO changed: %v %v", st, err)
	}
	if _, err := os.Lstat(filepath.Join(root, "a", ".settings.json.4567.tmp")); !os.IsNotExist(err) {
		t.Error("temporary file left behind")
	}
}

// mergeScript is the root secrets merge embedded in apply-config.sh.
func mergeScript(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("apply-config.sh")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	start := strings.Index(s, "<<'PY'\n")
	end := strings.Index(s, "\nPY\n")
	if start < 0 || end < start {
		t.Fatal("merge script not found in apply-config.sh")
	}
	return s[start+len("<<'PY'\n") : end+1]
}

func TestApplyConfigSecretsMerge(t *testing.T) {
	py := needPython(t)
	code := mergeScript(t)
	const installed = "# Camera credentials\nCAM1_USER=u\nCAM1_PASS=p\nOLD_PASS=a\vPROBE=1\n"
	cases := []struct {
		name, request, names, want string // want "" = refused
	}{
		{"no request", "", "CAM1_USER CAM1_PASS", installed},
		{"changed password", `{"CAM1_PASS":"new!pass"}`, "CAM1_USER CAM1_PASS",
			"# Camera credentials\nCAM1_USER=u\nOLD_PASS=a\vPROBE=1\nCAM1_PASS=new!pass\n"},
		{"new camera", `{"CAM2_USER":"admin","CAM2_PASS":"Garage55pass"}`, "CAM1_USER CAM1_PASS CAM2_USER CAM2_PASS",
			installed + "CAM2_USER=admin\nCAM2_PASS=Garage55pass\n"},
		{"name no camera uses", `{"EVIL_PASS":"x"}`, "CAM1_USER CAM1_PASS", ""},
		{"used name of another shape", `{"LD_PRELOAD":"/data/recordings/x.so"}`, "LD_PRELOAD CAM1_PASS", ""},
		{"vertical tab starts a variable on the next run", `{"CAM1_PASS":"x\u000bPROBE_VAR=probe"}`, "CAM1_USER CAM1_PASS", ""},
		{"line separator", `{"CAM1_PASS":"x PROBE_VAR=probe"}`, "CAM1_USER CAM1_PASS", ""},
		{"next line", `{"CAM1_PASS":"x\u0085PROBE_VAR=probe"}`, "CAM1_USER CAM1_PASS", ""},
		{"compose interpolation", `{"CAM1_PASS":"${OLD_PASS}"}`, "CAM1_USER CAM1_PASS", ""},
		{"quote", `{"CAM1_PASS":"a\"b"}`, "CAM1_USER CAM1_PASS", ""},
		{"empty", `{"CAM1_PASS":""}`, "CAM1_USER CAM1_PASS", ""},
		{"not an object", `["CAM1_PASS"]`, "CAM1_USER CAM1_PASS", ""},
	}
	for _, c := range cases {
		dir := t.TempDir()
		src, req, names, out := filepath.Join(dir, "secrets.env"), filepath.Join(dir, "request.json"),
			filepath.Join(dir, "names"), filepath.Join(dir, "out.env")
		write(t, src, installed)
		write(t, req, c.request)
		write(t, names, strings.ReplaceAll(c.names, " ", "\n")+"\n")
		err := exec.Command(py, "-I", "-c", code, src, req, names, out).Run()
		got, _ := os.ReadFile(out)
		switch {
		case c.want == "" && err == nil:
			t.Errorf("%s: accepted:\n%q", c.name, got)
		case c.want != "" && err != nil:
			t.Errorf("%s: refused: %v", c.name, err)
		case c.want != "" && string(got) != c.want:
			t.Errorf("%s: wrote\n%q\nwant\n%q", c.name, got, c.want)
		}
	}
}
