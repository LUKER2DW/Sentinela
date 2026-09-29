import { useEffect, useId, useMemo, useRef, useState, type DragEvent, type MouseEvent, type ReactNode } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError } from "../lib/api";
import { usePinchZoom } from "../lib/pinch";
import {
  TRASH,
  baseName,
  crumbs,
  fileUrl,
  hasThumb,
  inside,
  joinPath,
  kindLabel,
  kindOf,
  label,
  listDir,
  mkdir,
  move,
  parentPath,
  remove,
  restorePath,
  sortEntries,
  thumbUrl,
  uploadFile,
  type Kind,
  type NasEntry,
  type SortKey,
} from "../lib/nas";
import { fmtBytes, fmtDate } from "../lib/time";
import { useToast } from "../lib/session";
import { useCompact } from "../lib/viewport";
import { Icon } from "./Icon";

type View = "list" | "grid";
const VIEW_KEY = "sentinela.nuvem.view";
// Items dragged inside the Nuvem carry their paths under this type.
const DRAG_TYPE = "application/x-sentinela-nuvem";

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
    if (e.status === 404) return "O item não existe mais.";
  }
  return (e as Error).message || "Algo deu errado.";
};

const kindIcon: Record<Kind, string> = {
  folder: "folder",
  image: "image",
  video: "film",
  audio: "music",
  pdf: "pdf",
  text: "text",
  archive: "archive",
  file: "file",
};

function entryIcon(e: NasEntry) {
  if (e.dir && e.name === TRASH) return "trash";
  return kindIcon[kindOf(e)];
}

function Thumb({ path, entry }: { path: string; entry: NasEntry }) {
  const [failed, setFailed] = useState(false);
  if (!entry.dir && hasThumb(entry.name) && !failed) {
    return <img className="file-thumb" src={thumbUrl(path)} alt="" loading="lazy" decoding="async" onError={() => setFailed(true)} draggable={false} />;
  }
  return (
    <span className={`file-icon k-${entry.dir ? "folder" : kindOf(entry)}`}>
      <Icon name={entryIcon(entry)} />
    </span>
  );
}

/** A folder can receive items: not the share root, not the bin. */
const canReceive = (p: string) => p !== "" && p !== TRASH && !p.startsWith(`${TRASH}/`);

type Ask = { kind: "mkdir"; at: string } | { kind: "rename"; path: string };
type Menu = { x: number; y: number; bg: boolean };

