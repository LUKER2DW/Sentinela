# CLAUDE.md

@AGENTS.md

## Claude-specific notes

- Read `AGENTS.md` (imported above) and the current phase in `docs/PLAN.md`
  before starting any task.
- Camera facts live in `docs/CAMERA.md`. If a task needs a camera behaviour
  that is not documented there, **stop and ask** — do not guess DVRIP
  commands or config field names.
- When the user's computer is linked, the camera is reachable only from it
  (`192.168.1.20` is a LAN address). Run read-only probes there; never
  move PTZ, change settings or play audio without explicit confirmation in
  the same conversation.
- Never ask the user to paste the camera password into chat. Scripts read it
  with `read -s` or from `SENTINELA_CAMERA_PASSWORD` in a local `.env`.
- Prefer editing the plan in place over creating new markdown files.
- The user writes in Portuguese; code, comments, commits and docs are in
  English.
