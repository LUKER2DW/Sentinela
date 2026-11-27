package apps

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// Image pulls are made by the Docker daemon on the host's own network,
// outside the sn-* firewall: a registry named in an image reference (or the
// token service it points the daemon to) could be a camera, the router or a
// service on this server. Images from the well-known public registries,
// whose token services are their own, pull freely; any other registry needs
// the same consent as a build outside the firewall (App.UnfilteredBuilds).
var knownRegistries = setOf(
	"docker.io", "index.docker.io", "registry-1.docker.io",
	"ghcr.io", "quay.io", "lscr.io", "registry.gitlab.com",
	"gcr.io", "us.gcr.io", "eu.gcr.io", "asia.gcr.io", "registry.k8s.io",
	"mcr.microsoft.com", "public.ecr.aws",
)

// imageRegistry is the registry an image reference pulls from, by Docker's
// rule: a first component with a "." or ":", or "localhost", names the
// registry; anything else is Docker Hub.
func imageRegistry(ref string) string {
	first, _, ok := strings.Cut(ref, "/")
	if !ok || !strings.ContainsAny(first, ".:") && first != "localhost" {
		return "docker.io"
	}
	return strings.ToLower(first)
}

// foreignRegistry reports whether pulling ref may reach a host other than
// a well-known public registry. A variable before the first "/" counts: it
// could expand to a registry ("node:$V" with V=5000/x names host "node").
func foreignRegistry(ref string) bool {
	ref = strings.TrimSpace(ref)
	if first, _, _ := strings.Cut(ref, "/"); strings.Contains(first, "$") {
		return true
	}
	return !knownRegistries[imageRegistry(ref)]
}

// foreignPulls lists the services of the plan whose `up` may pull their
// image from another registry: a pull policy that pulls again, or an image
// that is not on the server yet. Images the project builds, and services
// with `pull_policy: never` or `build`, never pull.
func (p *composePlan) foreignPulls(ctx context.Context, exists func(context.Context, string) (bool, error)) ([]string, error) {
	services, _ := p.doc["services"].(map[string]any)
	var out []string
	for _, name := range p.services {
		if _, built := p.images[name]; built {
			continue
		}
		s, _ := services[name].(map[string]any)
		img, _ := s["image"].(string)
		policy, _ := s["pull_policy"].(string)
		if img == "" || !foreignRegistry(img) {
			continue
		}
		switch strings.ToLower(policy) {
		case "never", "build":
			continue
		case "", "missing", "if_not_present":
			ok, err := exists(ctx, img)
			if err != nil {
				return nil, err
			}
			if ok {
				continue
			}
		}
		out = append(out, fmt.Sprintf("%s (%s)", name, img))
	}
	return out, nil
}

// dockerfileRegistries lists the FROM images of a Dockerfile that come from
// another registry (the classic builder pulls them on the host's network).
// The defaults of ARGs declared before the first FROM are substituted (the
// agent passes no build arguments); build stages named earlier are not
// images; flags such as --platform are skipped.
func dockerfileRegistries(dockerfile string) []string {
	stages := map[string]bool{}
	global := map[string]string{}
	seenFrom := false
	var out []string
	for _, f := range dockerfileInstructions(dockerfile) {
		if len(f) > 0 && strings.EqualFold(f[0], "ONBUILD") {
			f = f[1:]
		}
		if len(f) >= 2 && strings.EqualFold(f[0], "ARG") && !seenFrom {
			for _, a := range f[1:] {
				if k, v, ok := strings.Cut(a, "="); ok {
					global[k] = strings.Trim(v, `"'`)
				}
			}
			continue
		}
		if len(f) < 2 || !strings.EqualFold(f[0], "FROM") {
			continue
		}
		seenFrom = true
		args := f[1:]
		for len(args) > 0 && strings.HasPrefix(args[0], "--") {
			args = args[1:]
		}
		if len(args) == 0 {
			continue
		}
		img := os.Expand(args[0], func(k string) string {
			if v, ok := global[k]; ok {
				return v
			}
			return "$" + k // unknown: stays a variable
		})
		if len(args) >= 3 && strings.EqualFold(args[1], "AS") {
			stages[strings.ToLower(args[2])] = true
		}
		if stages[strings.ToLower(img)] && !strings.Contains(img, "/") || strings.EqualFold(img, "scratch") {
			continue
		}
		if foreignRegistry(img) {
			if strings.Contains(img, "$") {
				img = args[0] // as written
			}
			out = append(out, img)
		}
	}
	return out
}

// dockerfileInstructions splits a Dockerfile into instructions (fields),
// joining continuation lines and honouring the escape directive.
func dockerfileInstructions(dockerfile string) [][]string {
	lines := strings.Split(strings.ReplaceAll(dockerfile, "\r\n", "\n"), "\n")
	escape := `\`
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if !strings.HasPrefix(t, "#") {
			break // parser directives come first
		}
		d := strings.ReplaceAll(strings.ToLower(t[1:]), " ", "")
		if v, ok := strings.CutPrefix(d, "escape="); ok && (v == "`" || v == `\`) {
			escape = v
		}
	}
	var out [][]string
	var inst strings.Builder
	flush := func() {
		if f := strings.Fields(inst.String()); len(f) > 0 {
			out = append(out, f)
		}
		inst.Reset()
	}
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if inst.Len() == 0 && (t == "" || strings.HasPrefix(t, "#")) {
			continue
		}
		if s, ok := strings.CutSuffix(t, escape); ok {
			inst.WriteString(s + " ")
			continue
		}
		inst.WriteString(t)
		flush()
	}
	flush()
	return out
}

// errForeignPull refuses a pull from another registry without consent.
func errForeignPull(what []string) error {
	return fmt.Errorf("%s would be downloaded by Docker on this server's own network, outside the services firewall, from a registry other than the well-known public ones (Docker Hub, GitHub, Quay, LinuxServer, GitLab, Google, Microsoft, Amazon): it could reach the cameras, the home network and this server. Turn on \"Allow builds and downloads outside the firewall\" in the service's settings only if you trust it",
		strings.Join(what, ", "))
}