export function FileBrowser({
  path,
  onNavigate,
  canWrite,
  summary,
  onConnect,
}: {
  path: string;
  onNavigate: (p: string) => void;
  canWrite: boolean;
  /** Space line for the top card. */
  summary?: ReactNode;
  /** Opens the "network folders" help. */
  onConnect?: () => void;
}) {
  const qc = useQueryClient();
  const toast = useToast();
  const compact = useCompact();
  const q = useQuery({ queryKey: ["nas", path], queryFn: () => listDir(path), retry: 0 });
  const user = q.data?.user ?? "";
  const writable = canWrite && (q.data?.can_write ?? false);
  const inTrash = path === TRASH || path.startsWith(`${TRASH}/`);
  const canAddHere = writable && canReceive(path);
  const [view, setView] = useState<View>(loadView);
  const [sort, setSort] = useState<{ key: SortKey; asc: boolean }>({ key: "name", asc: true });
  const [filter, setFilter] = useState("");
  const [sel, setSel] = useState<{ folder: string; paths: string[] }>({ folder: path, paths: [] });
  const [anchor, setAnchor] = useState<string | null>(null);
  const [menu, setMenu] = useState<Menu | null>(null);
  const [ask, setAsk] = useState<Ask | null>(null);
  const [moving, setMoving] = useState<string[] | null>(null);
  const [preview, setPreview] = useState<string | null>(null);
  const [details, setDetails] = useState<string | null>(null);
  const [uploads, setUploads] = useState<Upload[]>([]);
  const [dropOn, setDropOn] = useState<string | null>(null);
  const pickId = useId();
  // The hidden file input is clicked by id: menus and buttons share it.
  const pickFiles = () => (document.getElementById(pickId) as HTMLInputElement | null)?.click();
  const busy = useRef(false);

  // Selection belongs to the folder it was made in.
  const selected = useMemo(() => (sel.folder === path ? sel.paths : []), [sel, path]);
  const setSelected = (paths: string[]) => setSel({ folder: path, paths });

  const refresh = (...paths: string[]) => paths.forEach((p) => void qc.invalidateQueries({ queryKey: ["nas", p] }));

  const entries = useMemo(() => {
    const all = sortEntries(q.data?.entries ?? [], sort.key, sort.asc);
    const f = filter.trim().toLowerCase();
    return f ? all.filter((e) => e.name.toLowerCase().includes(f)) : all;
  }, [q.data, sort, filter]);
  const byPath = useMemo(() => new Map(entries.map((e) => [joinPath(path, e.name), e])), [entries, path]);
  const files = entries.filter((e) => !e.dir).map((e) => joinPath(path, e.name));

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

  const enqueue = (list: FileList | File[], dir = path) => {
    const files = Array.from(list);
    if (!files.length) return;
    setUploads((all) => [
      ...all.filter((u) => u.state !== "done" && u.state !== "cancelled"),
      ...files.map((file, i) => ({
        id: Date.now() + i,
        name: file.name,
        target: joinPath(dir, file.name),
        file,
        sent: 0,
        total: file.size,
        state: "waiting" as const,
        ctrl: new AbortController(),
      })),
    ]);
  };

  const run = async (fn: () => Promise<unknown>, ok: string, refreshPaths: string[] = [path]) => {
    try {
      await fn();
      if (ok) toast(ok);
      refresh(...refreshPaths);
      return true;
    } catch (e) {
      toast(errText(e), true);
      refresh(...refreshPaths);
      return false;
    }
  };

  /** Moves items into a folder, one by one; reports the first failure. */
  const moveInto = async (paths: string[], dest: string) => {
    const todo = paths.filter((p) => !inside(dest, p) && parentPath(p) !== dest);
    if (!todo.length) return false;
    let done = 0;
    for (const p of todo) {
      try {
        await move(p, joinPath(dest, baseName(p)));
        done++;
      } catch (e) {
        toast(`${baseName(p)}: ${errText(e)}`, true);
        break;
      }
    }
    refresh(path, dest, ...todo.map(parentPath));
    if (done) toast(done === 1 ? `Movido para ${folderName(dest, user)}.` : `${done} itens movidos para ${folderName(dest, user)}.`);
    setSelected([]);
    return done > 0;
  };

  const trash = async (paths: string[]) => {
    if (!paths.length) return;
    const what = paths.length === 1 ? `“${baseName(paths[0])}”` : `${paths.length} itens`;
    if (!window.confirm(`Mover ${what} para a lixeira? Ela guarda tudo por 30 dias.`)) return;
    let done = 0;
    for (const p of paths) {
      try {
        await remove(p);
        done++;
      } catch (e) {
        toast(`${baseName(p)}: ${errText(e)}`, true);
        break;
      }
    }
    refresh(path, TRASH);
    if (done) toast(done === 1 ? "Movido para a lixeira." : `${done} itens movidos para a lixeira.`);
    setSelected([]);
    setPreview(null);
  };

  const restore = async (paths: string[]) => {
    for (const p of paths) {
      const to = restorePath(p, user);
      if (to && !(await run(() => move(p, to), `Restaurado em ${folderName(parentPath(to), user)}.`, [path, parentPath(to)]))) break;
    }
    setSelected([]);
  };

  const download = (paths: string[]) => {
    paths
      .filter((p) => !byPath.get(p)?.dir)
      .forEach((p, i) =>
        window.setTimeout(() => {
          const a = document.createElement("a");
          a.href = fileUrl(p, true);
          a.download = baseName(p);
          document.body.append(a);
          a.click();
          a.remove();
        }, i * 400),
      );
  };

  const open = (p: string) => {
    const e = byPath.get(p);
    if (!e) return;
    if (e.dir) onNavigate(p);
    else setPreview(p);
  };

  // ---------- selection ----------
  const clickRow = (ev: MouseEvent, p: string) => {
    if (compact) {
      open(p);
      return;
    }
    if (ev.shiftKey && anchor && byPath.has(anchor)) {
      const keys = [...byPath.keys()];
      const [a, b] = [keys.indexOf(anchor), keys.indexOf(p)].sort((x, y) => x - y);
      setSelected(keys.slice(a, b + 1));
      return;
    }
    if (ev.ctrlKey || ev.metaKey) {
      setSelected(selected.includes(p) ? selected.filter((x) => x !== p) : [...selected, p]);
    } else {
      setSelected([p]);
    }
    setAnchor(p);
  };
  const toggleCheck = (p: string) => {
    setSelected(selected.includes(p) ? selected.filter((x) => x !== p) : [...selected, p]);
    setAnchor(p);
  };

  const rowMenu = (ev: MouseEvent, p: string) => {
    ev.preventDefault();
    ev.stopPropagation();
    if (!selected.includes(p)) setSelected([p]);
    setMenu({ x: ev.clientX, y: ev.clientY, bg: false });
  };
  const bgMenu = (ev: MouseEvent) => {
    if ((ev.target as HTMLElement).closest(".file")) return;
    ev.preventDefault();
    setSelected([]);
    setMenu({ x: ev.clientX, y: ev.clientY, bg: true });
  };

  // Keyboard: Delete, F2, Enter, Ctrl+A, Escape (not while typing or in a dialog).
  const keys = useRef<(e: KeyboardEvent) => void>(() => {});
  useEffect(() => {
    keys.current = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement;
      if (t.closest("input, textarea, select, dialog, .dialog, [contenteditable]") || preview || ask || moving || details) return;
      if (e.key === "Escape") {
        setSelected([]);
        setMenu(null);
      } else if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "a") {
        e.preventDefault();
        setSelected([...byPath.keys()]);
      } else if (e.key === "Delete" && selected.length && writable && !inTrash) {
        void trash(selected);
      } else if (e.key === "F2" && selected.length === 1 && writable && !inTrash) {
        e.preventDefault();
        setAsk({ kind: "rename", path: selected[0] });
      } else if (e.key === "Enter" && selected.length === 1) {
        open(selected[0]);
      }
    };
  });
  useEffect(() => {
    const on = (e: KeyboardEvent) => keys.current(e);
    window.addEventListener("keydown", on);
    return () => window.removeEventListener("keydown", on);
  }, []);

  // ---------- drag and drop ----------
  const dragStart = (ev: DragEvent, p: string) => {
    const paths = selected.includes(p) ? selected : [p];
    if (!selected.includes(p)) setSelected([p]);
    ev.dataTransfer.setData(DRAG_TYPE, JSON.stringify(paths));
    ev.dataTransfer.effectAllowed = "move";
  };
  /** Handlers that make a folder (row, tree node, breadcrumb) a drop target. */
  const dropProps = (dest: string) => ({
    onDragOver: (ev: DragEvent) => {
      if (!writable || !canReceive(dest)) return;
      const t = ev.dataTransfer.types;
      if (!t.includes(DRAG_TYPE) && !t.includes("Files")) return;
      ev.preventDefault();
      ev.stopPropagation();
      ev.dataTransfer.dropEffect = t.includes(DRAG_TYPE) ? "move" : "copy";
      if (dropOn !== dest) setDropOn(dest);
    },
    onDragLeave: (ev: DragEvent) => {
      if (!(ev.currentTarget as HTMLElement).contains(ev.relatedTarget as Node)) setDropOn((d) => (d === dest ? null : d));
    },
    onDrop: (ev: DragEvent) => {
      if (!writable || !canReceive(dest)) return;
      ev.preventDefault();
      ev.stopPropagation();
      setDropOn(null);
      const raw = ev.dataTransfer.getData(DRAG_TYPE);
      if (raw) void moveInto(JSON.parse(raw) as string[], dest);
      else if (ev.dataTransfer.files.length) enqueue(ev.dataTransfer.files, dest);
    },
    "data-drop": dropOn === dest ? "true" : undefined,
  });

  const one = selected.length === 1 ? byPath.get(selected[0]) : undefined;
  const selInTrash = selected.some((p) => restorePath(p, user));

  // Commands for the selection (menu and command bar share them).
  const commands: { id: string; label: string; icon: string; run: () => void; danger?: boolean; when: boolean }[] = [
    { id: "open", label: one?.dir ? "Abrir" : "Visualizar", icon: one?.dir ? "folder" : "eye", run: () => open(selected[0]), when: !!one },
    { id: "download", label: "Baixar", icon: "download", run: () => download(selected), when: selected.some((p) => !byPath.get(p)?.dir) },
    { id: "restore", label: "Restaurar", icon: "restore", run: () => void restore(selected), when: writable && selInTrash },
    { id: "rename", label: "Renomear", icon: "edit", run: () => setAsk({ kind: "rename", path: selected[0] }), when: writable && !!one && !inTrash && path !== "" },
    { id: "move", label: "Mover para…", icon: "move", run: () => setMoving(selected), when: writable && selected.length > 0 && !inTrash && path !== "" },
    { id: "details", label: "Detalhes", icon: "info", run: () => setDetails(selected[0]), when: !!one },
    { id: "delete", label: "Apagar", icon: "trash", run: () => void trash(selected), danger: true, when: writable && selected.length > 0 && !inTrash && path !== "" },
  ];
  const bgCommands = [
    { id: "mkdir", label: "Nova pasta", icon: "folderPlus", run: () => setAsk({ kind: "mkdir", at: path }), when: canAddHere },
    { id: "upload", label: "Enviar arquivos", icon: "upload", run: () => pickFiles(), when: canAddHere },
    { id: "refresh", label: "Atualizar", icon: "refresh", run: () => refresh(path), when: true },
    { id: "all", label: "Selecionar tudo", icon: "check", run: () => setSelected([...byPath.keys()]), when: entries.length > 0 && !compact },
  ];

  return (
    <div className="nuvem">
      <section className="card nuvem-top">
        <div className="nuvem-space">{summary}</div>
        <div className="nuvem-actions">
          <button className="btn" disabled={!canAddHere} onClick={() => setAsk({ kind: "mkdir", at: path })} title={canAddHere ? undefined : "Abra uma pasta sua ou a Compartilhado"}>
            <Icon name="folderPlus" /> Nova pasta
          </button>
          <button className="btn primary" disabled={!canAddHere} onClick={() => pickFiles()} title={canAddHere ? undefined : "Abra uma pasta sua ou a Compartilhado"}>
            <Icon name="upload" /> Enviar arquivos
          </button>
          {onConnect && !compact && (
            <button className="btn ghost" onClick={onConnect}>
              <Icon name="network" /> Acessar pelo computador
            </button>
          )}
          <input
            id={pickId}
            type="file"
            multiple
            hidden
            onChange={(e) => {
              if (e.target.files) enqueue(e.target.files);
              e.target.value = "";
            }}
          />
        </div>
      </section>

      <section className="card explorer">
        {!compact && (
          <aside className="tree" aria-label="Pastas">
            <FolderTree current={path} user={user} onOpen={onNavigate} dropProps={dropProps} />
          </aside>
        )}
        <div className="explorer-main" onContextMenu={bgMenu} {...(canAddHere ? dropProps(path) : {})} data-drop={dropOn === path ? "true" : undefined}>
          <div className="files-bar">
            <nav className="crumbs" aria-label="Pasta atual">
              {crumbs(path, user).map((c, i, all) =>
                i === all.length - 1 ? (
                  <span key={c.path} aria-current="page">
                    {c.name}
                  </span>
                ) : (
                  <button key={c.path} className={`crumb${dropOn === c.path ? " drop" : ""}`} onClick={() => onNavigate(c.path)} {...dropProps(c.path)}>
                    {c.name}
                  </button>
                ),
              )}
            </nav>
            <div className="files-tools">
              <label className="search">
                <Icon name="search" />
                <input className="input" placeholder="Filtrar nesta pasta" aria-label="Filtrar nesta pasta" value={filter} onChange={(e) => setFilter(e.target.value)} />
              </label>
              <div className="segmented" role="group" aria-label="Exibição">
                <button aria-pressed={view === "list"} title="Lista" aria-label="Lista" onClick={() => (setView("list"), saveView("list"))}>
                  <Icon name="list" />
                </button>
                <button aria-pressed={view === "grid"} title="Grade" aria-label="Grade" onClick={() => (setView("grid"), saveView("grid"))}>
                  <Icon name="grid" />
                </button>
              </div>
            </div>
          </div>

          {selected.length > 0 && !compact && (
            <div className="selbar" role="toolbar" aria-label="Seleção">
              <button className="btn ghost icon small" aria-label="Limpar seleção" onClick={() => setSelected([])}>
                <Icon name="x" />
              </button>
              <span className="selbar-count">{selected.length === 1 ? "1 selecionado" : `${selected.length} selecionados`}</span>
              {commands
                .filter((c) => c.when && c.id !== "details")
                .map((c) => (
                  <button key={c.id} className={`btn ghost small${c.danger ? " danger" : ""}`} onClick={c.run}>
                    <Icon name={c.icon} /> {c.label}
                  </button>
                ))}
            </div>
          )}

          {uploads.length > 0 && <Uploads uploads={uploads} setUploads={setUploads} />}

          {q.isError ? (
            <p className="notice error">{errText(q.error)}</p>
          ) : q.isLoading ? (
            <p className="muted files-empty">Carregando…</p>
          ) : entries.length === 0 ? (
            <div className="files-empty">
              <Icon name={inTrash ? "trash" : filter ? "search" : "folder"} />
              <strong>{filter ? "Nada com esse nome" : inTrash ? "A lixeira está vazia" : "Pasta vazia"}</strong>
              <span className="muted">
                {filter
                  ? "Tente outra parte do nome."
                  : inTrash
                    ? "Arquivos apagados ficam aqui por 30 dias."
                    : canAddHere
                      ? compact
                        ? "Toque em Enviar arquivos."
                        : "Arraste arquivos para cá ou use Enviar arquivos."
                      : "Nada por aqui ainda."}
              </span>
            </div>
          ) : (
            <>
              {view === "list" && (
                <div className="file-head" role="row">
                  {(["name", "modified", "size"] as SortKey[]).map((k) => (
                    <button
                      key={k}
                      className={`col-${k}${sort.key === k ? " on" : ""}`}
                      onClick={() => setSort((s) => ({ key: k, asc: s.key === k ? !s.asc : k === "name" }))}
                      aria-sort={sort.key === k ? (sort.asc ? "ascending" : "descending") : "none"}
                    >
                      {k === "name" ? "Nome" : k === "modified" ? "Modificado" : "Tamanho"}
                      {sort.key === k && <Icon name={sort.asc ? "up" : "down"} />}
                    </button>
                  ))}
                </div>
              )}
              <ul className={`file-list ${view}`} role="listbox" aria-multiselectable="true" aria-label="Arquivos">
                {entries.map((e) => {
                  const p = joinPath(path, e.name);
                  const isSel = selected.includes(p);
                  const drag = writable && !inTrash && path !== "";
                  const target = e.dir && drag ? dropProps(p) : {};
                  return (
                    <li key={e.name} role="option" aria-selected={isSel}>
                      <div
                        className={`file${isSel ? " selected" : ""}${dropOn === p ? " drop" : ""}`}
                        title={e.name}
                        draggable={drag && !compact}
                        onDragStart={(ev) => dragStart(ev, p)}
                        onClick={(ev) => clickRow(ev, p)}
                        onDoubleClick={() => open(p)}
                        onContextMenu={(ev) => rowMenu(ev, p)}
                        {...target}
                      >
                        {!compact && path !== "" && (
                          <span
                            className="file-check"
                            role="checkbox"
                            aria-checked={isSel}
                            aria-label={`Selecionar ${e.name}`}
                            onClick={(ev) => {
                              ev.stopPropagation();
                              toggleCheck(p);
                            }}
                          >
                            {isSel && <Icon name="check" />}
                          </span>
                        )}
                        <Thumb path={p} entry={e} />
                        <span className="file-name">{e.dir && path === "" ? label(e.name, user) : e.name}</span>
                        <span className="file-date muted small">{fmtDate(e.modified)}</span>
                        <span className="file-size muted small">{e.dir ? kindLabel.folder : fmtBytes(e.size)}</span>
                        {path !== "" && (
                          <button
                            className="btn ghost icon small file-more"
                            aria-label={`Ações para ${e.name}`}
                            onClick={(ev) => {
                              ev.stopPropagation();
                              const r = (ev.currentTarget as HTMLElement).getBoundingClientRect();
                              setSelected([p]);
                              setMenu({ x: r.right, y: r.bottom, bg: false });
                            }}
                          >
                            <Icon name="dots" />
                          </button>
                        )}
                      </div>
                    </li>
                  );
                })}
              </ul>
            </>
          )}
          {inTrash && entries.length > 0 && <p className="muted small files-foot">Itens da lixeira somem sozinhos 30 dias depois de apagados.</p>}
        </div>
      </section>

      {menu && (
        <ContextMenu
          at={menu}
          sheet={compact}
          title={!menu.bg && one ? one.name : undefined}
          items={(menu.bg ? bgCommands : commands).filter((c) => c.when)}
          onClose={() => setMenu(null)}
        />
      )}

      {ask && (
        <NameDialog
          ask={ask}
          onCancel={() => setAsk(null)}
          onSubmit={async (value) => {
            const ok =
              ask.kind === "mkdir"
                ? await run(() => mkdir(joinPath(ask.at, value)), "Pasta criada.", [ask.at])
                : await run(() => move(ask.path, joinPath(parentPath(ask.path), value)), "Nome alterado.");
            if (ok) {
              setAsk(null);
              if (ask.kind === "rename") setSelected([joinPath(parentPath(ask.path), value)]);
            }
          }}
        />
      )}

      {moving && (
        <MoveDialog
          paths={moving}
          user={user}
          start={path}
          onCancel={() => setMoving(null)}
          onMove={async (dest) => {
            if (await moveInto(moving, dest)) setMoving(null);
          }}
        />
      )}

      {preview && byPath.get(preview) && (
        <PreviewDialog
          path={preview}
          entry={byPath.get(preview)!}
          files={files}
          byPath={byPath}
          writable={writable && !inTrash}
          onGo={setPreview}
          onClose={() => setPreview(null)}
          onRename={() => setAsk({ kind: "rename", path: preview })}
          onMove={() => setMoving([preview])}
          onDelete={() => void trash([preview])}
        />
      )}

      {details && byPath.get(details) && (
        <DetailsDialog path={details} entry={byPath.get(details)!} user={user} onClose={() => setDetails(null)} />
      )}
    </div>
  );
}

