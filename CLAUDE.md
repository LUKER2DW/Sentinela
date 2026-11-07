# CLAUDE.md

@AGENTS.md

## Claude-specific notes

- Read `AGENTS.md` (imported above) and, when present, the current phase in `docs/PLAN.md`
  before starting any task.
- Camera facts live in `docs/CAMERA.md`. If a task needs a camera behaviour
  that is not documented there, **stop and ask** — do not guess DVRIP
  commands or config field names.
- Cameras are on the operator's LAN (addresses in the server's
  `/etc/sentinela/config.yaml`, never in the repository). Run read-only probes
  only from an authorized LAN PC or server; never
  move PTZ, change settings or play audio without explicit confirmation in
  the same conversation.
- Never ask the user to paste the camera password into chat. Scripts read it
  with `read -s` or from `SENTINELA_CAMERA_PASSWORD` in a local `.env`.
- Prefer editing the plan in place over creating new markdown files.
- The APK is generic (pairing codes carry address and pin). A household
  default server, if wanted, belongs in `~/.sentinela-android/connection.json`;
  follow the release rules in AGENTS.md.
- The user writes in Portuguese; code, comments, commits and docs are in
  English.
