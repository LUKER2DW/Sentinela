import { useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Icon } from "./Icon";
import { hosting, type EnvVar, type FileDocument, type FileScope, type ProjectFile, type Service } from "../lib/hosting";
import { fmtBytes, relative } from "../lib/time";
import { useToast } from "../lib/session";

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
      if (!name.trim() || /[/\\]/.test(name) || name === "." || name === "..") throw new Error("Informe apenas o nome do arquivo ou da pasta.");
      const p = joined(at, name.trim());
      return creating === "folder" ? hosting.mkdir(svc.id, scope, p) : hosting.writeFile(svc.id, scope, p, { kind: name.startsWith(".env") ? "env" : "text", revision: "", content: "", env: [] });
    },
    onSuccess: (result) => { setCreating(null); setName(""); refresh(); if ("secrets_imported" in result && result.secrets_imported) toast("Variáveis importadas para Secrets. Edite e salve nessa aba para aplicar."); },
  });
  const remove = useMutation({
    mutationFn: () => hosting.deleteFile(svc.id, scope, deleting!.path),
    onSuccess: () => { setDeleting(null); refresh(); toast("Arquivo excluído."); },
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
    onSuccess: (imported) => { refresh(); toast(imported ? "Arquivos enviados. Variáveis da .env foram para Secrets; abra essa aba e salve para aplicar." : "Arquivos enviados."); },
    onSettled: refresh,
  });
  const changeFolder = (p: string) => { setAt(p); setSelected(null); setCreating(null); open.reset(); };
  const crumbs = at ? at.split("/") : [];
  const busy = svc.status.state === "deploying" || upload.isPending || create.isPending || remove.isPending;
  const error = q.error ?? open.error ?? create.error ?? remove.error ?? upload.error;

  return (
    <div className="stack-sm svc-files">
      <div className="segmented" role="group" aria-label="Área de arquivos">
        <button aria-pressed={scope === "project"} disabled={!svc.managed} onClick={() => { setScope("project"); changeFolder(""); }}><Icon name="folder" /> Projeto protegido</button>
        <button aria-pressed={scope === "data"} disabled={!svc.deployed} onClick={() => { setScope("data"); changeFolder(""); }}><Icon name="archive" /> Dados persistentes</button>
      </div>
      <p className="hint">{scope === "project" ? "As alterações ficam guardadas e entram em execução ao clicar em Atualizar. Arquivos .env enviados são importados para a aba Secrets e removidos do projeto." : "Estes dados sobrevivem aos deploys. Alterá-los afeta o serviço atual; pare o serviço antes de editar arquivos que ele esteja usando."}</p>
      {!svc.managed && <p className="hint">Projetos locais e ZIP são transferidos para a área protegida no primeiro deploy. Projetos GitHub são atualizados pelo repositório.</p>}
      {error && <p className="notice error" role="alert">{error.message}</p>}
      {selected ? (
        <FileEditor key={`${scope}:${selected.path}:${selected.doc.revision}`} id={svc.id} scope={scope} path={selected.path} doc={selected.doc} busy={busy}
          onClose={() => setSelected(null)} onSaved={() => { refresh(); setSelected(null); toast(scope === "project" ? "Arquivo salvo. Clique em Atualizar para aplicar ao serviço." : "Arquivo salvo."); }} />
      ) : (
        <>
          <div className="svc-file-toolbar">
            <nav className="svc-crumbs small" aria-label="Pasta do serviço">
              <button className="btn ghost small" onClick={() => changeFolder("")}>{scope === "project" ? "Projeto" : "Dados"}</button>
              {crumbs.map((c, i) => <button key={i} className="btn ghost small" onClick={() => changeFolder(crumbs.slice(0, i + 1).join("/"))}>/ {c}</button>)}
            </nav>
            <span className="grow" />
            <button className="btn small" disabled={busy} onClick={() => { setCreating("folder"); create.reset(); }}><Icon name="plus" /> Pasta</button>
            <button className="btn small" disabled={busy} onClick={() => { setCreating("file"); create.reset(); }}><Icon name="plus" /> Arquivo</button>
            <button className="btn small" disabled={busy} onClick={() => uploadInput.current?.click()}><Icon name="upload" /> Enviar</button>
            <input ref={uploadInput} className="sr-only" type="file" multiple aria-label="Enviar arquivos ao serviço" onChange={(e) => { const files = Array.from(e.target.files ?? []); if (files.length) upload.mutate(files); e.target.value = ""; }} />
          </div>
          {upload.isPending && <p className="notice small" role="status">Enviando arquivos… {progress}%</p>}
          {creating && <form className="svc-file-create" onSubmit={(e) => { e.preventDefault(); create.mutate(); }}>
            <input className="input" aria-label={creating === "folder" ? "Nome da nova pasta" : "Nome do novo arquivo"} autoFocus value={name} onChange={(e) => setName(e.target.value)} placeholder={creating === "folder" ? "Nome da pasta" : "config.json"} />
            <button className="btn primary small" disabled={busy || !name.trim()}>Criar</button><button type="button" className="btn ghost small" onClick={() => setCreating(null)}>Cancelar</button>
          </form>}
          <ul className="svc-file-list">
            {q.isLoading && <li className="muted small">Carregando arquivos…</li>}
            {q.data?.entries.length === 0 && <li className="muted small">Pasta vazia.</li>}
            {q.data?.entries.map((file) => <li key={file.path}>
              <button className="svc-file-name" disabled={open.isPending} onClick={() => file.dir ? changeFolder(file.path) : open.mutate(file.path)}>
                <Icon name={file.dir ? "folder" : file.secret ? "lock" : "file"} /><span>{file.name}</span>
              </button>
              <span className="small muted num">{file.dir ? "Pasta" : fmtBytes(file.bytes)}</span>
              <span className="small muted svc-file-date">{relative(file.modified)}</span>
              <button className="btn ghost icon small danger" disabled={busy} aria-label={`Excluir ${file.name}`} onClick={() => { setDeleting(file); remove.reset(); }}><Icon name="trash" /></button>
            </li>)}
          </ul>
        </>
      )}
      {deleting && <div className="notice svc-file-delete" role="alert">
        <p>Excluir <strong>{deleting.name}</strong>{deleting.dir ? " e todo o conteúdo da pasta" : ""}?</p>
        <button className="btn danger small" disabled={busy} onClick={() => remove.mutate()}>Excluir</button><button className="btn ghost small" onClick={() => setDeleting(null)}>Cancelar</button>
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
    <div className="svc-file-toolbar"><strong className="svc-code">{path}</strong><span className="grow" /><button type="button" className="btn ghost small" onClick={onClose}>Voltar aos arquivos</button></div>
    {doc.kind === "env" ? <>
      <p className="notice small"><Icon name="lock" /> Valores existentes ficam no servidor. Deixe o valor vazio para manter; preencha para trocar. Excluir uma linha remove a variável.</p>
      {env.map((e,i) => <div className="svc-row env" key={i}>
        <input className="input svc-code" aria-label="Nome da variável" value={e.key} onChange={(ev) => update(i,{ key: ev.target.value })} spellCheck={false} />
        <input className="input svc-code" aria-label={`Novo valor de ${e.key}`} type="password" autoComplete="off" placeholder="guardado; vazio mantém" value={e.value} onChange={(ev) => update(i,{ value: ev.target.value })} />
        <button type="button" className="btn ghost icon small" aria-label={`Excluir variável ${e.key}`} onClick={() => setEnv((old) => old.filter((_,j) => j!==i))}><Icon name="x" /></button>
      </div>)}
      <button type="button" className="btn ghost small" onClick={() => setEnv((old) => [...old, { key:"", value:"", secret:true }])}><Icon name="plus" /> Variável</button>
    </> : <>
      {content.includes("__SENTINELA_SECRET_") && <p className="hint">Os marcadores de segredo mantêm os valores no servidor. Preserve-os para manter a configuração existente.</p>}
      <textarea className="input svc-code svc-file-text" aria-label={`Conteúdo de ${path}`} value={content} onChange={(e) => setContent(e.target.value)} spellCheck={false} />
    </>}
    {save.error && <p className="notice error" role="alert">{save.error.message}</p>}
    <div className="dialog-actions"><button type="button" className="btn ghost" onClick={onClose}>Cancelar</button><button className="btn primary" disabled={busy || save.isPending}>{save.isPending ? "Salvando…" : "Salvar arquivo"}</button></div>
  </form>;
}
