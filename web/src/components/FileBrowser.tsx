import { useEffect, useRef, useState, type DragEvent, type ReactNode } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError } from "../lib/api";
import {
  TRASH,
  baseName,
  crumbs,
  fileUrl,
  hasThumb,
  isAudio,
  isImage,
  isVideo,
  joinPath,
  label,
  listDir,
  mkdir,
  move,
  parentPath,
  remove,
  restorePath,
  thumbUrl,
  uploadFile,
  type NasEntry,
} from "../lib/nas";
import { fmtBytes, fmtDate } from "../lib/time";
import { useToast } from "../lib/session";
import { Icon } from "./Icon";

type View = "list" | "grid";
const VIEW_KEY = "sentinela.nuvem.view";

function loadView(): View {
  try {
    return localStorage.getItem(VIEW_KEY) === "grid" ? "grid" : "list";
  } catch {
    return "list";
  }
}

function saveView(v: View) {
  try {
    localStorage.setItem(VIEW_KEY, v);
  } catch {
    /* private mode: not remembered */
  }
}

interface Upload {
  id: number;
  name: string;
  target: string;
  file: File;
  sent: number;
  total: number;
  state: "waiting" | "sending" | "done" | "error" | "cancelled";
  error?: string;
  ctrl: AbortController;
}

const errText = (e: unknown) => {
  if (e instanceof ApiError) {
    if (e.status === 409) return "Já existe um item com esse nome aqui.";
    if (e.status === 403) return "Você não tem permissão para isso.";
    if (e.status === 413) return "Arquivo grande demais (máximo 4 GB).";
    if (e.status === 507) return "Não há espaço livre na Nuvem.";
    if (e.status === 503) return "A Nuvem não está disponível neste servidor.";
  }
  return (e as Error).message || "Algo deu errado.";
};

function entryIcon(e: NasEntry) {
  if (e.dir) return e.name === TRASH ? "trash" : "folder";
  if (isImage(e.name)) return "image";
  if (isVideo(e.name)) return "film";
  if (isAudio(e.name)) return "music";
  return "file";
}

function Thumb({ path, entry }: { path: string; entry: NasEntry }) {
  const [failed, setFailed] = useState(false);
  if (!entry.dir && hasThumb(entry.name) && !failed) {
    return <img className="file-thumb" src={thumbUrl(path)} alt="" loading="lazy" decoding="async" onError={() => setFailed(true)} />;
  }
  return (
    <span className={`file-icon${entry.dir ? " dir" : ""}`}>
      <Icon name={entryIcon(entry)} />
    </span>
  );
}

/** A small modal built on <dialog>; closes on Esc and backdrop click. */
function Sheet({ open, onClose, title, children }: { open: boolean; onClose: () => void; title: string; children: ReactNode }) {
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const d = ref.current;
    if (!d) return;
    if (open && !d.open) d.showModal();
    if (!open && d.open) d.close();
  }, [open]);
  return (
    <dialog
      ref={ref}
      className="sheet"
      aria-label={title}
      onClose={onClose}
      onClick={(e) => e.target === ref.current && onClose()}
    >
      {open && children}
    </dialog>
  );
}

type Ask = { kind: "mkdir" } | { kind: "rename"; path: string } | { kind: "move"; path: string };

