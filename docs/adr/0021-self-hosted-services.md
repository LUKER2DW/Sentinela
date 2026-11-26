# ADR-0021: Self-hosted services next to the NVR

Status: Accepted · 2026-10-01 (owner request; replaces the Phase 8 sketch)

Implementation status (2026-10-01): built and tested — unit tests with a
fake Docker engine, and an end-to-end run against a real Docker 29.8
engine (static site from a local folder with `.git`, Node service with a
secret variable, a two-container Compose project, stop/start/restart,
live configuration change, GitHub download, delete). The host firewall
script was exercised on iptables-nft 1.8.7 (Ubuntu 22.04). **Deployed and
enabled on the household server on 2026-10-01**: script, Dockerfile and
Compose HTTP smoke tests passed on Docker 29.8.2, including stop/start/
restart and the camera egress block. Temporary projects were removed.
Actual reboot and access from another Tailscale device remain untested.

## Context
The owner wants Sentinela to also host personal projects — automations,
APIs, bots, test services — deployed from the dashboard: from a GitHub
repository or from a project folder copied to the server (with its
`.git`), with a startup script, environment variables and ports, each
reachable at its own port over the LAN, Tailscale and, when chosen, the
router's public IP; with deploy, restart, stop, logs, update, resource
usage and start after reboot. PLAN Phase 8 had parked the idea behind an
ADR: isolation, caps next to the recorder, and (then) no WAN exposure.

The host can be the minimum one (1 GB RAM; 2 GB with services) and must keep recording
(AGENTS.md §2.2–2.3). The camera is an XMEye device on the same LAN.

## Decision
**One agent, one Docker socket.** A new Compose service, `apps`, runs
`sentinela apps` from the existing image. It is the only Sentinela
process with `/var/run/docker.sock` — root-equivalent — so it has no
network listener except `127.0.0.1:7091`, accepts only loopback requests
with a token (`/data/apps-agent/token`, written by `sentinela serve`), gets
no camera secrets, no recordings, and access to the configured Cloud root
for explicitly selected local project transfers. It runs as uid 1000 in the docker group, read-only root,
no capabilities, 0.5 CPU, 128 MB. The internet-facing API never touches
Docker: `/api/hosting/*` forwards to the agent after the usual session,
role and CSRF checks.

**Who and from where.** Admins only. Creating or changing a service
(which decides what code runs and where it is exposed), the GitHub token,
and source inspection need the LAN or tailnet, like account
administration; from the public listener a 2FA session may only start,
stop, restart, redeploy or check an existing service. Every change is
audited (never request bodies: they carry environment values).

**Sources.** GitHub only through `api.github.com` and its `codeload`
redirect (no git binary, no arbitrary URLs); a fine-grained read-only
token is optional for private repositories, stored 0600 and never
returned. Local folders come from `apps.local_root` (default the Cloud),
read through `os.Root`. As amended by the owner's request on 2026-10-10,
the first deploy copies the selected folder into a private per-service
`project` directory on the system disk, commits ownership, then removes
the original from Cloud. Failure before ownership is saved preserves the
original. The services explorer edits this owned working tree and persistent
`data`, with bounded UTF-8 editing, file/folder creation, upload and deletion;
changes to code require Update. Git internals and agent files are inaccessible
through the explorer; paths and symlinks cannot escape the selected root.
Snapshots use the current working files and skip `.git`, `node_modules`,
`.venv`. Direct ZIP upload accepts ≤ 300 MiB compressed / 1 GiB extracted,
100,000 entries, no traversal, links or duplicate files. One enclosing
folder is stripped; unused uploads expire after 24 hours (at most five
pending projects). GitHub downloads retain their repository update flow.

