package apps

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// composeNames are the file names `docker compose` looks for, in order.
var composeNames = []string{"compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml"}

// Detection is what a source looks like and how to run it. The dashboard
// pre-fills the new-service form with it; everything stays editable.
type Detection struct {
	Mode        string           `json:"mode"`
	Runtime     string           `json:"runtime,omitempty"`
	Setup       string           `json:"setup,omitempty"`
	Start       string           `json:"start,omitempty"`
	Port        int              `json:"port,omitempty"`
	ComposeFile string           `json:"compose_file,omitempty"`
	Services    []ComposeService `json:"services,omitempty"`
	// Found lists the files the guess is based on; Notes are warnings.
	Found   []string `json:"found"`
	Notes   []string `json:"notes,omitempty"`
	Version *Version `json:"version,omitempty"`
}

// ComposeService summarizes one service of a Compose file.
type ComposeService struct {
	Name  string `json:"name"`
	Image string `json:"image,omitempty"`
	Build bool   `json:"build,omitempty"`
	Ports []int  `json:"ports,omitempty"` // container ports published
}

// markers names the kinds of project files among names (folder picker).
func markers(names []string) []string {
	has := func(n ...string) bool {
		return slices.ContainsFunc(n, func(x string) bool { return slices.Contains(names, x) })
	}
	var out []string
	if has(composeNames...) {
		out = append(out, "compose")
	}
	if has("Dockerfile") {
		out = append(out, "dockerfile")
	}
	switch {
	case has("bun.lockb", "bun.lock"):
		out = append(out, "bun")
	case has("deno.json", "deno.jsonc"):
		out = append(out, "deno")
	case has("package.json"):
		out = append(out, "node")
	}
	if has("requirements.txt", "pyproject.toml", "Pipfile") {
		out = append(out, "python")
	}
	if has("go.mod") {
		out = append(out, "go")
	}
	if len(out) == 0 && has("index.html") {
		out = append(out, "static")
	}
	return out
}

// Detect inspects dir (a snapshot of the source, at its subfolder).
func Detect(dir string) Detection {
	entries, _ := os.ReadDir(dir)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	has := func(n string) bool { return slices.Contains(names, n) }
	read := func(n string) string {
		b, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil || len(b) > 1<<20 {
			return ""
		}
		return string(b)
	}
	d := Detection{Found: []string{}}
	for _, n := range composeNames {
		if has(n) {
			d.Mode, d.ComposeFile = ModeCompose, n
			d.Found = append(d.Found, n)
			d.Services, d.Notes = composeSummary([]byte(read(n)))
			return d
		}
	}
	if has("Dockerfile") {
		d.Mode, d.Found = ModeDockerfile, []string{"Dockerfile"}
		d.Port = exposedPort(read("Dockerfile"))
		return d
	}
	d.Mode = ModeScript
	switch {
	case has("bun.lockb") || has("bun.lock"):
		d.Runtime, d.Port = "bun", 3000
		d.Found = append(d.Found, "package.json")
		d.Setup = "bun install"
		d.Start = "bun run start"
		if pkgScripts(read("package.json"))["start"] == "" {
			d.Start = "bun run " + firstExisting(names, "index.ts", "src/index.ts", "index.js", "server.ts")
		}
	case has("deno.json") || has("deno.jsonc"):
		d.Runtime, d.Port = "deno", 8000
		d.Found = append(d.Found, "deno.json")
		d.Start = "deno run -A " + firstExisting(names, "main.ts", "mod.ts", "server.ts")
		if strings.Contains(read("deno.json")+read("deno.jsonc"), `"start"`) {
			d.Start = "deno task start"
		}
	case has("package.json"):
		d.Runtime, d.Port = "node", 3000
		d.Found = append(d.Found, "package.json")
		scripts := pkgScripts(read("package.json"))
		switch {
		case has("pnpm-lock.yaml"):
			d.Setup = "npx -y pnpm install --frozen-lockfile"
		case has("yarn.lock"):
			d.Setup = "npx -y yarn@1 install --frozen-lockfile"
		case has("package-lock.json"):
			d.Setup = "npm ci"
		default:
			d.Setup = "npm install"
		}
		if scripts["build"] != "" {
			d.Setup += " && npm run build"
		}
		d.Start = "npm start"
		if scripts["start"] == "" {
			d.Start = "node " + firstExisting(names, "index.js", "server.js", "app.js", "main.js")
		}
	case has("requirements.txt") || has("pyproject.toml") || has("Pipfile"):
		d.Runtime, d.Port = "python", 8000
		deps := strings.ToLower(read("requirements.txt") + read("pyproject.toml") + read("Pipfile"))
		venv := "python -m venv .venv && .venv/bin/pip install --upgrade pip && "
		if has("requirements.txt") {
			d.Found = append(d.Found, "requirements.txt")
			d.Setup = venv + ".venv/bin/pip install -r requirements.txt"
		} else {
			d.Found = append(d.Found, "pyproject.toml")
			d.Setup = venv + ".venv/bin/pip install ."
		}
		main := firstExisting(names, "main.py", "app.py", "bot.py", "server.py", "run.py")
		mod := strings.TrimSuffix(main, ".py")
		switch {
		case has("manage.py"):
			d.Start = ".venv/bin/python manage.py runserver 0.0.0.0:$PORT"
		case strings.Contains(deps, "uvicorn") || strings.Contains(deps, "fastapi"):
			d.Start = ".venv/bin/uvicorn " + mod + ":app --host 0.0.0.0 --port $PORT"
		case strings.Contains(deps, "flask"):
			d.Start = ".venv/bin/flask --app " + mod + " run --host 0.0.0.0 --port $PORT"
		case strings.Contains(deps, "streamlit"):
			d.Start = ".venv/bin/streamlit run " + main + " --server.address 0.0.0.0 --server.port $PORT"
		default:
			d.Start = ".venv/bin/python " + main
		}
	case has("go.mod"):
		d.Runtime, d.Port = "go", 8080
		d.Found = append(d.Found, "go.mod")
		d.Setup = "go build -o .bin/app ."
		d.Start = "./.bin/app"
	case has("index.html"):
		d.Runtime, d.Port = "static", 8080
		d.Found = append(d.Found, "index.html")
	default:
		d.Runtime = "shell"
		d.Notes = append(d.Notes, "Não reconheci o tipo do projeto: escolha o ambiente e os comandos.")
	}
	return d
}