export function FileBrowser({ path, onNavigate, canWrite }: { path: string; onNavigate: (p: string) => void; canWrite: boolean }) {
  const qc = useQueryClient();
  const toast = useToast();
  const q = useQuery({ queryKey: ["nas", path], queryFn: () => listDir(path), retry: 0 });
  const user = q.data?.user ?? "";
  const writable = canWrite && (q.data?.can_write ?? false);
  const inTrash = path === TRASH || path.startsWith(`${TRASH}/`);
  // The share root and the bin only hold folders managed by the server.
  const canAddHere = writable && path !== "" && !inTrash;
  const [view, setView] = useState<View>(loadView);
  const [selected, setSelected] = useState<string | null>(null);
  const [ask, setAsk] = useState<Ask | null>(null);
  const [uploads, setUploads] = useState<Upload[]>([]);
  const [dragging, setDragging] = useState(false);
  const pickRef = useRef<HTMLInputElement>(null);
  const busy = useRef(false);

  const refresh = (p = path) => void qc.invalidateQueries({ queryKey: ["nas", p] });

  // One upload at a time: gentle on the old server and on 4G.
  useEffect(() => {
    if (busy.current) return;
    const next = uploads.find((u) => u.state === "waiting");
    if (!next) return;
    busy.current = true;
    const patch = (p: Partial<Upload>) => setUploads((all) => all.map((u) => (u.id === next.id ? { ...u, ...p } : u)));
    patch({ state: "sending" });
    uploadFile(next.file, next.target, ({ sent, total }) => patch({ sent, total }), next.ctrl.signal)
      .then(() => {
        patch({ state: "done", sent: next.file.size });
        refresh(parentPath(next.target));
      })
      .catch((e) => patch(next.ctrl.signal.aborted ? { state: "cancelled" } : { state: "error", error: errText(e) }))
      .finally(() => {
        busy.current = false;
        setUploads((all) => [...all]); // wake the queue
      });
  }, [uploads]); // eslint-disable-line react-hooks/exhaustive-deps

  const enqueue = (files: FileList | File[]) => {
    const list = Array.from(files);
    if (!list.length) return;
    setUploads((all) => [
      ...all.filter((u) => u.state !== "done" && u.state !== "cancelled"),
      ...list.map((file, i) => ({
        id: Date.now() + i,
        name: file.name,
        target: joinPath(path, file.name),
        file,
        sent: 0,
        total: file.size,
        state: "waiting" as const,
        ctrl: new AbortController(),
      })),
    ]);
  };

  const onDrop = (e: DragEvent) => {
    e.preventDefault();
    setDragging(false);
    if (canAddHere) enqueue(e.dataTransfer.files);
  };

  const run = async (fn: () => Promise<unknown>, ok: string, refreshPaths: string[] = [path]) => {
    try {
      await fn();
      toast(ok);
      refreshPaths.forEach((p) => refresh(p));
      return true;
    } catch (e) {
      toast(errText(e), true);
      return false;
    }
  };

  const entries = q.data?.entries ?? [];
  const sel = selected ? entries.find((e) => joinPath(path, e.name) === selected) : undefined;

  return (
    <section
      className={`card files${dragging ? " dragging" : ""}`}
      onDragOver={(e) => {
        if (!canAddHere) return;
        e.preventDefault();
        setDragging(true);
      }}
      onDragLeave={() => setDragging(false)}
      onDrop={onDrop}
    >
      <div className="files-bar">
        <nav className="crumbs" aria-label="Pasta atual">
          {crumbs(path, user).map((c, i, all) =>
            i === all.length - 1 ? (
              <span key={c.path} aria-current="page">
                {c.name}
              </span>
            ) : (
              <button key={c.path} className="crumb" onClick={() => onNavigate(c.path)}>
                {c.name}
              </button>
            ),
          )}
        </nav>
        <div className="files-actions">
          <div className="segmented" role="group" aria-label="Exibição">
            <button aria-pressed={view === "list"} title="Lista" aria-label="Lista" onClick={() => (setView("list"), saveView("list"))}>
              <Icon name="list" />
            </button>
            <button aria-pressed={view === "grid"} title="Grade" aria-label="Grade" onClick={() => (setView("grid"), saveView("grid"))}>
              <Icon name="grid" />
            </button>
          </div>
          {canAddHere && (
            <>
              <button className="btn small" onClick={() => setAsk({ kind: "mkdir" })}>
                <Icon name="folderPlus" /> <span className="hide-sm">Nova pasta</span>
              </button>
              <button className="btn small primary" onClick={() => pickRef.current?.click()}>
                <Icon name="upload" /> Enviar
              </button>
              <input
                ref={pickRef}
                type="file"
                multiple
                hidden
                onChange={(e) => {
                  if (e.target.files) enqueue(e.target.files);
                  e.target.value = "";
                }}
              />
            </>
          )}
        </div>
      </div>

      {uploads.length > 0 && (
        <ul className="uploads" aria-label="Envios">
          {uploads.map((u) => (
            <li key={u.id} className={`upload ${u.state}`}>
              <span className="upload-name" title={u.target}>
                {u.name}
              </span>
              <span className="muted small num">
                {u.state === "error"
                  ? u.error
                  : u.state === "done"
                    ? "enviado"
                    : u.state === "cancelled"
                      ? "cancelado"
                      : u.state === "waiting"
                        ? "na fila"
                        : `${fmtBytes(u.sent)} de ${fmtBytes(u.total)}`}
              </span>
              {(u.state === "sending" || u.state === "waiting") && (
                <button className="btn ghost icon small" aria-label={`Cancelar ${u.name}`} onClick={() => u.ctrl.abort()}>
                  <Icon name="x" />
                </button>
              )}
              {u.state === "error" && (
                <button
                  className="btn ghost small"
                  onClick={() =>
                    setUploads((all) => all.map((x) => (x.id === u.id ? { ...x, state: "waiting", error: undefined, ctrl: new AbortController() } : x)))
                  }
                >
                  Tentar de novo
                </button>
              )}
              <span className="meter thin" aria-hidden="true">
                <span style={{ width: `${u.total ? (100 * u.sent) / u.total : u.state === "done" ? 100 : 0}%` }} />
              </span>
            </li>
          ))}
        </ul>
      )}

      {q.isError ? (
        <p className="notice error">{errText(q.error)}</p>
      ) : q.isLoading ? (
        <p className="muted files-empty">Carregando…</p>
      ) : entries.length === 0 ? (
        <div className="files-empty">
          <Icon name={inTrash ? "trash" : "folder"} />
          <strong>{inTrash ? "A lixeira está vazia" : "Pasta vazia"}</strong>
          <span className="muted">
            {inTrash
              ? "Arquivos apagados ficam aqui por 30 dias."
              : canAddHere
                ? "Toque em Enviar ou arraste arquivos para cá."
                : "Nada por aqui ainda."}
          </span>
        </div>
      ) : (
        <ul className={`file-list ${view}`}>
          {entries.map((e) => {
            const p = joinPath(path, e.name);
            return (
              <li key={e.name}>
                <button className="file" onClick={() => (e.dir ? onNavigate(p) : setSelected(p))} title={e.name}>
                  <Thumb path={p} entry={e} />
                  <span className="file-name">{e.dir && path === "" ? label(e.name, user) : e.name}</span>
                  <span className="file-meta muted small">
                    {e.dir ? fmtDate(e.modified) : `${fmtBytes(e.size)} · ${fmtDate(e.modified)}`}
                  </span>
                </button>
                {!(path === "" || (e.dir && e.name === TRASH)) && (
                  <button className="btn ghost icon small file-more" aria-label={`Ações para ${e.name}`} onClick={() => setSelected(p)}>
                    <Icon name="dots" />
                  </button>
                )}
              </li>
            );
          })}
        </ul>
      )}
      {inTrash && entries.length > 0 && <p className="muted small files-foot">Itens da lixeira somem sozinhos 30 dias depois de apagados.</p>}

      <Sheet open={!!sel} onClose={() => setSelected(null)} title={sel?.name ?? ""}>
        {sel && selected && (
          <ItemSheet
            path={selected}
            entry={sel}
            user={user}
            writable={writable}
            onClose={() => setSelected(null)}
            onRename={() => setAsk({ kind: "rename", path: selected })}
            onMove={() => setAsk({ kind: "move", path: selected })}
            onRestore={async (to) => {
              if (await run(() => move(selected, to), `Restaurado em ${parentPath(to) || "Nuvem"}.`)) setSelected(null);
            }}
            onDelete={async () => {
              if (await run(() => remove(selected), "Movido para a lixeira.")) setSelected(null);
            }}
          />
        )}
      </Sheet>

      <Sheet open={!!ask} onClose={() => setAsk(null)} title="Nome">
        {ask && (
          <NameForm
            ask={ask}
            path={path}
            onCancel={() => setAsk(null)}
            onSubmit={async (value) => {
              let ok = false;
              if (ask.kind === "mkdir") ok = await run(() => mkdir(joinPath(path, value)), "Pasta criada.");
              if (ask.kind === "rename") ok = await run(() => move(ask.path, joinPath(parentPath(ask.path), value)), "Nome alterado.");
              if (ask.kind === "move") {
                const to = joinPath(value.replace(/^\/+|\/+$/g, ""), baseName(ask.path));
                ok = await run(() => move(ask.path, to), "Movido.", [path, parentPath(to)]);
              }
              if (ok) {
                setAsk(null);
                setSelected(null);
              }
            }}
          />
        )}
      </Sheet>
    </section>
  );
}

