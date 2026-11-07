import { useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Icon } from "./Icon";
import { hosting, type EnvVar, type FileDocument, type FileScope, type ProjectFile, type Service } from "../lib/hosting";
import { fmtBytes, relative } from "../lib/time";
import { useToast } from "../lib/session";

import { t } from "../lib/i18n";
const joined = (at: string, name: string) => at ? `${at}/${name}` : name;

export function ServiceFiles({ svc }: { svc: Service }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [scope, setScope] = useState<FileScope>(svc.managed ? "project" : "data");
  const [at, setAt] = useState("");
  const [selected, setSelected] = useState<{ path: string; doc: FileDocument } | null>(null);
  const [creating, setCreating] = useState<"file" | "folder" | null>(null);
  const [name, setName] = useState("");
  const [deleting, setDeleting] = useState<ProjectFile | null>(null);
  const [progress, setProgress] = useState(0);
  const uploadInput = useRef<HTMLInputElement>(null);
  const q = useQuery({ queryKey: ["hosting", "files", svc.id, scope, at], queryFn: () => hosting.files(svc.id, scope, at), enabled: scope !== "project" || svc.managed });
  const refresh = () => void qc.invalidateQueries({ queryKey: ["hosting"] });
  const open = useMutation({
    mutationFn: (path: string) => hosting.readFile(svc.id, scope, path).then((doc) => ({ path, doc })),
    onSuccess: setSelected,
  });
  const create = useMutation({
    mutationFn: () => {
      if (!name.trim() || /[/\\]/.test(name) || name === "." || name === "..") throw new Error(t("Enter only the file or folder name."));
      const p = joined(at, name.trim());
      return creating === "folder" ? hosting.mkdir(svc.id, scope, p) : hosting.writeFile(svc.id, scope, p, { kind: name.startsWith(".env") ? "env" : "text", revision: "", content: "", env: [] });
    },
    onSuccess: (result) => { setCreating(null); setName(""); refresh(); if ("secrets_imported" in result && result.secrets_imported) toast(t("Variables imported into Secrets. Edit and save in this tab to apply.")); },
  });
  const remove = useMutation({
    mutationFn: () => hosting.deleteFile(svc.id, scope, deleting!.path),
    onSuccess: () => { setDeleting(null); refresh(); toast(t("File deleted.")); },
  });
  const upload = useMutation({
    mutationFn: async (files: File[]) => {
      let imported = false;
      for (const file of files) {
        setProgress(0);
        const result = await hosting.uploadFile(svc.id, scope, joined(at, file.name), file, setProgress);
        imported ||= !!result.secrets_imported;
      }
      return imported;
    },
    onSuccess: (imported) => { refresh(); toast(imported ? t("Files uploaded. .env variables were moved to Secrets; open that tab and save to apply.") : t("Files uploaded.")); },
    onSettled: refresh,
  });
  const changeFolder = (p: string) => { setAt(p); setSelected(null); setCreating(null); open.reset(); };
  const crumbs = at ? at.split("/") : [];
  const busy = svc.status.state === "deploying" || upload.isPending || create.isPending || remove.isPending;
  const error = q.error ?? open.error ?? create.error ?? remove.error ?? upload.error;

  return (
    <div className="stack-sm svc-files">
      <div className="segmented" role="group" aria-label={t("File area")}>
        <button aria-pressed={scope === "project"} disabled={!svc.managed} onClick={() => { setScope("project"); changeFolder(""); }}><Icon name="folder" />{" "}{t("Protected project")}</button>
        <button aria-pressed={scope === "data"} disabled={!svc.deployed} onClick={() => { setScope("data"); changeFolder(""); }}><Icon name="archive" />{" "}{t("Persistent data")}</button>
      </div>
      <p className="hint">{scope === "project" ? t("Changes are saved and take effect when you click Update. Uploaded .env files are imported into the Secrets tab and removed from the project.") : t("This data survives deploys. Changing it affects the current service; stop the service before editing files it is using.")}</p>
      {!svc.managed && <p className="hint">{t("Local and ZIP projects are moved to the protected area on the first deploy. GitHub projects are updated from the repository.")}</p>}
      {error && <p className="notice error" role="alert">{error.message}</p>}
      {selected ? (
        <FileEditor key={`${scope}:${selected.path}:${selected.doc.revision}`} id={svc.id} scope={scope} path={selected.path} doc={selected.doc} busy={busy}
          onClose={() => setSelected(null)} onSaved={() => { refresh(); setSelected(null); toast(scope === "project" ? t("File saved. Click Update to apply to the service.") : t("File saved.")); }} />
      ) : (
        <>
          <div className="svc-file-toolbar">
            <nav className="svc-crumbs small" aria-label={t("Service folder")}>
              <button className="btn ghost small" onClick={() => changeFolder("")}>{scope === "project" ? t("Project") : t("Data")}</button>
              {crumbs.map((c, i) => <button key={i} className="btn ghost small" onClick={() => changeFolder(crumbs.slice(0, i + 1).join("/"))}>/ {c}</button>)}
            </nav>
            <span className="grow" />
            <button className="btn small" disabled={busy} onClick={() => { setCreating("folder"); create.reset(); }}><Icon name="plus" />{" "}{t("Folder")}</button>
            <button className="btn small" disabled={busy} onClick={() => { setCreating("file"); create.reset(); }}><Icon name="plus" />{" "}{t("File")}</button>
            <button className="btn small" disabled={busy} onClick={() => uploadInput.current?.click()}><Icon name="upload" />{" "}{t("Upload")}</button>
            <input ref={uploadInput} className="sr-only" type="file" multiple aria-label={t("Upload files to the service")} onChange={(e) => { const files = Array.from(e.target.files ?? []); if (files.length) upload.mutate(files); e.target.value = ""; }} />
          </div>
          {upload.isPending && <p className="notice small" role="status">{t("Uploading files…")}{" "}{progress}%</p>}
          {creating && <form className="svc-file-create" onSubmit={(e) => { e.preventDefault(); create.mutate(); }}>
            <input className="input" aria-label={creating === "folder" ? t("New folder name") : t("New file name")} autoFocus value={name} onChange={(e) => setName(e.target.value)} placeholder={creating === "folder" ? t("Folder name") : "config.json"} />
            <button className="btn primary small" disabled={busy || !name.trim()}>{t("Create")}</button><button type="button" className="btn ghost small" onClick={() => setCreating(null)}>{t("Cancel")}</button>
          </form>}
          <ul className="svc-file-list">
            {q.isLoading && <li className="muted small">{t("Loading files…")}</li>}
            {q.data?.entries.length === 0 && <li className="muted small">{t("Empty folder.")}</li>}
            {q.data?.entries.map((file) => <li key={file.path}>
              <button className="svc-file-name" disabled={open.isPending} onClick={() => file.dir ? changeFolder(file.path) : open.mutate(file.path)}>
                <Icon name={file.dir ? "folder" : file.secret ? "lock" : "file"} /><span>{file.name}</span>
              </button>
              <span className="small muted num">{file.dir ? t("Folder") : fmtBytes(file.bytes)}</span>
              <span className="small muted svc-file-date">{relative(file.modified)}</span>
              <button className="btn ghost icon small danger" disabled={busy} aria-label={t("Remove {name}", { name: file.name })} onClick={() => { setDeleting(file); remove.reset(); }}><Icon name="trash" /></button>
            </li>)}
          </ul>
        </>
      )}
      {deleting && <div className="notice svc-file-delete" role="alert">
        <p>{t("Remove")}{" "}<strong>{deleting.name}</strong>{deleting.dir ? t(" and all the folder's contents") : ""}?</p>
        <button className="btn danger small" disabled={busy} onClick={() => remove.mutate()}>{t("Remove")}</button><button className="btn ghost small" onClick={() => setDeleting(null)}>{t("Cancel")}</button>
      </div>}
    </div>
  );
}

