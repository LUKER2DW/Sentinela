package config

import (
	"strings"
	"testing"
)

const installedCfg = `
listen: ":8080"
timezone: UTC
recording: {segment_seconds: 300}
remote:
  tailnet_url: https://sentinela.example.ts.net
  funnel_listen: 127.0.0.1:8081
  public: {domain: cam.example.com, listen: ":7443"}
apps:
  enabled: true
  compose_image: docker:29-cli
  runtimes: {node: "node:22-bookworm-slim"}
app_updates: {github: owner/repo}
volumes:
  - {id: nuvem, name: Cloud, path: /srv/nas, kind: nas}
  - {id: rec, name: Recordings, path: /data, kind: recordings}
cameras:
  - {id: a, host: 10.0.0.2, user_env: A_USER, password_env: A_PASS}
`

func TestCheckDashboardChange(t *testing.T) {
	rep := func(old, new string) func(string) string {
		return func(s string) string { return strings.Replace(s, old, new, 1) }
	}
	cases := []struct {
		name    string
		mutate  func(string) string
		wantErr string // "" = accepted
	}{
		{"same file", func(s string) string { return s }, ""},
		{"timezone, recording and cameras are editable", func(s string) string {
			s = strings.Replace(s, "timezone: UTC", "timezone: America/Sao_Paulo", 1)
			s = strings.Replace(s, "segment_seconds: 300", "segment_seconds: 600, max_disk_percent: 90", 1)
			return s + "  - {id: b, host: 10.0.0.3, user_env: B_USER, password_env: B_PASS}\n"
		}, ""},
		{"public port moves", rep(":7443", ":18443"), ""},
		{"apps compose image", rep("docker:29-cli", "evil/img:1"), "apps cannot be changed"},
		{"apps runtime", rep("node:22-bookworm-slim", "evil/img:1"), "apps cannot be changed"},
		{"apps switched off", rep("enabled: true", "enabled: false"), "apps cannot be changed"},
		{"update source", rep("owner/repo", "evil/repo"), "app_updates cannot be changed"},
		{"main listen", rep(`listen: ":8080"`, `listen: ":80"`), "listen cannot be changed"},
		{"data dir", func(s string) string { return "data_dir: /etc\n" + s }, "data_dir cannot be changed"},
		{"public port below 1024", rep(":7443", ":22"), "between 1024 and 65535"},
		{"public port too high", rep(":7443", ":70000"), "between 1024 and 65535"},
		{"public port not a number", rep(":7443", ":abc"), "between 1024 and 65535"},
		{"public port is go2rtc's API", rep(":7443", ":1984"), "another part of Sentinela"},
		{"public port is the dashboard", rep(":7443", ":8080"), "dashboard's own port"},
		{"public port in services range", rep(":7443", ":20010"), "services range"},
		{"http01 below 1024", rep(`listen: ":7443"`, `listen: ":7443", http01_listen: ":80"`), "http01_listen: the port"},
		{"tunnel port below 1024", rep("public:", "proxy: {url: https://x.example.com, listen: \"127.0.0.1:443\", kind: other}\n  public:"),
			"remote.proxy.listen: the port"},
		{"funnel moved", rep("127.0.0.1:8081", "127.0.0.1:9000"), "funnel_listen"},
		{"nas volume moved into /data", rep("path: /srv/nas", "path: /data/config"), "a nas volume must be"},
		{"nas volume escapes with dots", rep("path: /srv/nas", "path: /srv/nas/../../etc"), "a nas volume must be"},
		{"nas volume inside the root", rep("path: /srv/nas", "path: /srv/nas/shared"), ""},
	}
	for _, c := range cases {
		err := CheckDashboardChange([]byte(installedCfg), []byte(c.mutate(installedCfg)))
		switch {
		case c.wantErr == "" && err != nil:
			t.Errorf("%s: refused: %v", c.name, err)
		case c.wantErr != "" && err == nil:
			t.Errorf("%s: accepted", c.name)
		case c.wantErr != "" && !strings.Contains(err.Error(), c.wantErr):
			t.Errorf("%s: error %q lacks %q", c.name, err, c.wantErr)
		}
	}
}

func TestCheckDashboardChangeFromABareConfig(t *testing.T) {
	const bare = "timezone: UTC\n"
	if err := CheckDashboardChange([]byte(bare), []byte(bare+"apps: {enabled: true}\n")); err == nil {
		t.Error("apps section added through the dashboard path")
	}
	if err := CheckDashboardChange([]byte(bare), []byte(bare+"volumes:\n  - {id: n, path: /mnt/x, kind: nas}\n")); err == nil {
		t.Error("new nas volume outside /srv/nas accepted")
	}
	if err := CheckDashboardChange([]byte(bare), []byte(bare+"remote:\n  funnel_listen: 127.0.0.1:8081\n")); err != nil {
		t.Errorf("funnel default refused: %v", err)
	}
	if err := CheckDashboardChange([]byte(bare), []byte("{ not yaml")); err == nil {
		t.Error("garbage accepted")
	}
}