function folderName(p: string, user: string) {
  if (!p) return "Nuvem";
  const parts = p.split("/");
  return parts.length === 1 ? label(parts[0], user) : parts[parts.length - 1];
}

function Uploads({ uploads, setUploads }: { uploads: Upload[]; setUploads: (f: (u: Upload[]) => Upload[]) => void }) {
  const active = uploads.filter((u) => u.state === "sending" || u.state === "waiting").length;
  return (
    <div className="uploads-box">
      <div className="uploads-head">
        <strong>{active ? `Enviando ${active} ${active === 1 ? "arquivo" : "arquivos"}` : "Envios"}</strong>
        {!active && (
          <button className="btn ghost small" onClick={() => setUploads(() => [])}>
            Limpar
          </button>
        )}
      </div>
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
                onClick={() => setUploads((all) => all.map((x) => (x.id === u.id ? { ...x, state: "waiting", error: undefined, ctrl: new AbortController() } : x)))}
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
    </div>
  );
}

// ---------- folder tree ----------

type DropProps = (dest: string) => object;

function FolderTree({
  current,
  user,
  onOpen,
  dropProps,
  pick,
}: {
  current: string;
  user: string;
  onOpen: (p: string) => void;
  dropProps?: DropProps;
  /** Picker mode (Mover para…): these paths cannot be picked. */
  pick?: { exclude: string[] };
}) {
  // Ancestors of the current folder start open.
  const [open, setOpen] = useState<Set<string>>(() => {
    const s = new Set<string>();
    const parts = current.split("/");
    for (let i = 1; i <= parts.length; i++) s.add(parts.slice(0, i).join("/"));
    return s;
  });
  const toggle = (p: string) =>
    setOpen((s) => {
      const n = new Set(s);
      if (n.has(p)) n.delete(p);
      else n.add(p);
      return n;
    });
  return (
    <ul className="tree-list" role="tree">
      <TreeLevel parent="" depth={0} current={current} user={user} open={open} toggle={toggle} onOpen={onOpen} dropProps={dropProps} pick={pick} />
    </ul>
  );
}

