# ADR-0012: Minimal Nuvem file API in `sentinela serve`

Status: Accepted · 2026-09-29 (owner decision D4)

## Context
The mobile app needs the Nuvem (ADR-0008) without an SMB client. v1 built a
separate files service and a Nextcloud-like UI; that is exactly what we do
not repeat. Samba stays the main way to use the share on the LAN.

## Decision
A small file API inside the existing binary:
`GET /api/nas/list?path=`, `GET /api/nas/file?path=` (HTTP ranges),
resumable upload (`PUT` chunks with an offset, ≤ 4 GB per file, written
straight to a partial file on the target disk — never buffered in memory or
in `/tmp`, which is a RAM tmpfs), `POST /api/nas/mkdir`,
`POST /api/nas/move`, `DELETE /api/nas/file` → moved to `.lixeira/<user>/`
(the recycle bin Samba also uses; never a hard delete).

- Paths are opened through `os.Root` on `/srv/nas` (no `..`, no symlink
  escapes). Each Sentinela user sees `/srv/nas/<user>` and
  `/srv/nas/Compartilhado`; admins see the whole share.
- Roles: viewer read-only; operator/admin write. CSRF on writes; every write
  goes to `audit`.
- Thumbnails only for JPEG/PNG ≤ 20 MB (Go `image`), cached; no video
  thumbnails (no transcoding).
- The `sentinela` container mounts `/srv/nas` read-write (UID 1000 = owner
  of the share); the recorder has no access.

## Consequences
+ Nuvem in the app and the PWA through the same auth and Tailscale origin.
− The web process can now write user files: a bug here can destroy data,
  hence the recycle bin, the audit and `os.Root`. Backups (ADR-0014) cover
  `/srv/nas`.