**Secrets (owner amendment, 2026-10-10).** Real `.env`, `.env.*`, other
`.env`-prefixed names and `*.env` files are parsed as literal dotenv,
including quoted multiline values; example/sample/template files are left
as templates. All imported variables move to an AES-256-GCM vault outside
project and release directories, authenticated for that service. Files are
deleted only after the encrypted vault is saved. Upload staging also uses
an encrypted vault; editor/upload of an env file imports directly without
writing plaintext. The key is a server-only 0600 `secrets.key`, vaults are
0600 `apps/<id>/secrets.enc`; backups must include both. A missing key with
existing vaults fails closed. Legacy manually marked secrets are migrated
out of the state JSON on upgrade; credential-like variable names are
automatically marked secret.

The manager returns names and blank values, supports replacement/addition/
deletion, checks revisions to reject stale edits, and recreates a running
service after saving. Blank values preserve existing entries. Setup and
runtime receive variables through Docker environment, including Compose
interpolation and member environments; rendered Compose files keep variable
references instead of vault values. The vault protects storage in the
agent's state, not a compromised root/Docker account or the service that
legitimately receives its own secrets. Known values (including a bounded
encrypted rotation history) are redacted from dashboard output, deploy
logs and errors; JSON/YAML credential fields and private keys are masked
or refused in the text editor. No values are returned by the secrets API.

**Three ways to run**, suggested by detection (compose file, Dockerfile,
package.json/bun/deno, requirements/pyproject, go.mod, index.html):
- *script*: an allow-listed runtime image (`apps.runtimes`); the release at
  `/app`, persistent `/data`, package caches in `/cache`; setup once per
  deploy in a throwaway container, start on every run, uid 1000 unless
  the owner asks for root (still without capabilities);
- *dockerfile*: the classic builder with the build caps; a Dockerfile that
  needs BuildKit syntax is rebuilt with BuildKit (uncapped, logged, outside the `sn-*` firewall: see Consequences);
