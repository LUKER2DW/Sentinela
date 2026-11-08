# ADR-0024: Settings edited from the dashboard

Status: accepted (2026-11-07).

## Context

Everything site-specific lives in `/etc/sentinela/config.yaml` and the
root-only `secrets.env`. Changing a camera, the retention or the time zone
meant SSH, an editor and a restart, and the System page could only show
things. The API container must still not write to `/etc` or hold root
(AGENTS.md §2.4, §2.9).

## Decision

- Admins edit cameras (address, vendor, paths, ports, recording, audio,
  credentials), recording (disk use, retention, file length, the volume
  marker), the time zone, the network name, the Tailscale address and the
  disks shown in the dashboard, from System.
- `PUT /api/settings` (admin, from home or the tailnet) merges the change
  into the current file, keeps every other key, and validates the result
  with the server's own parser. It leaves `pending.yaml`, new or changed
  credentials in `pending-secrets.json` (0600) and a request under
  `data_dir/config`; it never returns credentials.
- `sentinela-config.path` (root, `scripts/apply-config.sh`) validates again
  with the real secrets (`sentinela check-config`, no network), keeps the
  last ten backups of both files, installs them, regenerates go2rtc's
  streams, recreates the services and rolls back when the health check
  fails. The pending files are always removed. The result is shown on the
  page.
- Not editable here, because they change what code runs: the services
  platform (`apps`) and where updates come from. (Remote access,
  `remote.public` and `remote.proxy`, became editable with ADR-0025.)

## Consequences

+ The whole house is configured from the browser, with validation, backups
  and rollback.
+ The API still holds no root and cannot write `/etc` directly.
− A change restarts the API and the recorder (a few seconds of gap).
− Comments in `config.yaml` are not kept when the dashboard rewrites it;
  the backups keep the earlier text.
