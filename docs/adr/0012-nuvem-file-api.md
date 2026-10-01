# ADR-0012: Nuvem file API inside Sentinela

Status: Accepted 2026-09-29, amended by the owner's UI/permission decisions
on 2026-09-30.

## Decision

The existing `sentinela serve` process owns the browser/mobile file API.
Samba remains the separate LAN share; no file-service container is added.

| Operation | Endpoint/behavior |
|---|---|
| List/read | `GET /api/nas/list`, `GET /api/nas/file`, HTTP ranges |
| Upload | Resumable `PUT /api/nas/upload?path=&offset=&total=` |
| Create/move | `POST /api/nas/mkdir`, `POST /api/nas/move` |
| Delete | `DELETE /api/nas/file` moves into the recycle bin |
| Purge | Explicit `DELETE /api/nas/trash` permanently removes trash |
| Text edit | `PUT /api/nas/text`, bounded to 2 MiB |

Uploads go directly to partial files on the target disk, never buffered as
a whole file in RAM or `/tmp`. Files are limited to 4 GiB; chunks are 8 MiB
in the UI. Existing final files are not silently overwritten.

## Access and presentation

- Filesystem operations use `os.Root` confinement and permission checks.
- Viewers have no Nuvem API or page. Operators use their own directory and
  Compartilhado. Admins can access the share.
- Top-level folders cannot be moved, renamed or deleted through the API.
  Admins can create additional top-level folders.
- CSRF protects writes, which are audited. Samba uses its own LAN account;
  these web scopes do not automatically constrain an SMB login.
- List/grid, tree, filters, details and previews share the same API.
  Previewing a video uses the viewing device; server video transcoding is
  not added. Server thumbnails are limited to bounded JPEG/PNG files.
- Personal-folder icons show the user's photo. The shared owner column
  comes from upload/mkdir audit data at that path; it is an informative
  label, not an access-control or filesystem ownership rule.
- Browser text editing, restore and permanent purge are explicit actions.
  API and Samba deletes share the `.lixeira/<user>/` convention.

## Folder upload

Desktop folder selection and drop traversal preserve file paths. Parent
directories are created first, then files use the same resumable uploader.
Three small files can transfer in parallel; large files are serial.
A conflicting top folder is sent as `Name (2)`, rather than mixed into an
existing one.

No ZIP creation/extraction is introduced: photos/video compress poorly,
and extraction would add work to the old server CPU. Empty directories
are not represented by the file picker/traversal. Phone users upload files;
Android's picker does not expose the desktop folder-selection flow.

## Consequences

+ One auth/API for browser and Android, on the selected HTTPS origin.
+ Recording remains independent; the recorder has no NAS mount.
- Web writes can affect user data. Recycle bin, scope checks and audit
  reduce risk; they do not replace backups and restore verification.