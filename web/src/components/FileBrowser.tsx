import { useEffect, useId, useMemo, useRef, useState, type DragEvent, type MouseEvent, type ReactNode } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError } from "../lib/api";
import { usePinchZoom } from "../lib/pinch";
import {
  SHARED,
  TRASH,
  baseName,
  createEmpty,
  crumbs,
  fileUrl,
  hasThumb,
  inside,
  isImage,
  joinPath,
  kindLabel,
  kindOf,
  label,
  listDir,
  matchesFilters,
  mkdir,
  move,
  parentPath,
  purge,
  remove,
  restorePath,
  saveText,
  sortEntries,
  thumbUrl,
  uploadFile,
  foldersOf,
  pickedFromDrop,
  pickedFromList,
  renameTop,
  PARALLEL_UPLOADS,
  SpeedMeter,
  easeToward,
  fmtBytesFine,
  fmtRemaining,
  SMALL_UPLOAD,
  type DateFilter,
  type Picked,
  type Kind,
  type NasEntry,
  type SortKey,
  type TypeFilter,
} from "../lib/nas";
import { fmtBytes, fmtDate } from "../lib/time";
import { useSession, useToast } from "../lib/session";
import { isPhone, useCompact } from "../lib/viewport";
import { Icon } from "./Icon";
import { Avatar } from "./Avatar";
import { FileIcon, fileType } from "./FileIcon";

import { getLang, plural, t } from "../lib/i18n";
type View = "list" | "grid";
const VIEW_KEY = "sentinela.cloud.view";
const PANE_KEY = "sentinela.cloud.details";
// Items dragged inside the Cloud carry their paths under this type.
const DRAG_TYPE = "application/x-sentinela-cloud";
// Small images (GIF/WebP, no server thumbnail) are shown as they are.
const INLINE_THUMB_MAX = 3 << 20;

function loadPref(key: string, fallback: string): string {
  try {
    return localStorage.getItem(key) ?? fallback;
  } catch {
    return fallback;
  }
}

function savePref(key: string, v: string) {
  try {
    localStorage.setItem(key, v);
  } catch {
    /* private mode: not remembered */
  }
}

interface Upload {
  id: number;
  name: string;
  /** Top folder when the file came in a folder upload. */
  group?: string;
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
    if (e.status === 409) return t("An item with this name already exists here.");
    if (e.status === 403) return t("You do not have permission for that.");
    if (e.status === 413) return t("File too large.");
    if (e.status === 507) return t("There is no free space in Cloud.");
    if (e.status === 503) return t("Cloud is not available on this server.");
    if (e.status === 404) return t("The item no longer exists.");
  }
  return (e as Error).message || t("Something went wrong.");
};



/** Shows a video's first frame only once it scrolls into view. */
function VideoThumb({ src }: { src: string }) {
  const ref = useRef<HTMLSpanElement>(null);
  const [seen, setSeen] = useState(false);
  useEffect(() => {
    const el = ref.current;
    if (!el || seen) return;
    const io = new IntersectionObserver((es) => es.some((e) => e.isIntersecting) && setSeen(true), { rootMargin: "100px" });
    io.observe(el);
    return () => io.disconnect();
  }, [seen]);
  return (
    <span ref={ref} className="file-thumb video-thumb">
      {seen && <video src={`${src}#t=1`} preload="metadata" muted playsInline tabIndex={-1} />}
      <Icon name="play" />
    </span>
  );
}

/** The drawn icon for an entry; top-level folders show whose they are. */
function EntryIcon({ path, entry, owner, big }: { path: string; entry: NasEntry; owner?: string; big?: boolean }) {
  const top = !path.includes("/");
  const t = fileType(entry.name, entry.dir, { shared: top && entry.name === SHARED, trash: top && entry.name === TRASH, user: top && !!owner });
  return <FileIcon type={t} name={entry.name} owner={owner} big={big} />;
}

function Thumb({ path, entry, rich, owner }: { path: string; entry: NasEntry; rich?: boolean; owner?: string }) {
  const [failed, setFailed] = useState(false);
  const k = kindOf(entry);
  if (!failed && !entry.dir && hasThumb(entry.name)) {
    return <img className="file-thumb" src={thumbUrl(path)} alt="" loading="lazy" decoding="async" onError={() => setFailed(true)} draggable={false} />;
  }
  if (!failed && rich && k === "image" && entry.size <= INLINE_THUMB_MAX) {
    return <img className="file-thumb" src={fileUrl(path)} alt="" loading="lazy" decoding="async" onError={() => setFailed(true)} draggable={false} />;
  }
  if (rich && k === "video") return <VideoThumb src={fileUrl(path)} />;
  return (
    <span className="file-icon drawn">
      <EntryIcon path={path} entry={entry} owner={owner} big={rich} />
    </span>
  );
}


/** A folder can receive items: not the share root, not the bin. */
const canReceive = (p: string) => p !== "" && p !== TRASH && !p.startsWith(`${TRASH}/`);

type Ask = { kind: "mkdir"; at: string } | { kind: "mkfile"; at: string } | { kind: "rename"; path: string };
interface Cmd {
  id: string;
  label: string;
  icon: string;
  run: () => void;
  danger?: boolean;
  when: boolean;
}
type Menu = { x: number; y: number; items: Cmd[]; title?: string };

const TYPE_FILTERS: { v: TypeFilter; label: string }[] = [
  { v: "", label: t("All types") },
  { v: "folder", label: t("Folders") },
  { v: "image", label: t("Images") },
  { v: "video", label: t("Videos") },
  { v: "audio", label: t("Audio") },
  { v: "doc", label: t("Documents") },
  { v: "other", label: t("Other") },
];
const DATE_FILTERS: { v: DateFilter; label: string }[] = [
  { v: "", label: t("Any date") },
  { v: "today", label: t("Today") },
  { v: "7d", label: t("Last 7 days") },
  { v: "30d", label: t("Last 30 days") },
  { v: "year", label: t("This year") },
];