function FileEditor({ id, scope, path, doc, busy, onClose, onSaved }: { id: string; scope: FileScope; path: string; doc: FileDocument; busy: boolean; onClose: () => void; onSaved: () => void }) {
  const [content, setContent] = useState(doc.content ?? "");
  const [env, setEnv] = useState<EnvVar[]>(doc.env ?? []);
  const save = useMutation({ mutationFn: () => hosting.writeFile(id, scope, path, { ...doc, content, env }), onSuccess: onSaved });
  const update = (i: number, patch: Partial<EnvVar>) => setEnv((old) => old.map((e,j) => j === i ? { ...e, ...patch } : e));
  return <form className="stack-sm svc-file-editor" onSubmit={(e) => { e.preventDefault(); save.mutate(); }}>
    <div className="svc-file-toolbar"><strong className="svc-code">{path}</strong><span className="grow" /><button type="button" className="btn ghost small" onClick={onClose}>{t("Back to files")}</button></div>
    {doc.kind === "env" ? <>
      <p className="notice small"><Icon name="lock" />{" "}{t("Existing values stay on the server. Leave the value empty to keep it; fill it in to replace it. Deleting a row removes the variable.")}</p>
      {env.map((e,i) => <div className="svc-row env" key={i}>
        <input className="input svc-code" aria-label={t("Variable name")} value={e.key} onChange={(ev) => update(i,{ key: ev.target.value })} spellCheck={false} />
        <input className="input svc-code" aria-label={t("New value for {key}", { key: e.key })} type="password" autoComplete="off" placeholder={t("stored; empty keeps it")} value={e.value} onChange={(ev) => update(i,{ value: ev.target.value })} />
        <button type="button" className="btn ghost icon small" aria-label={t("Delete variable {key}", { key: e.key })} onClick={() => setEnv((old) => old.filter((_,j) => j!==i))}><Icon name="x" /></button>
      </div>)}
      <button type="button" className="btn ghost small" onClick={() => setEnv((old) => [...old, { key:"", value:"", secret:true }])}><Icon name="plus" />{" "}{t("Variable")}</button>
    </> : <>
      {content.includes("__SENTINELA_SECRET_") && <p className="hint">{t("Secret markers keep the values on the server. Keep them to preserve the existing configuration.")}</p>}
      <textarea className="input svc-code svc-file-text" aria-label={t("Contents of {path}", { path })} value={content} onChange={(e) => setContent(e.target.value)} spellCheck={false} />
    </>}
    {save.error && <p className="notice error" role="alert">{save.error.message}</p>}
    <div className="dialog-actions"><button type="button" className="btn ghost" onClick={onClose}>{t("Cancel")}</button><button className="btn primary" disabled={busy || save.isPending}>{save.isPending ? t("Saving…") : t("Save file")}</button></div>
  </form>;
}