- *compose*: `docker compose config` normalizes the file in a throwaway
  container of the Docker CLI image (`apps.compose_image`, pinned by digest;
  agent's uid, socket group, no network); the agent checks every attribute
  against an allow-list (see "Compose allow-list" below) and refuses the
  rest; then rewrites limits, published ports, restart policy, build image
  names and bridge names, and only that file is run.

**Caps that protect recording.** Per service: memory (no swap), CPU
quota, `cpu_shares` 256 (the NVR keeps Docker's 1024), 512 pids,
`oom_score_adj` 500, capped json logs, no capabilities (script and
dockerfile), `no-new-privileges`, an init process. Together: running
services may reserve at most `apps.total_memory_mb` (default 1 GB); builds
and starts are refused under 400 MB free RAM; one deploy at a time;
setup/build ≤ 20 min, 1 core, 1 GB. Everything lives on the system disk
(`/var/lib/sentinela-apps`, Docker's root), never on `/data`.

**Supervision.** No Docker restart policies: the agent restarts crashed
services every 15 s check, pausing 5 min after 5 stops in 10 min. After a
host boot (new `boot_id`), only services marked to start automatically
come back, and none before the host has been up `boot_delay_seconds`
(default 120 s), so the NVR starts first. Compose one-off jobs that exit 0
are left alone. No container on the services' networks (service, setup
step, classic build, Compose `up` or restart) starts before the services
firewall is in place for this boot (2026-11-26): see *Network and
exposure*. Every start re-checks the bind folders for links, also the
restart of a crashed Compose member, which stops the project instead.
Startup validation waits 10 seconds. Deployment errors, last exit code,
OOM/SIGILL explanation, restart count, pause deadline and 40 diagnostic
events persist across agent restarts. A failed single-container update
attempts to restore the previously running release and keeps the failure
visible in the dashboard. Logs remain readable for failed/stopped services,
with separate deploy/output views and download/filter controls. The default
Node runtime is Debian (`node:22-bookworm-slim`), tested with native canvas
on the household CPU without AVX; Alpine remains an explicit option.
`scripts/deploy.sh --services` updates only the API/dashboard and apps
agent, leaving independent recorders and media containers running.

**Network and exposure.** Services join bridges named `sn-*` (`sn-apps`,
or one per Compose network). Ports come from `apps.port_first..last`
(20000–20099). Exposure per service: *local* (127.0.0.1), *lan* (LAN and
tailnet), *tailnet* (bound to the Tailscale address), *public* (the
router forwards the port; needs `apps.public_host`). Because Docker's
published ports bypass ufw, `scripts/apps-firewall.sh` keeps a
`DOCKER-USER` chain: services may reach the internet and DNS but no
private range (camera, router, LAN, tailnet); their ports answer private
addresses, and the internet only on ports the agent lists in
`/var/lib/sentinela-apps/public-ports` (re-applied by a systemd path unit).
Services reaching the host itself meet ufw's default deny. Runs of the
script are serialized; only after an apply has checked that DOCKER-USER
jumps first to the complete chain does it write
`/run/sentinela-apps/firewall-ready` (boot id and the ports it opened; on
tmpfs, mounted read-only into the agent). A failed apply, the unit's stop
(also when Docker stops) and `setup-apps.sh disable` remove it, a failed
boot apply is retried, and the agent asks for a new apply while the marker
is missing. The agent starts nothing without a marker of the current boot,
nor a non-internet service on a port the applied list still opens.
`disable` closes this gate, stops the agent, then the services, and
removes the rules only once no service container runs. Updated units are
installed by running `setup-apps.sh enable` again; the script's lock and
marker also work with the earlier units.

This amends AGENTS.md §2.5 (a second, per-service public exception,
owner-chosen and off by default), §2.8 (a new container), SECURITY.md
rule 6 (one container with the Docker socket) and the threat-model entry
"build pipeline competes with recording" (builds now happen on the
server, capped and serialized).

## Hardening of the agent and the Compose helper (2026-11-17)
Added after a security review; none of it changes the model above.
- **Compose helper environment.** The throwaway `docker compose` container
  holds the Docker socket, so it gets a fixed environment (`PATH`, `HOME`,
  `DOCKER_CONFIG`) plus the service's variables for interpolation, minus
  reserved names (`PATH`, `HOME`, `SHELL`, `IFS`, `ENV`, `BASH_ENV`, `LD_*`,
  `DOCKER_*`, `COMPOSE_*` except `COMPOSE_PROFILES`, `BUILDKIT_*`, `BUILDX_*`,
  `XDG_*` and a few more). It runs `/usr/local/bin/docker` by absolute path.
  The names skipped are logged (never values); the service itself still
  receives every variable. Names are not refused at import: a legitimate
  project may carry `PATH` or `LD_LIBRARY_PATH` for its own use.
- **Rendered Compose file.** The file the agent validated and then runs is
  written to `<state>/compose/<id>/rendered.json`, outside the service's
  folder (the only place a bind may come from) and mounted read-only into the
  helper, so no service can rewrite it between validation and `up`.
- **Bind sources.** Missing bind folders are created by the agent as real
  folders before `up`, so the daemon no longer creates or resolves a path
  that a service could still turn into a link; links are re-checked right
  before `up`. The race this left (a service with write access to a parent
  folder replacing a deeper bind folder with a link before the daemon
  mounts it) is closed by the nesting rule of the 2026-11-26 allow-list.
- **Agent memory.** Source files read for detection are limited to 1 MiB,
  opened through `os.Root` without following links; `.git` files, listings
  of the local picker and of the service explorer, and the disk-usage walk
  of a service's folder read in bounded batches with entry caps. The first
  disk walk waits two minutes after start, and a folder too big to walk is
  reported as a lower bound (`disk_partial`). At most two source
  inspections run at once, and their temporary folders are removed at start.
  FIFOs planted in a service's `data` are not opened blocking. Since
  2026-11-26 the disk walk opens each folder relative to its parent
  (`openat`, no links, non-blocking, folders only) and holds one batch of
  names per level, at most 48 levels; local and imported project folders
  are walked in batches with the entry cap enforced before a listing is
  held, and copied files are checked and bounded once open. Container
  output is cut at 4 KiB per line, a logs answer at 2 MiB, and the Compose
  helper's output at 4 MiB.

## Compose allow-list (2026-11-26)
Added after a second security review. The normalized file is checked
against an allow-list, so an attribute a later Compose release adds is
refused until reviewed; an unknown attribute left empty (`null`, `false`,
`""`, `[]`, `{}`, as `config` prints unset ones) is dropped, and a value of a
type the check does not expect (a mapping for `network_mode`, a volume
without `type`, a string for `external`) is refused. Extensions (`x-*`) are
accepted and dropped from the rendered file: Compose reads some of them.
- **Service attributes accepted as written:** `attach`, `command`,
  `entrypoint`, `environment`, `configs`/`secrets` references, `cap_drop`,
  `cpuset`, `depends_on`, `dns`, `dns_opt`, `dns_search`, `domainname`,
  `hostname`, `expose`, `extra_hosts`, `group_add`, `healthcheck`, `image`,
  `init`, `links`, `mem_reservation`, `mem_swappiness`, `platform`,
  `pull_policy`, `read_only`, `shm_size`, `stdin_open`, `tty`,
  `stop_grace_period`, `stop_signal`, `tmpfs`, `user`, `working_dir`.
  Replaced or dropped by the agent: the limits (`mem_limit`, `memswap_limit`,
  `cpus`, `cpu_shares`, the `cpu_*` quota keys, `pids_limit`,
  `oom_score_adj`), `restart`, `logging`, `profiles` (the file holds only
  the active services), `develop`, and `container_name` (a host-wide name;
  kept as an alias on the project's networks).
- **Accepted with limits:** `cap_add` (Docker's default set),
  `security_opt` (`no-new-privileges` only), `sysctls` (`net.*`), `ulimits`
  (not `rtprio`, `rttime` or `nice`, which bypass the CPU quota), `ipc`
  (`private`, `shareable`), `cgroup` (`private`), `network_mode` (`none`, or
  `service:` of the same project), service `networks` (aliases, priority),
  `labels` (not `com.docker.compose.*` or `sentinela.*`), `volumes` (named,
  tmpfs, and binds as below; no shared propagation), `volumes_from`
  (services, not containers), `env_file` (files of the release),
  `post_start`/`pre_stop` (never `privileged`), `deploy` and `scale` (one
  replica), `build` (below).
- **Refused**, among the rest: `privileged`, `use_api_socket`, `provider`
  services, devices and GPUs, host or container namespaces, `runtime`,
  `annotations`, `blkio_config`, real-time CPU, `oom_kill_disable`,
  `storage_opt`, `volume_driver`, `external_links`, fixed addresses and MACs.
- **Binds** come only from the release being run or the service's `data`
  folder, never from the service folder itself, where the agent keeps its
  vault, diagnostics, logs, releases and the protected project. A writable
  bind may not contain another bind or secret file of the project, nor one
  that containers still running from the previous `up` mount (recorded in
  `<state>/compose/<id>/binds.json`): a container could swap the inner path
  for a link before the daemon mounts it. Mount such folders side by side,
  or the outer one read-only; to move a bind below one that runs, stop the
  service first.
- **Networks and volumes** belong to the project: a `name` must start with
  `sapp-<id>_`, so no other service's volume or network is reachable, and
  `external` is refused. Networks are IPv4 bridges with the engine's own
  subnets: no IPAM, no driver options but the MTU, no IPv6 (the firewall has
  IPv4 rules only); the agent writes `enable_ipv6: false` on each.
- **Builds** take their context and Dockerfile from the release; no `tags`,
  SSH, extra contexts, cache import/export, entitlements or privileged
  builds; `network` only `default` or `none`. The image a build produces is
  always `sentinela-app/<id>:compose-<service>`, never pulled (a service
  that runs another's image, however it spells the reference, follows the
  rename), so no build can retag the helper image, `sentinela:local` or
  another service's image. A deploy removes the images of services its
  release no longer builds; deleting the service removes them all.
- **Memory budget:** the check before `up` counts one container per service
  of the plan being started, also on a first deploy.
- **Helper:** `apps.compose_image` defaults to the CLI image pinned by digest
  (bumped with Sentinela releases). `config` runs in the release with the
  service's variables, reading an input file the agent writes to
  `<state>/compose/<id>` (never into the release, which a service may bind
  writable), with the Compose file's folder as the project directory; `up`
  and `down` run with
  `<state>/compose/<id>` as the project directory, and every run gets an
  empty env file of the agent (`--env-file`), so no `.env` a service writes
  is read. Manager values in the rendered file are references to
  agent-named variables (`SENTINELA_VAR_<hash>`); a top-level secret or
  config read from a variable (`environment: NAME`) reads
  `SENTINELA_OBJ_<hash>`, which `up` gets with the service's value of
  `NAME`. These are the only variables `up` gets, so services also receive
  variables with reserved names. The helper mounts the release and `data`
  read-only, not the whole service folder.
- **Builds outside the firewall need consent.** A Compose project with
  `build:`, or a Dockerfile that needs BuildKit, deploys only when the
  service's *Allow builds outside the firewall* setting (`unfiltered_builds`)
  is on; otherwise the deploy stops before anything is built. The setting is
  off for existing services: the next deploy of one that builds asks for it.
- **Services deployed before these rules.** Their first `up` after the
  update (`--no-build`) recreates their containers and networks once. A
  build's image is given its new name from the image ID a container of the
  project runs (never from what an old name points to now), so no rebuild
  and no consent is needed. A project the agent cannot start this way (the
  current checks refuse its file, or no container runs a built image) is
  stopped with its containers, marked for a deploy, and the reason is shown
  on the service and in its events; the same applies whenever containers
  that no `up` of the running agent accepted would keep running.

## Alternatives considered
- *Docker socket in the API container*: refused — the internet-facing
  process would become root-equivalent.
- *Host systemd service with the docker CLI*: works, but adds a second
  deployment path for one binary; the Compose service keeps
  `deploy.sh` as the only deploy and the image as the only artifact.
- *Rootless Docker/Podman*: smaller blast radius, but a second container
  engine on a 4 GB host and a different networking model; revisit if
  services ever come from untrusted people.
- *Git push to the server (post-receive hook)*: no dashboard flow, needs
  SSH accounts; the local-folder source covers "deploy what is on my PC".

## Consequences
+ Personal projects run next to the NVR with one click, survive reboots,
  and cannot take the recorder's memory or CPU share, reach the camera, or
  be reached from the internet unless the owner marks them public and
  forwards the port.
− The agent is root-equivalent; its safety rests on loopback + token, the
  API's admin/zone checks, and Compose validation. A public service has no
  Sentinela login in front: it must protect itself.
− Builds use the server's CPU (capped at one core, one at a time) and
  images use the system disk; BuildKit-only Dockerfiles are not capped.
− **Build steps are not behind the egress firewall everywhere.** The host
  rules in `scripts/apps-firewall.sh` match only the `sn-*` bridges. The
  classic builder runs on `sn-apps`, but BuildKit (the retry of a Dockerfile
  that needs it) and the `build:` of a Compose project run on Docker's own
  network: the Engine API accepts no custom network for BuildKit, and a
  `build.network` of `none` would break builds that download packages. A
  `RUN` step from a hostile repository could therefore reach private
  addresses (camera, router, LAN) while it builds; runtime containers still
  cannot. The agent cannot close this itself: such a build needs the
  owner's consent per service (`unfiltered_builds`, off by default; without
  it the deploy is refused before building), and the agent marks the
  deployment (`build_outside_firewall`), logs a warning and records a
  service event. In every build mode, the classic one included, the Docker
  daemon also fetches `ADD <url>` sources itself, on the host network, out
  of reach of the `sn-*` rules, so a Dockerfile whose `ADD` names a URL, a
  git source or a variable needs the same consent. An `ONBUILD ADD` trigger
  of a base image is not seen before the build: deploy only sources you
  trust to services that build.
− Runtime images are referenced by tag (pin digests in `apps.runtimes` if
  wanted); the Compose CLI image is pinned by digest.
− Tailnet access needs the ACL to allow `tag:nvr:20000-20099` for the
  devices that should open services.
