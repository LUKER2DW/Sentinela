package apps

import (
	"errors"
	"io/fs"
	"testing"
)

// Docker copies an image's labels onto its containers, so a Dockerfile can
// give its container another project's Compose labels. Only a container
// that also carries the project's own sentinela.app label is a member.
func TestProjectContainersNeedTheServiceLabel(t *testing.T) {
	a, eng, _ := newTestAgent(t)
	eng.mu.Lock()
	eng.members = []Summary{
		{Name: "sapp-site-web-1", App: "site", Service: "web", Running: true},
		{Name: "sapp-evil", App: "evil", Service: "web", Running: true}, // LABEL com.docker.compose.project=sapp-site
		{Name: "unlabelled", Service: "web", Running: true},
	}
	eng.mu.Unlock()
	cs, err := a.projectContainers(t.Context(), "site")
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 1 || cs[0].Name != "sapp-site-web-1" {
		t.Fatalf("members = %+v", cs)
	}
}

func TestAddFetches(t *testing.T) {
	for _, tc := range []struct {
		name, dockerfile string
		want             bool
	}{
		{"copy only", "FROM alpine\nCOPY . /app\nADD app.tar.gz /app\n", false},
		{"url", "FROM alpine\nADD http://127.0.0.1:1984/api/frame.jpeg /x\n", true},
		{"lower case", "from alpine\nadd https://example.com/x /x\n", true},
		{"flags", "FROM alpine\nADD --chown=1:1 --checksum=sha256:00 https://example.com/x /x\n", true},
		{"json form", "FROM alpine\nADD [\"https://example.com/x\", \"/x\"]\n", true},
		{"continuation", "FROM alpine\nADD \\\n  http://192.168.1.1/ \\\n  /x\n", true},
		{"variable", "FROM alpine\nARG SRC\nADD $SRC /x\n", true},
		{"git", "FROM alpine\nADD git@example.com:a/b.git /src\n", true},
		{"onbuild", "FROM alpine\nONBUILD ADD http://example.com/x /x\n", true},
		{"escape directive", "# escape=`\nFROM alpine\nADD `\n  http://example.com/x /x\n", true},
		{"comment", "FROM alpine\n# ADD http://example.com/x /x\nRUN true\n", false},
		{"crlf", "FROM alpine\r\nADD http://example.com/x /x\r\n", true},
		{"url elsewhere", "FROM alpine\nRUN echo http://example.com\nLABEL url=https://example.com\n", false},
	} {
		if got := addFetches(tc.dockerfile); got != tc.want {
			t.Errorf("%s: addFetches = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// A service may not run, by name, an image the agent built for a service:
// only the project's own builds use sentinela-app/ names.
func TestForeignBuildsRefused(t *testing.T) {
	plan := func(images map[string]string, svcImage string) *composePlan {
		return &composePlan{
			doc:      map[string]any{"services": map[string]any{"web": map[string]any{"image": svcImage}}},
			services: []string{"web"},
			images:   images,
		}
	}
	for _, tc := range []struct {
		name   string
		p      *composePlan
		refuse bool
	}{
		{"public image", plan(nil, "nginx:1.27"), false},
		{"own build", plan(map[string]string{"web": "sentinela-app/site:compose-web"}, "myapp"), false},
		{"another service's build", plan(nil, "sentinela-app/other:compose-web"), true},
		{"spelled in full", plan(nil, "docker.io/sentinela-app/other:compose-web"), true},
		{"a Dockerfile service's image", plan(nil, "sentinela-app/other:abc123"), true},
	} {
		err := tc.p.foreignBuilds("site")
		if (err != nil) != tc.refuse {
			t.Errorf("%s: err = %v, want refused=%v", tc.name, err, tc.refuse)
		}
	}
}

// A system error (a full disk, a failed stat) is retried later; only a
// refusal stops the project until the next deploy.
func TestBlockedOnlyForRefusals(t *testing.T) {
	var be *blockedError
	sys := &fs.PathError{Op: "mkdir", Path: "/x", Err: errors.New("no space left on device")}
	if errors.As(blocked(sys), &be) {
		t.Fatal("a system error was marked blocked")
	}
	if !errors.As(blocked(errors.New(`folder "x" goes through a symbolic link`)), &be) {
		t.Fatal("a refusal was not marked blocked")
	}
}
