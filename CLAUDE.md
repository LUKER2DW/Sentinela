# CLAUDE.md

@AGENTS.md

## Claude-specific notes

- Read `AGENTS.md` (imported above) and the current phase in `docs/PLAN.md`
  before starting any task.
- Camera facts live in `docs/CAMERA.md`. If a task needs a camera behaviour
  that is not documented there, **stop and ask** — do not guess DVRIP
  commands or config field names.
- The camera (`192.168.1.20`) is on the home LAN. Run read-only probes
  only from an authorized LAN PC or server; never
  move PTZ, change settings or play audio without explicit confirmation in
  the same conversation.
- Never ask the user to paste the camera password into chat. Scripts read it
  with `read -s` or from `SENTINELA_CAMERA_PASSWORD` in a local `.env`.
- Prefer editing the plan in place over creating new markdown files.
- Android build defaults belong in `~/.sentinela-android/connection.json`.
  Do not depend on a temporary shell session to supply the certificate pin;
  follow the release validation and publication rules in AGENTS.md/README.md.
- The user writes in Portuguese; code, comments, commits and docs are in
  English.