func firstExisting(names []string, candidates ...string) string {
	for _, c := range candidates {
		if slices.Contains(names, c) {
			return c
		}
	}
	return candidates[0]
}

func pkgScripts(pkg string) map[string]string {
	var p struct {
		Scripts map[string]string `json:"scripts"`
	}
	_ = json.Unmarshal([]byte(pkg), &p)
	if p.Scripts == nil {
		p.Scripts = map[string]string{}
	}
	return p.Scripts
}

var exposeLine = regexp.MustCompile(`(?im)^\s*EXPOSE\s+(\d+)`)

func exposedPort(dockerfile string) int {
	if m := exposeLine.FindStringSubmatch(dockerfile); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	return 0
}

// composeSummary lists a Compose file's services and published ports, and
// warns about settings the platform will refuse (the full check runs on
// the normalized file at deploy).
func composeSummary(raw []byte) ([]ComposeService, []string) {
	var doc struct {
		Services map[string]map[string]any `yaml:"services"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, []string{"O arquivo Compose não pôde ser lido: " + err.Error()}
	}
	var out []ComposeService
	var notes []string
	for name, s := range doc.Services {
		cs := ComposeService{Name: name}
		cs.Image, _ = s["image"].(string)
		_, cs.Build = s["build"]
		if ps, ok := s["ports"].([]any); ok {
			for _, p := range ps {
				if n := shortPortTarget(p); n > 0 {
					cs.Ports = append(cs.Ports, n)
				}
			}
		}
		if v, _ := s["privileged"].(bool); v {
			notes = append(notes, fmt.Sprintf("%s: privileged não é permitido.", name))
		}
		if nm, _ := s["network_mode"].(string); nm == "host" {
			notes = append(notes, fmt.Sprintf("%s: network_mode host não é permitido.", name))
		}
		if vols, ok := s["volumes"].([]any); ok {
			for _, v := range vols {
				if str, ok := v.(string); ok && strings.Contains(str, "docker.sock") {
					notes = append(notes, fmt.Sprintf("%s: o socket do Docker não pode ser montado.", name))
				}
			}
		}
		out = append(out, cs)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	sort.Strings(notes)
	return out, notes
}

// shortPortTarget is the container port of a published port entry
// ("8080:80", "127.0.0.1:8080:80/tcp", {target: 80, published: 8080}).
func shortPortTarget(p any) int {
	switch v := p.(type) {
	case string:
		v, _, _ = strings.Cut(v, "/")
		parts := strings.Split(v, ":")
		if len(parts) < 2 {
			return 0 // container port only: not published on a fixed port
		}
		n, _ := strconv.Atoi(parts[len(parts)-1])
		return n
	case int:
		return 0
	case map[string]any:
		if _, ok := v["published"]; !ok {
			return 0
		}
		switch t := v["target"].(type) {
		case int:
			return t
		case string:
			n, _ := strconv.Atoi(t)
			return n
		}
	}
	return 0
}

// detectInSnapshot runs Detect on dir/sub, reporting a missing subfolder.
func detectInSnapshot(dir, sub string) (Detection, error) {
	work := dir
	if sub != "" {
		work = filepath.Join(dir, filepath.FromSlash(sub))
	}
	if st, err := os.Stat(work); err != nil || !st.IsDir() {
		return Detection{}, fmt.Errorf("a pasta %q não existe na fonte", sub)
	}
	return Detect(work), nil
}