function TreeLevel({
  parent,
  depth,
  current,
  user,
  open,
  toggle,
  onOpen,
  dropProps,
  pick,
}: {
  parent: string;
  depth: number;
  current: string;
  user: string;
  open: Set<string>;
  toggle: (p: string) => void;
  onOpen: (p: string) => void;
  dropProps?: DropProps;
  pick?: { exclude: string[] };
}) {
  const q = useQuery({ queryKey: ["nas", parent], queryFn: () => listDir(parent), retry: 0, staleTime: 15_000 });
  let dirs = (q.data?.entries ?? []).filter((e) => e.dir);
  // The bin goes last at the top level, and is not a destination.
  if (parent === "") dirs = [...dirs.filter((e) => e.name !== TRASH), ...(pick ? [] : dirs.filter((e) => e.name === TRASH))];
  dirs = sortEntries(dirs, "name", true);
  if (parent === "") dirs = [...dirs.filter((e) => e.name === user), ...dirs.filter((e) => e.name !== user && e.name !== TRASH), ...dirs.filter((e) => e.name === TRASH)];
  if (q.isLoading) return <li className="tree-loading muted small" style={{ paddingLeft: 12 + depth * 16 }}>Carregando…</li>;
  return (
    <>
      {dirs.map((d) => {
        const p = joinPath(parent, d.name);
        const isOpen = open.has(p);
        const blocked = !!pick && pick.exclude.some((x) => inside(p, x));
        // The bin's per-user level is an implementation detail: it opens straight through.
        const name = parent === "" ? label(d.name, user) : d.name;
        const icon = parent === "" && d.name === TRASH ? "trash" : parent === "" && d.name === user ? "home" : parent === "" && d.name === "Compartilhado" ? "users" : "folder";
        return (
          <li key={p} role="treeitem" aria-expanded={isOpen} aria-selected={current === p}>
            <div className={`tree-row${current === p ? " on" : ""}${blocked ? " blocked" : ""}`} style={{ paddingLeft: 6 + depth * 16 }} {...(dropProps && !pick ? dropProps(p) : {})}>
              <button className={`tree-twist${isOpen ? " open" : ""}`} aria-label={isOpen ? `Fechar ${name}` : `Abrir ${name}`} onClick={() => toggle(p)} tabIndex={-1}>
                <Icon name="chevronRight" />
              </button>
              <button className="tree-name" disabled={blocked} onClick={() => onOpen(p)} onDoubleClick={() => toggle(p)} title={name}>
                <Icon name={icon} /> <span>{name}</span>
              </button>
            </div>
            {isOpen && (
              <ul role="group">
                <TreeLevel parent={p} depth={depth + 1} current={current} user={user} open={open} toggle={toggle} onOpen={onOpen} dropProps={dropProps} pick={pick} />
              </ul>
            )}
          </li>
        );
      })}
    </>
  );
}