export function FileBrowser({
  path,
  onNavigate,
  canWrite,
  summary,
}: {
  path: string;
  onNavigate: (p: string) => void;
  canWrite: boolean;
  /** Space line for the top card. */
  summary?: ReactNode;
}) {
  const qc = useQueryClient();
  const toast = useToast();
  const compact = useCompact();
  const admin = useSession().can("admin");
  const q = useQuery({ queryKey: ["nas", path], queryFn: () => listDir(path), retry: 0 });
  const user = q.data?.user ?? "";
  const owners = q.data?.owners ?? {};
  const writable = canWrite && (q.data?.can_write ?? false);
  const inTrash = path === TRASH || path.startsWith(`${TRASH}/`);
  const canAddHere = writable && canReceive(path);
  // Phones start in the grid (thumbnails are easier to tap); each kind of
  // screen remembers its own choice.
  const viewKey = isPhone() ? `${VIEW_KEY}.phone` : VIEW_KEY;
  const [view, setView] = useState<View>(() => (loadPref(viewKey, isPhone() ? "grid" : "list") === "grid" ? "grid" : "list"));
  const [pane, setPane] = useState(() => loadPref(PANE_KEY, "1") === "1");
  const [sort, setSort] = useState<{ key: SortKey; asc: boolean }>({ key: "name", asc: true });
  const [filter, setFilter] = useState("");
  const [type, setType] = useState<TypeFilter>("");
  const [date, setDate] = useState<DateFilter>("");
  const [sel, setSel] = useState<{ folder: string; paths: string[] }>({ folder: path, paths: [] });
  const [anchor, setAnchor] = useState<string | null>(null);
  const [menu, setMenu] = useState<Menu | null>(null);
  const [ask, setAsk] = useState<Ask | null>(null);
  // Phones: one floating "+" opens the add actions (no big button row).
  const [adding, setAdding] = useState(false);
  const [filterSheet, setFilterSheet] = useState(false);
  const [moving, setMoving] = useState<string[] | null>(null);
  const [preview, setPreview] = useState<{ path: string; edit?: boolean } | null>(null);
  const [pending, setPending] = useState<{ path: string; edit?: boolean } | null>(null);
  const [details, setDetails] = useState<string | null>(null);
  const [uploads, setUploads] = useState<Upload[]>([]);
  const [dropOn, setDropOn] = useState<string | null>(null);
  const [treeKey, setTreeKey] = useState(0);
  const pickId = useId();
  const folderPickId = useId();
  const uploadDir = useRef(path);
  // The hidden file inputs are clicked by id: menus and buttons share them.
  const pickFiles = (dir = path) => {
    uploadDir.current = dir;
    (document.getElementById(pickId) as HTMLInputElement | null)?.click();
  };
  const pickFolder = (dir = path) => {
    uploadDir.current = dir;
    (document.getElementById(folderPickId) as HTMLInputElement | null)?.click();
  };
  // Phones have no folder picker (Android's file chooser returns files only).
  const folderUploads = !isPhone();
  const sending = useRef(new Set<number>());

  // Selection belongs to the folder it was made in.
  const selected = useMemo(() => (sel.folder === path ? sel.paths : []), [sel, path]);
  const setSelected = (paths: string[]) => setSel({ folder: path, paths });

  const refresh = (...paths: string[]) => new Set(paths).forEach((p) => void qc.invalidateQueries({ queryKey: ["nas", p] }));

  const all = useMemo(() => sortEntries(q.data?.entries ?? [], sort.key, sort.asc), [q.data, sort]);
  const entries = useMemo(() => {
    const f = filter.trim().toLowerCase();
    return all.filter((e) => (!f || e.name.toLowerCase().includes(f)) && matchesFilters(e, type, date));
  }, [all, filter, type, date]);
  const filtered = entries.length !== all.length;
  const byPath = useMemo(() => new Map(all.map((e) => [joinPath(path, e.name), e])), [all, path]);
  const files = entries.filter((e) => !e.dir).map((e) => joinPath(path, e.name));

  // A file opened from the tree (or just created) previews once its folder is listed.
  if (pending && byPath.has(pending.path)) {
    setPreview(pending);
    setSel({ folder: path, paths: [pending.path] });
    setPending(null);
  }

  // Folder listings touched by finished uploads, refreshed together at most
  // every 1.5 s: a folder of 500 photos must not re-list the folder 500 times.
  const toRefresh = useRef(new Set<string>());
  const refreshTimer = useRef<ReturnType<typeof setTimeout>>(undefined);
  const refreshSoon = (...paths: string[]) => {
    paths.forEach((p) => toRefresh.current.add(p));
    refreshTimer.current ??= setTimeout(() => {
      refreshTimer.current = undefined;
      refresh(...toRefresh.current);
      toRefresh.current.clear();
    }, 1500);
  };

  // Large files go one at a time (one already fills the link and the disk);
  // smaller ones up to PARALLEL_UPLOADS at a time, since a folder of photos
  // is limited by round trips, not bandwidth. Chunks resume after a drop.
  useEffect(() => {
    const big = (u: Upload) => u.file.size >= SMALL_UPLOAD;
    const running = uploads.filter((u) => sending.current.has(u.id));
    for (const next of uploads) {
      if (next.state !== "waiting" || sending.current.has(next.id)) continue;
      const room = running.length === 0 || (running.length < PARALLEL_UPLOADS && !big(next) && !running.some(big));
      if (!room) break;
      running.push(next);
      sending.current.add(next.id);
      const patch = (p: Partial<Upload>) => setUploads((all) => all.map((u) => (u.id === next.id ? { ...u, ...p } : u)));
      patch({ state: "sending" });
      uploadFile(next.file, next.target, ({ sent, total }) => patch({ sent, total }), next.ctrl.signal)
        .then(() => {
          patch({ state: "done", sent: next.file.size });
          refreshSoon(parentPath(next.target), path);
        })
        .catch((e) => patch(next.ctrl.signal.aborted ? { state: "cancelled" } : { state: "error", error: errText(e) }))
        .finally(() => {
          sending.current.delete(next.id);
          setUploads((all) => [...all]); // wake the queue
        });
    }
  }, [uploads]); // eslint-disable-line react-hooks/exhaustive-deps

  const enqueue = async (picked: Picked[], dir = path) => {
    if (!picked.length) return;
    // A folder that already exists here is sent as "Nome (2)".
    let taken = new Set<string>();
    if (picked.some((p) => p.rel.includes("/"))) {
      try {
        taken = new Set((dir === path ? all : (await listDir(dir)).entries).map((e) => e.name));
      } catch {
        /* the mkdir below reports the problem */
      }
    }
    const list = renameTop(picked, taken);
    const folders = foldersOf(list);
    for (const f of folders) {
      try {
        await mkdir(joinPath(dir, f));
      } catch (e) {
        if (!(e instanceof ApiError && e.status === 409)) {
          toast(t("Could not create the folder {f}: {v1}", { f, v1: errText(e) }), true);
          return;
        }
      }
    }
    if (folders.length) refresh(dir);
    const stamp = Date.now();
    setUploads((prev) => [
      ...prev.filter((u) => u.state !== "done" && u.state !== "cancelled"),
      ...list.map((p, i) => ({
        id: stamp + i,
        name: p.file.name,
        group: p.rel.includes("/") ? p.rel.split("/")[0] : undefined,
        target: joinPath(dir, p.rel),
        file: p.file,
        sent: 0,
        total: p.file.size,
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
    if (done) toast(done === 1 ? t("Moved to {v0}.", { v0: folderName(dest, user) }) : t("{done} items moved to {v1}.", { done, v1: folderName(dest, user) }));
    setSelected([]);
    return done > 0;
  };

  const trash = async (paths: string[]) => {
    if (!paths.length) return;
    const what = paths.length === 1 ? `“${paths[0].includes("/") ? baseName(paths[0]) : label(paths[0], user)}”` : t("{length} items", { length: paths.length });
    // Top-level folders (admins): the user's own and Compartilhado come back empty.
    const top = paths.some((p) => !p.includes("/"));
    const note = top ? t(" The main folders (yours and Shared) come back empty.") : "";
    if (!window.confirm(t("Move {what} to the trash, with everything inside? It keeps everything for 30 days.{note}", { what, note }))) return;
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
    refresh(path, TRASH, ...paths.map(parentPath));
    if (done) toast(done === 1 ? t("Moved to trash.") : t("{done} items moved to trash.", { done }));
    setSelected([]);
    setPreview(null);
  };

  const myBin = `${TRASH}/${user}`;
  const purgeForGood = async (paths: string[]) => {
    const what = paths.length === 1 ? `“${baseName(paths[0])}”` : t("{length} items", { length: paths.length });
    if (!window.confirm(t("Delete {what} forever? This cannot be undone.", { what }))) return;
    for (const p of paths) {
      if (!(await run(() => purge(p), "", [path, TRASH, myBin]))) return;
    }
    toast(paths.length === 1 ? t("Permanently deleted.") : t("{length} items deleted forever.", { length: paths.length }));
    setSelected([]);
  };
  const emptyBin = async () => {
    if (!window.confirm(t("Empty the trash? Everything in it is deleted forever. This cannot be undone."))) return;
    if (await run(() => purge(myBin), t("Trash emptied."), [path, TRASH, myBin])) {
      setSelected([]);
      if (path.startsWith(`${myBin}/`)) onNavigate(TRASH);
    }
  };

  const restore = async (paths: string[]) => {
    for (const p of paths) {
      const to = restorePath(p, user);
      if (to && !(await run(() => move(p, to), t("Restored to {folder}.", { folder: folderName(parentPath(to), user) }), [path, parentPath(to)]))) break;
    }
    setSelected([]);
  };

  const download = (paths: string[]) => {
    paths.forEach((p, i) =>
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
    else setPreview({ path: p });
  };
  /** Opens a file from anywhere (tree): go to its folder, then preview it. */
  const openFile = (p: string, edit = false) => {
    if (parentPath(p) === path && byPath.has(p)) setPreview({ path: p, edit });
    else {
      setPending({ path: p, edit });
      onNavigate(parentPath(p));
    }
  };

  // ---------- selection ----------
  const clickRow = (ev: MouseEvent, p: string) => {
    if (compact) {
      open(p);
      return;
    }
    if (ev.shiftKey && anchor && byPath.has(anchor)) {
      const keys = entries.map((e) => joinPath(path, e.name));
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

  const inShare = path !== "";
  // At the share root only admins change folders (never the bin itself).
  const canEdit = (paths: string[]) => writable && !inTrash && (inShare || admin) && !paths.includes(TRASH);

  // Commands for the selection (menu, selection bar and details pane share them).
  const commands = (paths: string[]): Cmd[] => {
    const single = paths.length === 1 ? byPath.get(paths[0]) : undefined;
    const binned = paths.some((p) => restorePath(p, user));
    // Top-level folders (each person's, Compartilhado, Lixeira) are never renamed, moved or deleted.
    const unprotected = paths.every((p) => p.includes("/"));
    return [
      { id: "open", label: single?.dir ? t("Open") : t("Preview"), icon: single?.dir ? "folder" : "eye", run: () => open(paths[0]), when: !!single },
      { id: "edit", label: t("Edit text"), icon: "edit", run: () => openFile(paths[0], true), when: !!single && kindOf(single) === "text" && writable && !inTrash },
      { id: "download", label: t("Download"), icon: "download", run: () => download(paths.filter((p) => !byPath.get(p)?.dir)), when: paths.some((p) => !byPath.get(p)?.dir) },
      { id: "restore", label: t("Restore"), icon: "restore", run: () => void restore(paths), when: writable && binned },
      { id: "rename", label: t("Rename"), icon: "edit", run: () => setAsk({ kind: "rename", path: paths[0] }), when: !!single && canEdit(paths) && unprotected },
      { id: "move", label: t("Move to…"), icon: "move", run: () => setMoving(paths), when: paths.length > 0 && canEdit(paths) && inShare && unprotected },
      { id: "details", label: t("Details"), icon: "info", run: () => setDetails(paths[0]), when: !!single },
      { id: "delete", label: t("Delete"), icon: "trash", run: () => void trash(paths), danger: true, when: paths.length > 0 && canEdit(paths) && unprotected },
      { id: "purge", label: t("Delete forever"), icon: "trash", run: () => void purgeForGood(paths), danger: true, when: writable && paths.length > 0 && paths.every((p) => p.startsWith(`${myBin}/`)) },
      { id: "empty", label: t("Empty trash"), icon: "trash", run: () => void emptyBin(), danger: true, when: writable && paths.length === 1 && paths[0] === TRASH },
    ];
  };
  const folderCommands = (at: string): Cmd[] => [
    { id: "mkfile", label: t("New text file"), icon: "text", run: () => setAsk({ kind: "mkfile", at }), when: writable && canReceive(at) },
    { id: "mkdir", label: t("New folder"), icon: "folderPlus", run: () => setAsk({ kind: "mkdir", at }), when: writable && canReceive(at) },
    { id: "upload", label: t("Upload files"), icon: "upload", run: () => pickFiles(at), when: writable && canReceive(at) },
    { id: "uploadDir", label: t("Upload folder"), icon: "folderUp", run: () => pickFolder(at), when: folderUploads && writable && canReceive(at) },
    { id: "refresh", label: t("Update"), icon: "refresh", run: () => refresh(at), when: true },
  ];
  /** Commands for a node of the tree (any folder, or a file inside one). */
  const treeCommands = (p: string, dir: boolean): Cmd[] => {
    const top = !p.includes("/");
    const editable = writable && !p.startsWith(TRASH) && (!top || admin);
    if (dir)
      return [
        { id: "open", label: t("Open"), icon: "folder", run: () => onNavigate(p), when: true },
        ...folderCommands(p),
        { id: "rename", label: t("Rename"), icon: "edit", run: () => setAsk({ kind: "rename", path: p }), when: editable && !top },
        { id: "move", label: t("Move to…"), icon: "move", run: () => setMoving([p]), when: editable && !top },
        { id: "delete", label: t("Delete"), icon: "trash", run: () => void trash([p]), danger: true, when: editable && !top },
        { id: "empty", label: t("Empty trash"), icon: "trash", run: () => void emptyBin(), danger: true, when: writable && (p === TRASH || p === myBin) },
      ];
    const k = kindOf({ name: p, dir: false });
    return [
      { id: "open", label: t("Preview"), icon: "eye", run: () => openFile(p), when: true },
      { id: "edit", label: t("Edit text"), icon: "edit", run: () => openFile(p, true), when: k === "text" && editable },
      { id: "download", label: t("Download"), icon: "download", run: () => download([p]), when: true },
      { id: "rename", label: t("Rename"), icon: "edit", run: () => setAsk({ kind: "rename", path: p }), when: editable },
      { id: "move", label: t("Move to…"), icon: "move", run: () => setMoving([p]), when: editable },
      { id: "delete", label: t("Delete"), icon: "trash", run: () => void trash([p]), danger: true, when: editable },
    ];
  };

  const showMenu = (ev: MouseEvent | { clientX: number; clientY: number }, items: Cmd[], title?: string) => {
    const list = items.filter((c) => c.when);
    if (list.length) setMenu({ x: ev.clientX, y: ev.clientY, items: list, title });
  };
  const rowMenu = (ev: MouseEvent, p: string) => {
    ev.preventDefault();
    ev.stopPropagation();
    const paths = selected.includes(p) ? selected : [p];
    if (!selected.includes(p)) setSelected([p]);
    showMenu(ev, commands(paths), paths.length === 1 ? baseName(p) : t("{length} items", { length: paths.length }));
  };
  const bgMenu = (ev: MouseEvent) => {
    if ((ev.target as HTMLElement).closest(".file")) return;
    ev.preventDefault();
    setSelected([]);
    showMenu(ev, [
      ...folderCommands(path),
      { id: "all", label: t("Select all"), icon: "check", run: () => setSelected(entries.map((e) => joinPath(path, e.name))), when: entries.length > 0 && !compact },
    ]);
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
        setSelected(entries.map((x) => joinPath(path, x.name)));
      } else if (e.key === "Delete" && selected.length && canEdit(selected)) {
        void trash(selected);
      } else if (e.key === "F2" && selected.length === 1 && writable && !inTrash && inShare) {
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
      else if (ev.dataTransfer.files.length) void pickedFromDrop(ev.dataTransfer).then((picked) => enqueue(picked, dest));
    },
    "data-drop": dropOn === dest ? "true" : undefined,
  });

  const barCommands = commands(selected).filter((c) => c.when && c.id !== "details" && c.id !== "edit");
  const paneOn = pane && !compact;

  return (
    <div className="cloud">
      <section className="card cloud-top">
        <div className="cloud-space">{summary}</div>
        {!compact && (
          <div className="cloud-actions">
            <button className="btn" disabled={!canAddHere} onClick={() => setAsk({ kind: "mkdir", at: path })} title={canAddHere ? undefined : t("Open one of your folders or Shared")}>
              <Icon name="folderPlus" />{" "}{t("New folder")}
            </button>
            <button className="btn" disabled={!canAddHere} onClick={() => setAsk({ kind: "mkfile", at: path })} title={canAddHere ? t("New text file") : t("Open one of your folders or Shared")}>
              <Icon name="text" />{" "}{t("New file")}
            </button>
            <div className="split-btn" role="group" aria-label={t("Upload")}>
              <button className="btn primary" disabled={!canAddHere} onClick={() => pickFiles()} title={canAddHere ? undefined : t("Open one of your folders or Shared")}>
                <Icon name="upload" />{" "}{t("Upload files")}
              </button>
              {folderUploads && (
                <button className="btn primary" disabled={!canAddHere} onClick={() => pickFolder()} title={canAddHere ? t("Upload a whole folder, with its subfolders") : t("Open one of your folders or Shared")}>
                  <Icon name="folderUp" />{" "}{t("Folder")}
                </button>
              )}
            </div>
          </div>
        )}
        <input
            id={pickId}
            type="file"
            multiple
            hidden
            onChange={(e) => {
              if (e.target.files) void enqueue(pickedFromList(e.target.files), uploadDir.current);
              e.target.value = "";
            }}
          />
        {folderUploads && (
          <input
            id={folderPickId}
            type="file"
            hidden
            {...({ webkitdirectory: "", directory: "" } as object)}
            onChange={(e) => {
              if (e.target.files) void enqueue(pickedFromList(e.target.files), uploadDir.current);
              e.target.value = "";
            }}
          />
        )}
      </section>

      {compact && canAddHere && (
        <button className="fab" aria-label={t("Add: folder, file or upload")} onClick={() => setAdding(true)}>
          <Icon name="plus" />
        </button>
      )}
      {adding && (
        <div className="dialog-backdrop bottom" role="dialog" aria-modal="true" aria-label={t("Add")} onMouseDown={(e) => e.target === e.currentTarget && setAdding(false)}>
          <div className="action-sheet">
            <button className="btn primary" onClick={() => (setAdding(false), pickFiles())}>
              <Icon name="upload" />{" "}{t("Upload files")}
            </button>
            <button className="btn" onClick={() => (setAdding(false), setAsk({ kind: "mkdir", at: path }))}>
              <Icon name="folderPlus" />{" "}{t("New folder")}
            </button>
            <button className="btn" onClick={() => (setAdding(false), setAsk({ kind: "mkfile", at: path }))}>
              <Icon name="text" />{" "}{t("New text file")}
            </button>
            <button className="btn ghost" onClick={() => setAdding(false)}>
              {t("Cancel")}
            </button>
          </div>
        </div>
      )}

      {filterSheet && (
        <div className="dialog-backdrop bottom" role="dialog" aria-modal="true" aria-label={t("Sort and filter")} onMouseDown={(e) => e.target === e.currentTarget && setFilterSheet(false)}>
          <div className="action-sheet filter-sheet">
            <div className="sheet-head">
              <strong>{t("Sort and filter")}</strong>
              <button className="btn ghost icon small" aria-label={t("Close")} onClick={() => setFilterSheet(false)}>
                <Icon name="x" />
              </button>
            </div>
            <OptionGroup
              label={t("Sort by")}
              value={`${sort.key}:${sort.asc ? 1 : 0}`}
              options={SORTS}
              onChange={(v) => {
                const [k, a] = v.split(":");
                setSort({ key: k as SortKey, asc: a === "1" });
              }}
            />
            <OptionGroup label={t("Type")} value={type} options={TYPE_FILTERS.map((o) => ({ v: o.v, label: o.v ? o.label : t("All") }))} onChange={(v) => setType(v as TypeFilter)} />
            <OptionGroup label={t("Modified")} value={date} options={DATE_FILTERS.map((o) => ({ v: o.v, label: o.v ? o.label : t("Any date") }))} onChange={(v) => setDate(v as DateFilter)} />
            <div className="row-gap">
              <button
                className="btn ghost"
                disabled={!type && !date}
                onClick={() => {
                  setType("");
                  setDate("");
                }}
              >
                {t("Clear filters")}
              </button>
              <button className="btn primary grow" onClick={() => setFilterSheet(false)}>
                {t("View")}{" "}{entries.length} {plural(entries.length, "{n} item", "{n} items")}
              </button>
            </div>
          </div>
        </div>
      )}

      <section className={`card explorer${paneOn ? " with-pane" : ""}`}>
        {!compact && (
          <aside className="tree" aria-label={t("Folders and files")}>
            <div className="tree-bar">
              <span className="tree-title">{t("Explorer")}</span>
              <button className="tree-tool" title={t("New text file")} aria-label={t("New text file")} disabled={!canAddHere} onClick={() => setAsk({ kind: "mkfile", at: path })}>
                <Icon name="text" />
              </button>
              <button className="tree-tool" title={t("New folder")} aria-label={t("New folder")} disabled={!canAddHere} onClick={() => setAsk({ kind: "mkdir", at: path })}>
                <Icon name="folderPlus" />
              </button>
              <button className="tree-tool" title={t("Upload files")} aria-label={t("Upload files")} disabled={!canAddHere} onClick={() => pickFiles()}>
                <Icon name="upload" />
              </button>
              {folderUploads && (
                <button className="tree-tool" title={t("Upload folder")} aria-label={t("Upload folder")} disabled={!canAddHere} onClick={() => pickFolder()}>
                  <Icon name="folderUp" />
                </button>
              )}
              <button className="tree-tool" title={t("Update")} aria-label={t("Update")} onClick={() => void qc.invalidateQueries({ queryKey: ["nas"] })}>
                <Icon name="refresh" />
              </button>
              <button className="tree-tool" title={t("Collapse all")} aria-label={t("Collapse all")} onClick={() => setTreeKey((k) => k + 1)}>
                <Icon name="collapse" />
              </button>
            </div>
            <div className="tree-scroll">
              <FolderTree
                key={treeKey}
                current={path}
                user={user}
                onOpen={onNavigate}
                onOpenFile={(p) => openFile(p)}
                selected={preview?.path ?? (selected.length === 1 ? selected[0] : undefined)}
                dropProps={dropProps}
                showFiles
                onMenu={(ev, p, dir) => {
                  ev.preventDefault();
                  showMenu(ev, treeCommands(p, dir), p.includes("/") ? baseName(p) : label(p, user));
                }}
                expandCurrent={treeKey === 0}
              />
            </div>
          </aside>
        )}
        <div className="explorer-main" onContextMenu={bgMenu} {...(canAddHere ? dropProps(path) : {})} data-drop={dropOn === path ? "true" : undefined}>
          <div className="files-bar">
            <nav className="crumbs" aria-label={t("Current folder")}>
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
                <input className="input" placeholder={t("Filter in this folder")} aria-label={t("Filter in this folder")} value={filter} onChange={(e) => setFilter(e.target.value)} />
              </label>
              <div className="segmented" role="group" aria-label={t("Display")}>
                <button aria-pressed={view === "list"} title={t("List")} aria-label={t("List")} onClick={() => (setView("list"), savePref(viewKey, "list"))}>
                  <Icon name="list" />
                </button>
                <button aria-pressed={view === "grid"} title={t("Grid with thumbnails")} aria-label={t("Grid")} onClick={() => (setView("grid"), savePref(viewKey, "grid"))}>
                  <Icon name="grid" />
                </button>
              </div>
              {!compact && (
                <button
                  className={`btn icon small${pane ? " on" : ""}`}
                  aria-pressed={pane}
                  title={t("Details panel")}
                  aria-label={t("Details panel")}
                  onClick={() => (setPane((v) => !v), savePref(PANE_KEY, pane ? "0" : "1"))}
                >
                  <Icon name="panel" />
                </button>
              )}
            </div>
          </div>

          <div className="filter-row" role="group" aria-label={t("Filters")}>
            {compact ? (
              // Phone: one button and a sheet, instead of a row of dropdowns
              // that does not fit (owner, round 6).
              <button className={`filter-btn${type || date ? " on" : ""}`} onClick={() => setFilterSheet(true)}>
                <Icon name="sort" /> {SORTS.find((o) => o.v === `${sort.key}:${sort.asc ? 1 : 0}`)?.label}
                {(type ? 1 : 0) + (date ? 1 : 0) > 0 && <span className="count-badge">{(type ? 1 : 0) + (date ? 1 : 0)}</span>}
              </button>
            ) : (
              <>
                <FilterChip label={t("Type")} value={type} options={TYPE_FILTERS} onChange={setType} />
                <FilterChip label={t("Modified")} value={date} options={DATE_FILTERS} onChange={setDate} />
                <SortChip sort={sort} onChange={setSort} />
              </>
            )}
            {(type || date || filter) && (
              <button
                className="btn ghost small"
                onClick={() => {
                  setType("");
                  setDate("");
                  setFilter("");
                }}
              >
                <Icon name="x" />{" "}{t("Clear filters")}
              </button>
            )}
            <span className="grow" />
            {inTrash && writable && (
              <button className="btn small ghost danger" onClick={() => void emptyBin()}>
                <Icon name="trash" />{" "}{t("Empty trash")}
              </button>
            )}
            <span className="muted small num">{filtered ? `${t("{a} of {b}", { a: entries.length, b: all.length })}` : `${plural(all.length, "{n} item", "{n} items")}`}</span>
          </div>

          {selected.length > 0 && !compact && (
            <div className="selbar" role="toolbar" aria-label={t("Selection")}>
              <button className="btn ghost icon small" aria-label={t("Clear selection")} onClick={() => setSelected([])}>
                <Icon name="x" />
              </button>
              <span className="selbar-count">{selected.length === 1 ? t("1 selected") : t("{n} selected", { n: selected.length })}</span>
              {barCommands.map((c) => (
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
            <p className="muted files-empty">{t("Loading…")}</p>
          ) : entries.length === 0 ? (
            <div className="files-empty">
              <Icon name={inTrash ? "trash" : filtered ? "search" : "folder"} />
              <strong>{filtered ? t("Nothing with these filters") : inTrash ? t("The trash is empty") : t("Empty folder")}</strong>
              <span className="muted">
                {filtered
                  ? t("Change or clear the filters.")
                  : inTrash
                    ? t("Deleted files stay here for 30 days.")
                    : canAddHere
                      ? compact
                        ? t("Tap + to upload files or create a folder.")
                        : t("Drag files or folders here, or use Upload or the right-click menu.")
                      : t("Nothing here yet.")}
              </span>
              {compact && path !== "" && !filtered && (
                <button className="btn" onClick={() => onNavigate(parentPath(path))}>
                  <Icon name="chevronLeft" />{" "}{t("Back")}
                </button>
              )}
            </div>
          ) : (
            <>
              {view === "list" && (
                <div className="file-head" role="row">
                  <span className="col-owner">{t("Owner")}</span>
                  {(["name", "modified", "size"] as SortKey[]).map((k) => (
                    <button
                      key={k}
                      className={`col-${k}${sort.key === k ? " on" : ""}`}
                      onClick={() => setSort((s) => ({ key: k, asc: s.key === k ? !s.asc : k === "name" }))}
                      aria-sort={sort.key === k ? (sort.asc ? "ascending" : "descending") : "none"}
                    >
                      {k === "name" ? t("Name") : k === "modified" ? t("Modified") : t("Size")}
                      {sort.key === k && <Icon name={sort.asc ? "up" : "down"} />}
                    </button>
                  ))}
                </div>
              )}
              <ul className={`file-list ${view}`} role="listbox" aria-multiselectable="true" aria-label={t("Files")}>
                {entries.map((e) => {
                  const p = joinPath(path, e.name);
                  const isSel = selected.includes(p);
                  const drag = writable && !inTrash && inShare;
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
                        {!compact && (inShare || admin) && (
                          <span
                            className="file-check"
                            role="checkbox"
                            aria-checked={isSel}
                            aria-label={t("Select {name}", { name: e.name })}
                            onClick={(ev) => {
                              ev.stopPropagation();
                              toggleCheck(p);
                            }}
                          >
                            {isSel && <Icon name="check" />}
                          </span>
                        )}
                        <Thumb path={p} entry={e} rich={view === "grid"} owner={owners[e.name]} />
                        <span className="file-name">{e.dir && !inShare ? label(e.name, user) : e.name}</span>
                        {compact ? (
                          <span className="file-sub muted small">
                            {e.dir ? kindLabel.folder : fmtBytes(e.size)}, {fmtDate(e.modified)}
                            {owners[e.name] && owners[e.name] !== user && path.startsWith(SHARED) ? t(", of {v0}", { v0: owners[e.name] }) : ""}
                          </span>
                        ) : (
                          <>
                            <span className="file-owner small">
                              {owners[e.name] ? (
                                <>
                                  <Avatar name={owners[e.name]} className="tiny" />
                                  <span>{owners[e.name] === user ? t("me") : owners[e.name]}</span>
                                </>
                              ) : path === "" && e.name === SHARED ? (
                                <span className="muted">{t("All")}</span>
                              ) : (
                                <span className="muted">—</span>
                              )}
                            </span>
                            <span className="file-date muted small">{fmtDate(e.modified)}</span>
                            <span className="file-size muted small">{e.dir ? kindLabel.folder : `${kindLabel[kindOf(e)]} · ${fmtBytes(e.size)}`}</span>
                          </>
                        )}
                        {(inShare || admin) && (
                          <button
                            className="btn ghost icon small file-more"
                            aria-label={t("Actions for {name}", { name: e.name })}
                            onClick={(ev) => {
                              ev.stopPropagation();
                              const r = (ev.currentTarget as HTMLElement).getBoundingClientRect();
                              setSelected([p]);
                              showMenu({ clientX: r.right, clientY: r.bottom }, commands([p]), e.name);
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
          {inTrash && entries.length > 0 && <p className="muted small files-foot">{t("Trash items disappear by themselves 30 days after being deleted.")}</p>}
        </div>
        {paneOn && (
          <DetailsPane path={path} user={user} entries={all} selected={selected} byPath={byPath} actions={commands(selected).filter((c) => c.when && c.id !== "details")} />
        )}
      </section>

      {menu && <ContextMenu at={menu} sheet={compact} onClose={() => setMenu(null)} />}

      {ask && (
        <NameDialog
          ask={ask}
          onCancel={() => setAsk(null)}
          onSubmit={async (value) => {
            if (ask.kind === "mkdir") {
              if (await run(() => mkdir(joinPath(ask.at, value)), t("Folder created."), [ask.at])) setAsk(null);
            } else if (ask.kind === "mkfile") {
              const name = /\.[a-z0-9]{1,5}$/i.test(value) ? value : `${value}.txt`;
              const p = joinPath(ask.at, name);
              if (await run(() => createEmpty(p), t("File created."), [ask.at])) {
                setAsk(null);
                openFile(p, kindOf({ name, dir: false }) === "text");
              }
            } else {
              const to = joinPath(parentPath(ask.path), value);
              if (await run(() => move(ask.path, to), t("Name changed."), [parentPath(ask.path)])) {
                setAsk(null);
                if (parentPath(ask.path) === path) setSelected([to]);
                if (preview?.path === ask.path) setPreview({ path: to });
              }
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
            if (await moveInto(moving, dest)) {
              setMoving(null);
              setPreview(null);
            }
          }}
        />
      )}

      {preview && byPath.get(preview.path) && (
        <PreviewDialog
          path={preview.path}
          entry={byPath.get(preview.path)!}
          startEditing={!!preview.edit}
          files={files}
          byPath={byPath}
          writable={writable && !inTrash}
          onGo={(p) => setPreview({ path: p })}
          onClose={() => setPreview(null)}
          onSaved={() => refresh(path)}
          onRename={() => setAsk({ kind: "rename", path: preview.path })}
          onMove={() => setMoving([preview.path])}
          onDelete={() => void trash([preview.path])}
        />
      )}

      {details && byPath.get(details) && <DetailsDialog path={details} entry={byPath.get(details)!} user={user} onClose={() => setDetails(null)} />}
    </div>
  );
}

function folderName(p: string, user: string) {
  if (!p) return t("Cloud");
  const parts = p.split("/");
  return parts.length === 1 ? label(parts[0], user) : parts[parts.length - 1];
}

function FilterChip<T extends string>({ label, value, options, onChange }: { label: string; value: T; options: { v: T; label: string }[]; onChange: (v: T) => void }) {
  return (
    <label className={`filter-chip${value ? " on" : ""}`}>
      <select value={value} onChange={(e) => onChange(e.target.value as T)} aria-label={label}>
        {options.map((o) => (
          <option key={o.v} value={o.v}>
            {o.label}
          </option>
        ))}
      </select>
      <Icon name="chevronDown" />
    </label>
  );
}

/** Radio chips inside the phone's filter sheet. */
function OptionGroup({ label, value, options, onChange }: { label: string; value: string; options: { v: string; label: string }[]; onChange: (v: string) => void }) {
  return (
    <div className="option-group" role="radiogroup" aria-label={label}>
      <span className="set-group-title">{label}</span>
      <div className="option-chips">
        {options.map((o) => (
          <button key={o.v} role="radio" aria-checked={value === o.v} className={value === o.v ? "on" : ""} onClick={() => onChange(o.v)}>
            {value === o.v && <Icon name="check" />}
            {o.label}
          </button>
        ))}
      </div>
    </div>
  );
}

const SORTS: { v: string; label: string }[] = [
  { v: "name:1", label: t("Name (A–Z)") },
  { v: "name:0", label: t("Name (Z–A)") },
  { v: "modified:0", label: t("Newest") },
  { v: "modified:1", label: t("Oldest") },
  { v: "size:0", label: t("Largest") },
  { v: "size:1", label: t("Smallest") },
];

function SortChip({ sort, onChange }: { sort: { key: SortKey; asc: boolean }; onChange: (s: { key: SortKey; asc: boolean }) => void }) {
  return (
    <label className="filter-chip">
      <Icon name="sort" />
      <select
        aria-label={t("Sort")}
        value={`${sort.key}:${sort.asc ? 1 : 0}`}
        onChange={(e) => {
          const [k, a] = e.target.value.split(":");
          onChange({ key: k as SortKey, asc: a === "1" });
        }}
      >
        {SORTS.map((o) => (
          <option key={o.v} value={o.v}>
            {o.label}
          </option>
        ))}
      </select>
      <Icon name="chevronDown" />
    </label>
  );
}

/**
 * Overall upload speed, sampled once a second (steady numbers, like the big
 * drives show). While sending, leaving the page asks first: the bytes on the
 * server are kept, but the queue of picked files would be lost.
 */
function useUploadSpeed(sent: number, busy: boolean): number {
  const [speed, setSpeed] = useState(0);
  const sentRef = useRef(sent);
  useEffect(() => {
    sentRef.current = sent;
  }, [sent]);
  useEffect(() => {
    if (!busy) return;
    const meter = new SpeedMeter();
    const tick = () => {
      const t = performance.now();
      meter.add(t, sentRef.current);
      setSpeed(meter.rate(t));
    };
    tick();
    const id = setInterval(tick, 1000);
    const warn = (e: BeforeUnloadEvent) => e.preventDefault();
    addEventListener("beforeunload", warn);
    return () => {
      clearInterval(id);
      removeEventListener("beforeunload", warn);
      setSpeed(0);
    };
  }, [busy]);
  return speed;
}

/**
 * A number that glides to its target every frame, so the byte counter
 * climbs 5,70 → 5,71 → 5,72 GB instead of jumping per progress event.
 * Going down (a cancelled file) and reduced motion jump straight there.
 */
function useEased(target: number, tauMs: number): number {
  const [shown, setShown] = useState(target);
  const ref = useRef(target);
  useEffect(() => {
    const instant = target < ref.current || matchMedia("(prefers-reduced-motion: reduce)").matches;
    let last = performance.now();
    let raf = 0;
    const step = (t: number) => {
      ref.current = instant ? target : easeToward(ref.current, target, t - last, tauMs);
      last = t;
      setShown(ref.current);
      if (ref.current !== target) raf = requestAnimationFrame(step);
    };
    raf = requestAnimationFrame(step);
    return () => cancelAnimationFrame(raf);
  }, [target, tauMs]);
  return shown;
}

function Uploads({ uploads, setUploads }: { uploads: Upload[]; setUploads: (f: (u: Upload[]) => Upload[]) => void }) {
  const [all, setAll] = useState(false);
  const active = uploads.filter((u) => u.state === "sending" || u.state === "waiting").length;
  const done = uploads.filter((u) => u.state === "done").length;
  const failed = uploads.filter((u) => u.state === "error").length;
  const total = uploads.reduce((a, u) => a + (u.state === "cancelled" ? 0 : u.total), 0);
  const sent = uploads.reduce((a, u) => a + (u.state === "cancelled" ? 0 : u.state === "done" ? u.total : u.sent), 0);
  const groups = [...new Set(uploads.map((u) => u.group).filter(Boolean))];
  const busy = active > 0;
  const speed = useUploadSpeed(sent, busy);
  const shownSent = useEased(sent, 400);
  const shownSpeed = useEased(speed, 800);
  const pct = total ? Math.floor((100 * shownSent) / total) : 100;
  // Long lists (a folder of photos): what is moving or failed first, the rest behind "Ver todos".
  const LIMIT = 6;
  const order = (u: Upload) => ({ error: 0, sending: 1, waiting: 2, done: 3, cancelled: 4 })[u.state];
  // All sent: only the summary stays, the list opens on demand.
  const quiet = !active && !failed;
  const shown = all ? uploads : quiet ? [] : [...uploads].sort((a, b) => order(a) - order(b)).slice(0, LIMIT);
  return (
    <div className="uploads-box">
      <div className="uploads-head">
        <div className="uploads-title">
          <strong>
            {active
              ? t("Uploading {v0}", { v0: groups.length === 1 ? t("folder {v0}", { v0: groups[0] }) : plural(active, "{n} file", "{n} files") })
              : failed
                ? plural(failed, "{n} upload failed", "{n} uploads failed")
                : t("Uploads completed")}
          </strong>
          <span className="muted small num">
            {t(uploads.length === 1 ? "{done} of {count} file · {sent} of {total}" : "{done} of {count} files · {sent} of {total}", { done, count: uploads.length, sent: busy ? fmtBytesFine(shownSent) : fmtBytes(sent), total: fmtBytes(total) })}
            {busy && ` (${pct}%)`}
          </span>
          {busy && (
            <span className="small num uploads-speed" aria-live="off">
              {speed > 0 ? (
                <>
                  <strong>{fmtBytes(shownSpeed)}/s</strong>
                  <span className="muted"> · {fmtRemaining(((total - sent) / speed) * 1000)}</span>
                </>
              ) : (
                <span className="muted">{t("measuring the speed…")}</span>
              )}
            </span>
          )}
        </div>
        {active > 1 && (
          <button className="btn ghost small" onClick={() => uploads.forEach((u) => (u.state === "sending" || u.state === "waiting") && u.ctrl.abort())}>
            {t("Cancel all")}
          </button>
        )}
        {!active && (
          <button className="btn ghost small" onClick={() => setUploads(() => [])}>
            {t("Clear")}
          </button>
        )}
      </div>
      <span className="meter thin uploads-meter" aria-hidden="true">
        <span style={{ width: `${total ? (100 * shownSent) / total : 100}%` }} />
      </span>
      {shown.length > 0 && (
      <ul className="uploads" aria-label={t("Uploads")}>
        {shown.map((u) => (
          <li key={u.id} className={`upload ${u.state}`}>
            <span className="upload-name" title={u.target}>
              {u.name}
            </span>
            <span className="muted small num">
              {u.state === "error"
                ? u.error
                : u.state === "done"
                  ? t("sent")
                  : u.state === "cancelled"
                    ? t("canceled")
                    : u.state === "waiting"
                      ? t("queued")
                      : t("{v0} of {v1} · {v2}%", { v0: fmtBytesFine(u.sent), v1: fmtBytes(u.total), v2: u.total ? Math.floor((100 * u.sent) / u.total) : 0 })}
            </span>
            {(u.state === "sending" || u.state === "waiting") && (
              <button className="btn ghost icon small" aria-label={t("Cancel {name}", { name: u.name })} onClick={() => u.ctrl.abort()}>
                <Icon name="x" />
              </button>
            )}
            {u.state === "error" && (
              <button
                className="btn ghost small"
                onClick={() => setUploads((all) => all.map((x) => (x.id === u.id ? { ...x, state: "waiting", error: undefined, ctrl: new AbortController() } : x)))}
              >
                {t("Try again")}
              </button>
            )}
            <span className="meter thin" aria-hidden="true">
              <span style={{ width: `${u.total ? (100 * u.sent) / u.total : u.state === "done" ? 100 : 0}%` }} />
            </span>
          </li>
        ))}
      </ul>
      )}
      {(uploads.length > LIMIT || quiet) && (
        <button className="linkish small uploads-more" onClick={() => setAll((v) => !v)}>
          {all ? t("Show less") : uploads.length === 1 ? t("View the file") : t("View the {length}", { length: uploads.length })}
        </button>
      )}
    </div>
  );
}

// ---------- details pane ----------

function DetailsPane({
  path,
  user,
  entries,
  selected,
  byPath,
  actions,
}: {
  path: string;
  user: string;
  entries: NasEntry[];
  selected: string[];
  byPath: Map<string, NasEntry>;
  actions: Cmd[];
}) {
  const one = selected.length === 1 ? byPath.get(selected[0]) : undefined;
  if (!one) {
    const list = selected.length ? selected.map((p) => byPath.get(p)).filter((e): e is NasEntry => !!e) : entries;
    const filesOnly = list.filter((e) => !e.dir);
    const bytes = filesOnly.reduce((a, e) => a + e.size, 0);
    return (
      <aside className="details-pane" aria-label={t("Details")}>
        <div className="pane-preview">
          <span className="file-icon big k-folder">
            <Icon name={selected.length ? "check" : path.startsWith(TRASH) ? "trash" : "folder"} />
          </span>
        </div>
        <h3 className="pane-title">{selected.length ? t("{length} items selected", { length: selected.length }) : folderName(path, user)}</h3>
        <dl className="kv pane-kv">
          <dt>{t("Folders")}</dt>
          <dd>{list.length - filesOnly.length}</dd>
          <dt>{t("Files")}</dt>
          <dd>{filesOnly.length}</dd>
          <dt>{t("Size")}</dt>
          <dd>{fmtBytes(bytes)}</dd>
        </dl>
        {selected.length > 0 && <PaneActions actions={actions} />}
        {!selected.length && <p className="muted small">{t("Select a file to see the preview here.")}</p>}
      </aside>
    );
  }
  const p = selected[0];
  const k = kindOf(one);
  const src = fileUrl(p);
  return (
    <aside className="details-pane" aria-label={t("Details")}>
      <div className={`pane-preview k-${k}`}>
        {k === "image" ? (
          <img src={hasThumb(one.name) ? thumbUrl(p) : src} alt="" />
        ) : k === "video" ? (
          <video key={p} src={src} controls muted playsInline preload="metadata" />
        ) : k === "audio" ? (
          <audio key={p} src={src} controls preload="none" />
        ) : k === "pdf" ? (
          <iframe key={p} src={`${src}#toolbar=0&view=FitH`} title={one.name} />
        ) : k === "text" && one.size <= 256 << 10 ? (
          <TextSnippet path={p} />
        ) : (
          <EntryIcon path={selected[0]} entry={one} big />
        )}
      </div>
      <h3 className="pane-title" title={one.name}>
        {one.name}
      </h3>
      <dl className="kv pane-kv">
        <dt>{t("Type")}</dt>
        <dd>{kindLabel[k]}</dd>
        {!one.dir && (
          <>
            <dt>{t("Size")}</dt>
            <dd>{fmtBytes(one.size)}</dd>
          </>
        )}
        <dt>{t("Modified")}</dt>
        <dd>{new Date(one.modified).toLocaleString(getLang(), { dateStyle: "short", timeStyle: "short" })}</dd>
        <dt>{t("Local")}</dt>
        <dd>{crumbs(parentPath(p), user).map((c) => c.name).join(" / ")}</dd>
      </dl>
      <PaneActions actions={actions} />
      {isImage(one.name) && <p className="muted small">{t("Double-click opens the picture full screen.")}</p>}
    </aside>
  );
}

function PaneActions({ actions }: { actions: Cmd[] }) {
  return (
    <div className="pane-actions">
      {actions.map((c) => (
        <button key={c.id} className={`btn small${c.danger ? " ghost danger" : ""}`} onClick={c.run}>
          <Icon name={c.icon} /> {c.label}
        </button>
      ))}
    </div>
  );
}

function TextSnippet({ path }: { path: string }) {
  const q = useQuery({ queryKey: ["nas-text", path], queryFn: () => fetchText(path), staleTime: 60_000 });
  return <pre className="pane-text">{q.data?.slice(0, 1500) ?? "…"}</pre>;
}

async function fetchText(path: string): Promise<string> {
  const res = await fetch(fileUrl(path, true), { credentials: "same-origin", cache: "no-store" });
  if (!res.ok) throw new ApiError(res.status, `HTTP ${res.status}`);
  return new TextDecoder("utf-8").decode(await res.arrayBuffer());
}

// ---------- folder tree ----------

type DropProps = (dest: string) => object;

interface TreeProps {
  current: string;
  user: string;
  onOpen: (p: string) => void;
  onOpenFile?: (p: string) => void;
  /** Highlighted file (preview or single selection). */
  selected?: string;
  dropProps?: DropProps;
  /** Picker mode (Mover para…): these paths cannot be picked. */
  pick?: { exclude: string[] };
  showFiles?: boolean;
  onMenu?: (ev: MouseEvent, p: string, dir: boolean) => void;
  /** Open the current folder's ancestors on mount. */
  expandCurrent?: boolean;
}

function ancestors(p: string): string[] {
  const parts = p.split("/");
  return parts.map((_, i) => parts.slice(0, i + 1).join("/"));
}

function FolderTree(props: TreeProps) {
  const { current, expandCurrent = true } = props;
  // Ancestors of the current folder start open ("Recolher tudo" remounts closed).
  const [open, setOpen] = useState<Set<string>>(() => new Set(expandCurrent ? ancestors(current) : []));
  // Navigating elsewhere opens the way to the new folder.
  const [seen, setSeen] = useState(current);
  if (seen !== current) {
    setSeen(current);
    setOpen(new Set([...open, ...ancestors(current)]));
  }
  const toggle = (p: string) =>
    setOpen((s) => {
      const n = new Set(s);
      if (n.has(p)) n.delete(p);
      else n.add(p);
      return n;
    });
  return (
    <ul className="tree-list" role="tree">
      <TreeLevel {...props} parent="" depth={0} open={open} toggle={toggle} />
    </ul>
  );
}

function TreeLevel(props: TreeProps & { parent: string; depth: number; open: Set<string>; toggle: (p: string) => void }) {
  const { parent, depth, current, user, open, toggle, onOpen, onOpenFile, selected, dropProps, pick, showFiles, onMenu } = props;
  const q = useQuery({ queryKey: ["nas", parent], queryFn: () => listDir(parent), retry: 0, staleTime: 15_000 });
  const all = sortEntries(q.data?.entries ?? [], "name", true);
  let dirs = all.filter((e) => e.dir && !(pick && e.name === TRASH));
  // Top level: my folder first, the bin last.
  if (parent === "") dirs = [...dirs.filter((e) => e.name === user), ...dirs.filter((e) => e.name !== user && e.name !== TRASH), ...dirs.filter((e) => e.name === TRASH)];
  const files = showFiles && parent !== "" ? all.filter((e) => !e.dir) : [];
  if (q.isLoading)
    return (
      <li className="tree-loading muted small" style={{ paddingLeft: 12 + depth * 14 }}>
        {t("Loading…")}
      </li>
    );
  if (!dirs.length && !files.length && depth > 0)
    return (
      <li className="tree-empty muted small" style={{ paddingLeft: 30 + depth * 14 }}>
        {t("empty")}
      </li>
    );
  return (
    <>
      {dirs.map((d) => {
        const p = joinPath(parent, d.name);
        const isOpen = open.has(p);
        const blocked = !!pick && pick.exclude.some((x) => inside(p, x));
        const name = parent === "" ? label(d.name, user) : d.name;
        const owner = parent === "" ? q.data?.owners?.[d.name] : undefined;
        return (
          <li key={p} role="treeitem" aria-expanded={isOpen} aria-selected={current === p}>
            <div
              className={`tree-row${current === p ? " on" : ""}${blocked ? " blocked" : ""}`}
              style={{ paddingLeft: 4 + depth * 14 }}
              onContextMenu={onMenu ? (ev) => onMenu(ev, p, true) : undefined}
              {...(dropProps && !pick ? dropProps(p) : {})}
            >
              <button className={`tree-twist${isOpen ? " open" : ""}`} aria-label={t(isOpen ? "Close {name}" : "Open {name}", { name })} onClick={() => toggle(p)} tabIndex={-1}>
                <Icon name="chevronRight" />
              </button>
              <button
                className="tree-name"
                disabled={blocked}
                onClick={() => {
                  onOpen(p);
                  if (!isOpen) toggle(p);
                }}
                title={name}
              >
                <span className="tree-ico drawn">
                  <EntryIcon path={p} entry={d} owner={owner} />
                </span>
                <span className="tree-label">{name}</span>
              </button>
            </div>
            {isOpen && (
              <ul role="group">
                <TreeLevel {...props} parent={p} depth={depth + 1} />
              </ul>
            )}
          </li>
        );
      })}
      {files.map((f) => {
        const p = joinPath(parent, f.name);
        return (
          <li key={p} role="treeitem" aria-selected={selected === p}>
            <div className={`tree-row file-row${selected === p ? " on" : ""}`} style={{ paddingLeft: 26 + depth * 14 }} onContextMenu={onMenu ? (ev) => onMenu(ev, p, false) : undefined}>
              <button className="tree-name" onClick={() => onOpenFile?.(p)} title={`${f.name} · ${fmtBytes(f.size)}`}>
                <span className="tree-ico drawn">
                  <FileIcon type={fileType(f.name, false)} name={f.name} />
                </span>
                <span className="tree-label">{f.name}</span>
              </button>
            </div>
          </li>
        );
      })}
    </>
  );
}

// ---------- menus and dialogs ----------

function ContextMenu({ at, sheet, onClose }: { at: Menu; sheet: boolean; onClose: () => void }) {
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
  const list = (
    <div ref={ref} className={`ctx-menu${sheet ? " sheet-menu" : ""}`} role="menu" style={sheet ? undefined : pos} onContextMenu={(e) => e.preventDefault()}>
      {at.title && <div className="ctx-title">{at.title}</div>}
      {at.items.map((c, i) => (
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

function Dialog({ title, onClose, children, className }: { title: string; onClose: () => void; children: ReactNode; className?: string }) {
  useEffect(() => {
    const k = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", k);
    return () => window.removeEventListener("keydown", k);
  }, [onClose]);
  return (
    <div className="dialog-backdrop" role="dialog" aria-modal="true" aria-label={title} onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className={`dialog${className ? ` ${className}` : ""}`}>{children}</div>
    </div>
  );
}

function NameDialog({ ask, onCancel, onSubmit }: { ask: Ask; onCancel: () => void; onSubmit: (v: string) => void }) {
  const initial = ask.kind === "rename" ? baseName(ask.path) : ask.kind === "mkfile" ? t("New file.txt") : "";
  const [value, setValue] = useState(initial);
  const title = ask.kind === "mkdir" ? t("New folder") : ask.kind === "mkfile" ? t("New text file") : t("Rename");
  const inputRef = useRef<HTMLInputElement>(null);
  useEffect(() => {
    const el = inputRef.current;
    if (!el) return;
    el.focus();
    // The name is selected without the extension, like a file manager.
    const dot = initial.lastIndexOf(".");
    el.setSelectionRange(0, dot > 0 ? dot : initial.length);
  }, [initial]);
  const invalid = /[/\\]/.test(value) || value.trim().startsWith(".");
  const same = ask.kind === "rename" && value.trim() === initial;
  return (
    <Dialog title={title} onClose={onCancel}>
      <form
        className="stack-sm"
        onSubmit={(e) => {
          e.preventDefault();
          if (value.trim() && !invalid && !same) onSubmit(value.trim());
        }}
      >
        <h2>{title}</h2>
        <input ref={inputRef} className="input" value={value} onChange={(e) => setValue(e.target.value)} aria-label={title} placeholder={ask.kind === "mkdir" ? t("Folder name") : undefined} />
        {invalid && <span className="hint bad">{t("The name cannot contain / or \\ or start with a dot.")}</span>}
        {ask.kind === "mkfile" && <span className="hint">{t("Without an extension, it becomes .txt. The file opens in the editor.")}</span>}
        <div className="dialog-actions">
          <button type="button" className="btn ghost" onClick={onCancel}>
            {t("Cancel")}
          </button>
          <button type="submit" className="btn primary" disabled={!value.trim() || invalid || same}>
            {ask.kind === "rename" ? t("Rename") : t("Create")}
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
    <Dialog title={t("Move to")} onClose={onCancel} className="move-dialog">
      <h2>{t("Move")}{" "}{paths.length === 1 ? `“${baseName(paths[0])}”` : t("{length} items", { length: paths.length })}</h2>
      <p className="muted small">{t("Choose the destination folder.")}</p>
      <div className="move-tree">
        <FolderTree current={dest} user={user} onOpen={setDest} pick={{ exclude: paths }} />
      </div>
      <p className="small move-dest">
        {t("Destination:")}{" "}<strong>{dest ? crumbs(dest, user).slice(1).map((c) => c.name).join(" / ") : "—"}</strong>
      </p>
      <div className="dialog-actions">
        <button className="btn ghost" onClick={onCancel}>
          {t("Cancel")}
        </button>
        <button
          className="btn primary"
          disabled={bad || busy}
          onClick={() => {
            setBusy(true);
            Promise.resolve(onMove(dest)).finally(() => setBusy(false));
          }}
        >
          <Icon name="move" />{" "}{t("Move here")}
        </button>
      </div>
    </Dialog>
  );
}

function DetailsDialog({ path, entry, user, onClose }: { path: string; entry: NasEntry; user: string; onClose: () => void }) {
  const from = restorePath(path, user);
  return (
    <Dialog title={t("Details")} onClose={onClose}>
      <div className="dialog-head">
        <h2 className="wrap-anywhere">{entry.name}</h2>
        <button className="btn ghost icon small" aria-label={t("Close")} onClick={onClose}>
          <Icon name="x" />
        </button>
      </div>
      <dl className="kv details-kv">
        <dt>{t("Type")}</dt>
        <dd>{kindLabel[kindOf(entry)]}</dd>
        {!entry.dir && (
          <>
            <dt>{t("Size")}</dt>
            <dd>{fmtBytes(entry.size)}</dd>
          </>
        )}
        <dt>{t("Modified")}</dt>
        <dd>{new Date(entry.modified).toLocaleString(getLang())}</dd>
        <dt>{t("Local")}</dt>
        <dd>{crumbs(parentPath(path), user).map((c) => c.name).join(" / ")}</dd>
        {from && (
          <>
            <dt>{t("Came from")}</dt>
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

/** Large viewer: images, video, audio, PDF and text (editable); ← → walk the folder. */
function PreviewDialog({
  path,
  entry,
  startEditing,
  files,
  byPath,
  writable,
  onGo,
  onClose,
  onSaved,
  onRename,
  onMove,
  onDelete,
}: {
  path: string;
  entry: NasEntry;
  startEditing: boolean;
  files: string[];
  byPath: Map<string, NasEntry>;
  writable: boolean;
  onGo: (p: string) => void;
  onClose: () => void;
  onSaved: () => void;
  onRename: () => void;
  onMove: () => void;
  onDelete: () => void;
}) {
  const toast = useToast();
  const [sharing, setSharing] = useState(false);
  const [dirty, setDirty] = useState(false);
  const i = files.indexOf(path);
  const prev = i > 0 ? files[i - 1] : null;
  const next = i >= 0 && i < files.length - 1 ? files[i + 1] : null;
  const kind = kindOf(entry);
  const src = fileUrl(path);
  const leave = (fn: () => void) => () => {
    if (!dirty || window.confirm(t("Discard unsaved changes?"))) {
      setDirty(false);
      fn();
    }
  };

  const keys = useRef<(e: KeyboardEvent) => void>(() => {});
  useEffect(() => {
    keys.current = (e: KeyboardEvent) => {
      if ((e.target as HTMLElement).closest("input, textarea")) return;
      if (e.key === "Escape") leave(onClose)();
      if (e.key === "ArrowLeft" && prev) leave(() => onGo(prev))();
      if (e.key === "ArrowRight" && next) leave(() => onGo(next))();
    };
  });
  useEffect(() => {
    const k = (e: KeyboardEvent) => keys.current(e);
    window.addEventListener("keydown", k);
    return () => window.removeEventListener("keydown", k);
  }, []);

  return (
    <div className="dialog-backdrop viewer-backdrop" role="dialog" aria-modal="true" aria-label={entry.name} onMouseDown={(e) => e.target === e.currentTarget && leave(onClose)()}>
      <div className="viewer">
        <header className="viewer-head">
          <span className="viewer-ico">
            <FileIcon type={fileType(entry.name, false)} name={entry.name} />
          </span>
          <div className="viewer-title">
            <h2 title={entry.name}>
              {entry.name}
              {dirty && <span className="muted"> •</span>}
            </h2>
            <span className="muted small">
              {kindLabel[kind]} · {fmtBytes(entry.size)} · {fmtDate(entry.modified)}
              {files.length > 1 && i >= 0 && t(" · {v0} of {length}", { v0: i + 1, length: files.length })}
            </span>
          </div>
          <div className="viewer-actions">
            <a className="btn small" href={fileUrl(path, true)} download={entry.name}>
              <Icon name="download" /> <span className="hide-sm">{t("Download")}</span>
            </a>
            {entry.size <= SHARE_MAX && "share" in navigator && (
              <button
                className="btn small"
                disabled={sharing}
                onClick={() => {
                  setSharing(true);
                  shareFile(path, entry.name)
                    .then((ok) => ok || toast(t("This device does not share files; use Download."), true))
                    .catch((e) => (e as Error).name !== "AbortError" && toast(errText(e), true))
                    .finally(() => setSharing(false));
                }}
              >
                <Icon name="share" /> <span className="hide-sm">{sharing ? t("Preparing…") : t("Share")}</span>
              </button>
            )}
            {kind === "pdf" && (
              <a className="btn small hide-sm" href={src} target="_blank" rel="noreferrer">
                <Icon name="open" />{" "}{t("New tab")}
              </a>
            )}
            {writable && (
              <>
                <button className="btn ghost icon small" title={t("Rename")} aria-label={t("Rename")} onClick={onRename}>
                  <Icon name="edit" />
                </button>
                <button className="btn ghost icon small" title={t("Move to…")} aria-label={t("Move")} onClick={onMove}>
                  <Icon name="move" />
                </button>
                <button className="btn ghost icon small danger" title={t("Delete")} aria-label={t("Delete")} onClick={onDelete}>
                  <Icon name="trash" />
                </button>
              </>
            )}
            <button className="btn ghost icon small" aria-label={t("Close")} title={t("Close (Esc)")} onClick={leave(onClose)}>
              <Icon name="x" />
            </button>
          </div>
        </header>
        <div className={`viewer-stage k-${kind}`}>
          {prev && (
            <button className="viewer-nav prev" aria-label={t("Previous: {name}", { name: byPath.get(prev)?.name })} title={t("Previous (←)")} onClick={leave(() => onGo(prev))}>
              <Icon name="chevronLeft" />
            </button>
          )}
          {kind === "text" && entry.size <= TEXT_MAX ? (
            <TextEditor key={path} path={path} writable={writable} startEditing={startEditing} onDirty={setDirty} onSaved={onSaved} />
          ) : (
            <PreviewBody key={path} kind={kind} src={src} path={path} entry={entry} />
          )}
          {next && (
            <button className="viewer-nav next" aria-label={t("Next: {name}", { name: byPath.get(next)?.name })} title={t("Next (→)")} onClick={leave(() => onGo(next))}>
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
  return (
    <div className="viewer-none">
      <FileIcon type={fileType(entry.name, false)} name={entry.name} big />
      <strong>{t("No preview for this type of file")}</strong>
      <span className="muted">{t("Download to open on the device.")}</span>
      <a className="btn primary" href={fileUrl(path, true)} download={entry.name}>
        <Icon name="download" />{" "}{t("Download (")}{fmtBytes(entry.size)})
      </a>
    </div>
  );
}

/** Plain-text viewer that turns into an editor (Ctrl+S saves). */
function TextEditor({
  path,
  writable,
  startEditing,
  onDirty,
  onSaved,
}: {
  path: string;
  writable: boolean;
  startEditing: boolean;
  onDirty: (d: boolean) => void;
  onSaved: () => void;
}) {
  const toast = useToast();
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["nas-text", path], queryFn: () => fetchText(path), staleTime: 0 });
  const [editing, setEditing] = useState(startEditing && writable);
  const [draft, setDraft] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const text = draft ?? q.data ?? "";
  const dirty = draft !== null && draft !== q.data;
  useEffect(() => onDirty(dirty), [dirty, onDirty]);

  const save = async () => {
    if (!dirty) return;
    setSaving(true);
    try {
      await saveText(path, text);
      qc.setQueryData(["nas-text", path], text);
      setDraft(null);
      onSaved();
      toast(t("Saved."));
    } catch (e) {
      toast(errText(e), true);
    } finally {
      setSaving(false);
    }
  };

  if (q.isLoading) return <p className="viewer-none muted">{t("Loading…")}</p>;
  if (q.isError) return <p className="viewer-none">{errText(q.error)}</p>;
  return (
    <div className="text-editor">
      {writable && (
        <div className="editor-bar">
          {editing ? (
            <>
              <span className="muted small">{dirty ? t("Unsaved changes") : t("Editing")}{" "}{t("· Ctrl+S saves")}</span>
              <span className="grow" />
              <button
                className="btn small ghost"
                onClick={() => {
                  if (!dirty || window.confirm(t("Discard the changes?"))) {
                    setDraft(null);
                    setEditing(false);
                  }
                }}
              >
                {t("Cancel")}
              </button>
              <button className="btn small primary" disabled={!dirty || saving} onClick={() => void save()}>
                <Icon name="check" /> {saving ? t("Saving…") : t("Save")}
              </button>
            </>
          ) : (
            <>
              <span className="grow" />
              <button className="btn small" onClick={() => setEditing(true)}>
                <Icon name="edit" />{" "}{t("Edit")}
              </button>
            </>
          )}
        </div>
      )}
      {editing ? (
        <textarea
          className="viewer-text editing"
          value={text}
          autoFocus
          spellCheck={false}
          aria-label={t("File contents")}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => {
            if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "s") {
              e.preventDefault();
              void save();
            }
          }}
        />
      ) : (
        <pre className="viewer-text selectable">{text || <span className="muted">{t("Empty file.")}</span>}</pre>
      )}
    </div>
  );
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