function NameForm({ ask, path, onCancel, onSubmit }: { ask: Ask; path: string; onCancel: () => void; onSubmit: (v: string) => void }) {
  const initial = ask.kind === "rename" ? baseName(ask.path) : ask.kind === "move" ? path : "";
  const [value, setValue] = useState(initial);
  const title = ask.kind === "mkdir" ? "Nova pasta" : ask.kind === "rename" ? "Renomear" : "Mover para a pasta";
  return (
    <form
      className="stack-sm"
      onSubmit={(e) => {
        e.preventDefault();
        if (value.trim() || ask.kind === "move") onSubmit(value.trim());
      }}
    >
      <h2>{title}</h2>
      {ask.kind === "move" && <p className="muted small">Caminho da pasta de destino, por exemplo “Compartilhado/Fotos”.</p>}
      <input className="input" autoFocus value={value} onChange={(e) => setValue(e.target.value)} aria-label={title} />
      <div className="sheet-actions">
        <button type="button" className="btn" onClick={onCancel}>
          Cancelar
        </button>
        <button type="submit" className="btn primary">
          {ask.kind === "mkdir" ? "Criar" : ask.kind === "rename" ? "Renomear" : "Mover"}
        </button>
      </div>
    </form>
  );
}

/** Web Share with the file itself, when the phone supports it (≤ 50 MB). */
async function shareFile(path: string, name: string): Promise<boolean> {
  const nav = navigator as Navigator & { canShare?: (d: ShareData) => boolean };
  if (!nav.share || !nav.canShare) return false;
  const res = await fetch(fileUrl(path, true), { credentials: "same-origin" });
  if (!res.ok) throw new ApiError(res.status, `HTTP ${res.status}`);
  const blob = await res.blob();
  const file = new File([blob], name, { type: blob.type || "application/octet-stream" });
  if (!nav.canShare({ files: [file] })) return false;
  await nav.share({ files: [file], title: name });
  return true;
}