// ---------- menus and dialogs ----------

function ContextMenu({
  at,
  items,
  sheet,
  title,
  onClose,
}: {
  at: Menu;
  items: { id: string; label: string; icon: string; run: () => void; danger?: boolean }[];
  sheet: boolean;
  title?: string;
  onClose: () => void;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const [pos, setPos] = useState({ left: at.x, top: at.y });
  useEffect(() => {
    const el = ref.current;
    if (el && !sheet) {
      const r = el.getBoundingClientRect();
      setPos({ left: Math.max(8, Math.min(at.x, innerWidth - r.width - 8)), top: Math.max(8, Math.min(at.y, innerHeight - r.height - 8)) });
    }
    el?.querySelector("button")?.focus();
    const close = (e: Event) => {
      if (e instanceof KeyboardEvent && e.key !== "Escape") return;
      if (e.type === "pointerdown" && ref.current?.contains(e.target as Node)) return;
      onClose();
    };
    window.addEventListener("pointerdown", close, true);
    window.addEventListener("keydown", close);
    window.addEventListener("resize", onClose);
    window.addEventListener("scroll", onClose, true);
    return () => {
      window.removeEventListener("pointerdown", close, true);
      window.removeEventListener("keydown", close);
      window.removeEventListener("resize", onClose);
      window.removeEventListener("scroll", onClose, true);
    };
  }, [at, sheet, onClose]);
  if (!items.length) return null;
  const list = (
    <div ref={ref} className={`ctx-menu${sheet ? " sheet-menu" : ""}`} role="menu" style={sheet ? undefined : pos} onContextMenu={(e) => e.preventDefault()}>
      {title && <div className="ctx-title">{title}</div>}
      {items.map((c, i) => (
        <button
          key={c.id}
          role="menuitem"
          className={`${c.danger ? "danger" : ""}${c.danger && i > 0 ? " sep" : ""}`}
          onClick={() => {
            onClose();
            c.run();
          }}
          onKeyDown={(e) => {
            const btns = [...(ref.current?.querySelectorAll("button") ?? [])];
            const n = btns.indexOf(e.currentTarget);
            if (e.key === "ArrowDown") btns[(n + 1) % btns.length]?.focus();
            if (e.key === "ArrowUp") btns[(n - 1 + btns.length) % btns.length]?.focus();
          }}
        >
          <Icon name={c.icon} /> {c.label}
        </button>
      ))}
    </div>
  );
  return sheet ? <div className="dialog-backdrop">{list}</div> : list;
}

function Dialog({ title, onClose, children, wide, className }: { title: string; onClose: () => void; children: ReactNode; wide?: boolean; className?: string }) {
  useEffect(() => {
    const k = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", k);
    return () => window.removeEventListener("keydown", k);
  }, [onClose]);
  return (
    <div className="dialog-backdrop" role="dialog" aria-modal="true" aria-label={title} onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className={`dialog${wide ? " wide" : ""}${className ? ` ${className}` : ""}`}>{children}</div>
    </div>
  );
}

function NameDialog({ ask, onCancel, onSubmit }: { ask: Ask; onCancel: () => void; onSubmit: (v: string) => void }) {
  const initial = ask.kind === "rename" ? baseName(ask.path) : "";
  const [value, setValue] = useState(initial);
  const title = ask.kind === "mkdir" ? "Nova pasta" : "Renomear";
  const inputRef = useRef<HTMLInputElement>(null);
  useEffect(() => {
    const el = inputRef.current;
    if (!el) return;
    el.focus();
    // Rename selects the name without the extension, like a file manager.
    const dot = initial.lastIndexOf(".");
    el.setSelectionRange(0, dot > 0 ? dot : initial.length);
  }, [initial]);
  const invalid = /[/\\]/.test(value) || value.trim() === "." || value.trim() === "..";
  return (
    <Dialog title={title} onClose={onCancel}>
      <form
        className="stack-sm"
        onSubmit={(e) => {
          e.preventDefault();
          if (value.trim() && !invalid && value.trim() !== initial) onSubmit(value.trim());
        }}
      >
        <h2>{title}</h2>
        <input ref={inputRef} className="input" value={value} onChange={(e) => setValue(e.target.value)} aria-label={title} placeholder={ask.kind === "mkdir" ? "Nome da pasta" : undefined} />
        {invalid && <span className="hint bad">O nome não pode ter / nem \.</span>}
        <div className="dialog-actions">
          <button type="button" className="btn ghost" onClick={onCancel}>
            Cancelar
          </button>
          <button type="submit" className="btn primary" disabled={!value.trim() || invalid || value.trim() === initial}>
            {ask.kind === "mkdir" ? "Criar" : "Renomear"}
          </button>
        </div>
      </form>
    </Dialog>
  );
}

function MoveDialog({ paths, user, start, onCancel, onMove }: { paths: string[]; user: string; start: string; onCancel: () => void; onMove: (dest: string) => void }) {
  const [dest, setDest] = useState(start);
  const [busy, setBusy] = useState(false);
  const bad = !canReceive(dest) || paths.some((p) => inside(dest, p)) || paths.every((p) => parentPath(p) === dest);
  return (
    <Dialog title="Mover para" onClose={onCancel} className="move-dialog">
      <h2>Mover {paths.length === 1 ? `“${baseName(paths[0])}”` : `${paths.length} itens`}</h2>
      <p className="muted small">Escolha a pasta de destino.</p>
      <div className="move-tree">
        <FolderTree current={dest} user={user} onOpen={setDest} pick={{ exclude: paths }} />
      </div>
      <p className="small move-dest">
        Destino: <strong>{dest ? crumbs(dest, user).slice(1).map((c) => c.name).join(" / ") : "—"}</strong>
      </p>
      <div className="dialog-actions">
        <button className="btn ghost" onClick={onCancel}>
          Cancelar
        </button>
        <button
          className="btn primary"
          disabled={bad || busy}
          onClick={() => {
            setBusy(true);
            Promise.resolve(onMove(dest)).finally(() => setBusy(false));
          }}
        >
          <Icon name="move" /> Mover aqui
        </button>
      </div>
    </Dialog>
  );
}

function DetailsDialog({ path, entry, user, onClose }: { path: string; entry: NasEntry; user: string; onClose: () => void }) {
  const from = restorePath(path, user);
  return (
    <Dialog title="Detalhes" onClose={onClose}>
      <div className="dialog-head">
        <h2 className="wrap-anywhere">{entry.name}</h2>
        <button className="btn ghost icon small" aria-label="Fechar" onClick={onClose}>
          <Icon name="x" />
        </button>
      </div>
      <dl className="kv details-kv">
        <dt>Tipo</dt>
        <dd>{kindLabel[kindOf(entry)]}</dd>
        {!entry.dir && (
          <>
            <dt>Tamanho</dt>
            <dd>{fmtBytes(entry.size)}</dd>
          </>
        )}
        <dt>Modificado</dt>
        <dd>{new Date(entry.modified).toLocaleString("pt-BR")}</dd>
        <dt>Local</dt>
        <dd>{crumbs(parentPath(path), user).map((c) => c.name).join(" / ")}</dd>
        {from && (
          <>
            <dt>Veio de</dt>
            <dd>{crumbs(parentPath(from), user).map((c) => c.name).join(" / ")}</dd>
          </>
        )}
      </dl>
    </Dialog>
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
const TEXT_MAX = 2 << 20;

/** Large viewer: images, video, audio, PDF and text; ← → walk the folder. */
function PreviewDialog({
  path,
  entry,
  files,
  byPath,
  writable,
  onGo,
  onClose,
  onRename,
  onMove,
  onDelete,
}: {
  path: string;
  entry: NasEntry;
  files: string[];
  byPath: Map<string, NasEntry>;
  writable: boolean;
  onGo: (p: string) => void;
  onClose: () => void;
  onRename: () => void;
  onMove: () => void;
  onDelete: () => void;
}) {
  const toast = useToast();
  const [sharing, setSharing] = useState(false);
  const i = files.indexOf(path);
  const prev = i > 0 ? files[i - 1] : null;
  const next = i >= 0 && i < files.length - 1 ? files[i + 1] : null;
  const kind = kindOf(entry);
  const src = fileUrl(path);

  useEffect(() => {
    const k = (e: KeyboardEvent) => {
      if ((e.target as HTMLElement).closest("input, textarea")) return;
      if (e.key === "Escape") onClose();
      if (e.key === "ArrowLeft" && prev) onGo(prev);
      if (e.key === "ArrowRight" && next) onGo(next);
    };
    window.addEventListener("keydown", k);
    return () => window.removeEventListener("keydown", k);
  }, [prev, next, onGo, onClose]);

  return (
    <div className="dialog-backdrop viewer-backdrop" role="dialog" aria-modal="true" aria-label={entry.name} onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="viewer">
        <header className="viewer-head">
          <span className={`file-icon small k-${kind}`}>
            <Icon name={kindIcon[kind]} />
          </span>
          <div className="viewer-title">
            <h2 title={entry.name}>{entry.name}</h2>
            <span className="muted small">
              {kindLabel[kind]} · {fmtBytes(entry.size)} · {fmtDate(entry.modified)}
              {files.length > 1 && i >= 0 && ` · ${i + 1} de ${files.length}`}
            </span>
          </div>
          <div className="viewer-actions">
            <a className="btn small" href={fileUrl(path, true)} download={entry.name}>
              <Icon name="download" /> <span className="hide-sm">Baixar</span>
            </a>
            {entry.size <= SHARE_MAX && "share" in navigator && (
              <button
                className="btn small"
                disabled={sharing}
                onClick={() => {
                  setSharing(true);
                  shareFile(path, entry.name)
                    .then((ok) => ok || toast("Este aparelho não compartilha arquivos; use Baixar.", true))
                    .catch((e) => (e as Error).name !== "AbortError" && toast(errText(e), true))
                    .finally(() => setSharing(false));
                }}
              >
                <Icon name="share" /> <span className="hide-sm">{sharing ? "Preparando…" : "Compartilhar"}</span>
              </button>
            )}
            {kind === "pdf" && (
              <a className="btn small hide-sm" href={src} target="_blank" rel="noreferrer">
                <Icon name="open" /> Nova aba
              </a>
            )}
            {writable && (
              <>
                <button className="btn ghost icon small" title="Renomear" aria-label="Renomear" onClick={onRename}>
                  <Icon name="edit" />
                </button>
                <button className="btn ghost icon small" title="Mover para…" aria-label="Mover" onClick={onMove}>
                  <Icon name="move" />
                </button>
                <button className="btn ghost icon small danger" title="Apagar" aria-label="Apagar" onClick={onDelete}>
                  <Icon name="trash" />
                </button>
              </>
            )}
            <button className="btn ghost icon small" aria-label="Fechar" title="Fechar (Esc)" onClick={onClose}>
              <Icon name="x" />
            </button>
          </div>
        </header>
        <div className={`viewer-stage k-${kind}`}>
          {prev && (
            <button className="viewer-nav prev" aria-label={`Anterior: ${byPath.get(prev)?.name}`} title="Anterior (←)" onClick={() => onGo(prev)}>
              <Icon name="chevronLeft" />
            </button>
          )}
          <PreviewBody key={path} kind={kind} src={src} path={path} entry={entry} />
          {next && (
            <button className="viewer-nav next" aria-label={`Próximo: ${byPath.get(next)?.name}`} title="Próximo (→)" onClick={() => onGo(next)}>
              <Icon name="chevronRight" />
            </button>
          )}
        </div>
      </div>
    </div>
  );
}

function PreviewBody({ kind, src, path, entry }: { kind: Kind; src: string; path: string; entry: NasEntry }) {
  if (kind === "image") return <ZoomImage src={src} alt={entry.name} />;
  if (kind === "video") return <video className="viewer-media" src={src} controls autoPlay playsInline preload="metadata" />;
  if (kind === "audio")
    return (
      <div className="viewer-audio">
        <span className="file-icon big k-audio">
          <Icon name="music" />
        </span>
        <audio src={src} controls autoPlay preload="metadata" />
      </div>
    );
  if (kind === "pdf") return <iframe className="viewer-pdf" src={src} title={entry.name} />;
  if (kind === "text" && entry.size <= TEXT_MAX) return <TextPreview path={path} />;
  return (
    <div className="viewer-none">
      <span className={`file-icon big k-${kind}`}>
        <Icon name={kindIcon[kind]} />
      </span>
      <strong>Sem pré-visualização para este tipo de arquivo</strong>
      <span className="muted">Baixe para abrir no aparelho.</span>
      <a className="btn primary" href={fileUrl(path, true)} download={entry.name}>
        <Icon name="download" /> Baixar ({fmtBytes(entry.size)})
      </a>
    </div>
  );
}

function TextPreview({ path }: { path: string }) {
  const q = useQuery({
    queryKey: ["nas-text", path],
    queryFn: async () => {
      const res = await fetch(fileUrl(path, true), { credentials: "same-origin" });
      if (!res.ok) throw new ApiError(res.status, `HTTP ${res.status}`);
      return new TextDecoder("utf-8").decode(await res.arrayBuffer());
    },
    staleTime: 60_000,
  });
  if (q.isLoading) return <p className="viewer-none muted">Carregando…</p>;
  if (q.isError) return <p className="viewer-none">{errText(q.error)}</p>;
  return <pre className="viewer-text selectable">{q.data}</pre>;
}

/** Photo preview: pinch to zoom and pan on touch screens, double-tap to reset. */
function ZoomImage({ src, alt }: { src: string; alt: string }) {
  const ref = useRef<HTMLDivElement>(null);
  const pz = usePinchZoom(ref);
  return (
    <div ref={ref} className={`zoom-frame viewer-image${pz.zoomed ? " zoomed" : ""}`} {...pz.handlers}>
      <img src={src} alt={alt} style={pz.style} draggable={false} />
    </div>
  );
}