const SHARE_MAX = 50 << 20;

function ItemSheet(props: {
  path: string;
  entry: NasEntry;
  user: string;
  writable: boolean;
  onClose: () => void;
  onRename: () => void;
  onMove: () => void;
  onRestore: (to: string) => void;
  onDelete: () => void;
}) {
  const { path, entry, user, writable } = props;
  const toast = useToast();
  const [sharing, setSharing] = useState(false);
  const from = restorePath(path, user);
  const inTrash = path.startsWith(`${TRASH}/`);
  const src = fileUrl(path);
  return (
    <div className="stack-sm item-sheet">
      <div className="sheet-head">
        <h2 title={entry.name}>{entry.name}</h2>
        <button className="btn ghost icon small" aria-label="Fechar" onClick={props.onClose}>
          <Icon name="x" />
        </button>
      </div>
      {!entry.dir && isImage(entry.name) && <img className="preview" src={src} alt={entry.name} />}
      {!entry.dir && isVideo(entry.name) && <video className="preview" src={src} controls playsInline preload="metadata" />}
      {!entry.dir && isAudio(entry.name) && <audio src={src} controls preload="metadata" />}
      <p className="muted small">
        {entry.dir ? "Pasta" : fmtBytes(entry.size)} · alterado em {fmtDate(entry.modified)}
        {from && <> · veio de {parentPath(from) || "Nuvem"}</>}
      </p>
      <div className="sheet-grid">
        {!entry.dir && (
          <a className="btn" href={fileUrl(path, true)} download={entry.name}>
            <Icon name="download" /> Baixar
          </a>
        )}
        {!entry.dir && entry.size <= SHARE_MAX && "share" in navigator && (
          <button
            className="btn"
            disabled={sharing}
            onClick={() => {
              setSharing(true);
              shareFile(path, entry.name)
                .then((ok) => ok || toast("Este aparelho não compartilha arquivos; use Baixar.", true))
                .catch((e) => (e as Error).name !== "AbortError" && toast(errText(e), true))
                .finally(() => setSharing(false));
            }}
          >
            <Icon name="share" /> {sharing ? "Preparando…" : "Compartilhar"}
          </button>
        )}
        {writable && from && (
          <button className="btn primary" onClick={() => props.onRestore(from)}>
            <Icon name="restore" /> Restaurar
          </button>
        )}
        {writable && !inTrash && (
          <>
            <button className="btn" onClick={props.onRename}>
              <Icon name="edit" /> Renomear
            </button>
            <button className="btn" onClick={props.onMove}>
              <Icon name="move" /> Mover
            </button>
            <button className="btn danger" onClick={() => window.confirm(`Mover “${entry.name}” para a lixeira?`) && props.onDelete()}>
              <Icon name="trash" /> Apagar
            </button>
          </>
        )}
      </div>
    </div>
  );
}
