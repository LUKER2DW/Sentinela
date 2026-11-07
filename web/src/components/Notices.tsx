import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, fetchSystem, get, type ControlState, type ReviewItem, type ReviewList } from "../lib/api";
import { buildNotices, isRead, useNoticeReads, type Notice } from "../lib/notices";
import { href, navigate } from "../lib/router";
import { eventLabel, fmtTime, relative, startOfDay, useNow } from "../lib/time";
import { useCompact } from "../lib/viewport";
import { useCameras } from "../pages/LivePage";
import { Icon } from "./Icon";

/** The server's current notices, with this device's read state. */
export function useSystemNotices() {
  const sys = useQuery({ queryKey: ["system"], queryFn: fetchSystem, refetchInterval: 60_000 });
  const cams = useCameras();
  const states = useQueries({
    queries: (cams.data ?? []).map((c) => ({
      queryKey: ["control", c.id],
      queryFn: () => get<ControlState>(`/api/cameras/${c.id}/control`),
      staleTime: 5 * 60_000,
      refetchOnWindowFocus: false,
      retry: 1,
    })),
  });
  const { read, mark } = useNoticeReads();
  const name = (id: string) => cams.data?.find((c) => c.id === id)?.name ?? id;
  const cloudOn = (cams.data ?? []).filter((_, i) => states[i]?.data?.cloud_p2p === true).map((c) => c.id);
  const all = sys.data ? buildNotices(sys.data, name, cloudOn) : [];
  const unread = all.filter((n) => !isRead(n, read));
  return { all, unread, read, mark };
}

/** Today's camera events for the inbox (same query as the Eventos badge). */
function useTodayEvents() {
  const qc = useQueryClient();
  const since = startOfDay(useNow()).toISOString();
  const q = useQuery({
    queryKey: ["review", "inbox", since],
    queryFn: () => get<ReviewList>(`/api/review?from=${since}`),
    refetchInterval: 60_000,
  });
  const seen = useMutation({
    mutationFn: (body: { ids?: number[]; all?: boolean }) => api("POST", `/api/review/seen?from=${since}`, body),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["review"] }),
  });
  return { items: q.data?.items ?? [], unseen: q.data?.unseen ?? 0, loaded: q.isSuccess, seen };
}

const typesText = (it: ReviewItem) => [...new Set(it.types.map((t) => eventLabel[t] ?? t))].join(", ") || "Evento";

type Tab = "events" | "system";

/**
 * Bell in the top bar: one inbox with two tabs, camera events of today and
 * system notices (disks, gaps, backups). A popover on the computer, a
 * bottom sheet on the phone.
 */
export function NoticeBell() {
  const sys = useSystemNotices();
  const ev = useTodayEvents();
  const cams = useCameras();
  const compact = useCompact();
  const [open, setOpen] = useState(false);
  const [tab, setTab] = useState<Tab>("events");
  const ref = useRef<HTMLDivElement>(null);
  const total = sys.unread.length + ev.unseen;

  // Any navigation (a tab, the back button) closes it.
  useEffect(() => {
    const shut = () => setOpen(false);
    window.addEventListener("hashchange", shut);
    return () => window.removeEventListener("hashchange", shut);
  }, []);

  useEffect(() => {
    if (!open) return;
    const close = (e: Event) => {
      if (e instanceof KeyboardEvent ? e.key === "Escape" : !ref.current?.contains(e.target as Node)) setOpen(false);
    };
    // The phone sheet lives in a portal and closes on its own backdrop.
    if (!compact) window.addEventListener("pointerdown", close, true);
    window.addEventListener("keydown", close);
    return () => {
      window.removeEventListener("pointerdown", close, true);
      window.removeEventListener("keydown", close);
    };
  }, [open, compact]);

  const toggle = () => {
    // Opens on what needs attention: system problems first, then new events.
    if (!open) setTab(sys.unread.some((n) => n.level === "error") || (sys.unread.length && !ev.unseen) ? "system" : "events");
    setOpen((v) => !v);
  };
  const camName = (id: string) => cams.data?.find((c) => c.id === id)?.name ?? id;
  const multi = (cams.data?.length ?? 0) > 1;

  const panel = (
    <div className={`inbox${compact ? " sheet" : ""}`} role="dialog" aria-label="Avisos">
      <div className="inbox-head">
        <strong>Avisos</strong>
        <div className="inbox-tabs" role="tablist" aria-label="Tipo de aviso">
          <button role="tab" aria-selected={tab === "events"} onClick={() => setTab("events")}>
            Eventos {ev.unseen > 0 && <span className="count-badge">{ev.unseen > 99 ? "99+" : ev.unseen}</span>}
          </button>
          <button role="tab" aria-selected={tab === "system"} onClick={() => setTab("system")}>
            Sistema {sys.unread.length > 0 && <span className="count-badge">{sys.unread.length}</span>}
          </button>
        </div>
        {compact && (
          <button className="btn ghost icon small" aria-label="Fechar" onClick={() => setOpen(false)}>
            <Icon name="x" />
          </button>
        )}
      </div>

      <div className="inbox-body" role="tabpanel">
        {tab === "events" ? (
          ev.items.length === 0 ? (
            <InboxEmpty icon="events" title={ev.loaded ? "Nenhum evento hoje" : "Carregando…"} text={ev.loaded ? "Pessoas, carros e alertas das câmeras aparecem aqui." : ""} />
          ) : (
            <ul className="inbox-list">
              {ev.items.slice(0, 20).map((it) => (
                <li key={it.id}>
                  <button
                    className={`inbox-item event${it.seen ? " read" : ""}${it.severity === "alert" ? " alert" : ""}`}
                    onClick={() => {
                      if (!it.seen) ev.seen.mutate({ ids: [it.id] });
                      setOpen(false);
                      navigate("recordings", { cam: it.camera_id, t: new Date(new Date(it.start).getTime() - 5000).toISOString(), ev: String(it.id) }, { push: true });
                    }}
                  >
                    <span className="inbox-thumb">
                      {it.snapshot_event ? <img src={`/api/events/${it.snapshot_event}/snapshot`} alt="" loading="lazy" decoding="async" /> : <Icon name="events" />}
                    </span>
                    <span className="inbox-text">
                      <strong>{typesText(it)}</strong>
                      <span className="muted small">
                        {multi ? `${camName(it.camera_id)}, ` : ""}
                        {fmtTime(it.start)} ({relative(it.start)})
                      </span>
                    </span>
                    {!it.seen && <span className="inbox-dot" aria-label="Novo" />}
                  </button>
                </li>
              ))}
            </ul>
          )
        ) : sys.all.length === 0 ? (
          <InboxEmpty icon="check" ok title="Tudo em ordem" text="Gravação, discos e backups sem problemas." />
        ) : (
          <ul className="inbox-list">
            {[...sys.unread, ...sys.all.filter((n) => isRead(n, sys.read))].map((n) => (
              <li key={n.id}>
                <NoticeItem
                  n={n}
                  read={isRead(n, sys.read)}
                  onOpen={() => {
                    sys.mark([n]);
                    setOpen(false);
                    navigate("system", { s: n.section }, { push: true });
                  }}
                  onToggle={() => sys.mark([n], !isRead(n, sys.read))}
                />
              </li>
            ))}
          </ul>
        )}
      </div>

      <div className="inbox-foot">
        {tab === "events" ? (
          <>
            {ev.unseen > 0 && (
              <button className="btn ghost small" disabled={ev.seen.isPending} onClick={() => ev.seen.mutate({ all: true })}>
                <Icon name="check" /> Marcar todos como vistos
              </button>
            )}
            <a className="btn ghost small inbox-more" href={href("events")} onClick={() => setOpen(false)}>
              Abrir Eventos
            </a>
          </>
        ) : (
          <>
            {sys.unread.length > 0 && (
              <button className="btn ghost small" onClick={() => sys.mark(sys.unread)}>
                <Icon name="check" /> Marcar todos como lidos
              </button>
            )}
            <a className="btn ghost small inbox-more" href={href("system")} onClick={() => setOpen(false)}>
              Abrir Sistema
            </a>
          </>
        )}
      </div>
    </div>
  );

  return (
    <div className="bell" ref={ref}>
      <button className="theme-btn bell-btn" aria-label={total ? `${total} avisos não lidos` : "Avisos"} title="Avisos" aria-expanded={open} onClick={toggle}>
        <Icon name="bell" />
        {total > 0 && <span className={`bell-count${sys.unread.some((n) => n.level === "error") ? " bad" : ""}`}>{total > 99 ? "99+" : total}</span>}
      </button>
      {open &&
        (compact
          ? // Portal: the phone bar's backdrop-filter would trap a fixed sheet.
            createPortal(
              <div className="dialog-backdrop bottom inbox-backdrop" onMouseDown={(e) => e.target === e.currentTarget && setOpen(false)}>
                {panel}
              </div>,
              document.body,
            )
          : panel)}
    </div>
  );
}

function InboxEmpty({ icon, title, text, ok }: { icon: string; title: string; text: string; ok?: boolean }) {
  return (
    <div className="inbox-empty">
      <span className={`set-ico${ok ? " kelp" : ""}`}>
        <Icon name={icon} />
      </span>
      <strong>{title}</strong>
      {text && <span className="muted small">{text}</span>}
    </div>
  );
}

/** One system notice: severity icon, what happened, what to do. */
function NoticeItem({ n, read, onOpen, onToggle }: { n: Notice; read: boolean; onOpen: () => void; onToggle: () => void }) {
  return (
    <div className={`inbox-item notice-item ${n.level}${read ? " read" : ""}`}>
      <span className={`level-ico ${n.level}`} aria-hidden="true">
        <Icon name={n.level === "error" ? "alert" : "info"} />
      </span>
      <button className="inbox-text" onClick={onOpen}>
        <strong>{n.title}</strong>
        <span className="muted small clamp-2">{n.text}</span>
      </button>
      <button className="btn ghost icon small" onClick={onToggle} title={read ? "Marcar como não lido" : "Marcar como lido"} aria-label={read ? "Marcar como não lido" : "Marcar como lido"}>
        <Icon name={read ? "bell" : "check"} />
      </button>
    </div>
  );
}

/** Full list on the System overview, with mark-as-read. */
export function NoticesPanel() {
  const { all, unread, read, mark } = useSystemNotices();
  const [showRead, setShowRead] = useState(false);
  const readList = all.filter((n) => isRead(n, read));
  if (!all.length)
    return (
      <div className="notice ok-notice">
        <Icon name="check" /> Nenhum aviso: gravação, discos e backups em ordem.
      </div>
    );
  return (
    <section className="notices" aria-label="Avisos do sistema">
      <div className="notices-head">
        <h2>Avisos {unread.length > 0 && <span className="count-badge">{unread.length}</span>}</h2>
        {unread.length > 0 && (
          <button className="btn small ghost" onClick={() => mark(unread)}>
            <Icon name="check" /> Marcar todos como lidos
          </button>
        )}
      </div>
      {unread.map((n) => (
        <NoticeRow key={n.id} n={n} onRead={() => mark([n])} />
      ))}
      {readList.length > 0 && (
        <button className="linkish small" onClick={() => setShowRead((v) => !v)}>
          {showRead ? "Esconder os lidos" : `Mostrar ${readList.length} ${readList.length === 1 ? "aviso lido" : "avisos lidos"}`}
        </button>
      )}
      {showRead && readList.map((n) => <NoticeRow key={n.id} n={n} read onUnread={() => mark([n], false)} />)}
    </section>
  );
}

function NoticeRow({ n, read, onRead, onUnread }: { n: Notice; read?: boolean; onRead?: () => void; onUnread?: () => void }) {
  return (
    <div className={`notice-row ${n.level}${read ? " read" : ""}`} role={read ? undefined : "alert"}>
      <span className={`level-ico ${n.level}`} aria-hidden="true">
        <Icon name={n.level === "error" ? "alert" : "info"} />
      </span>
      <div className="notice-text">
        <strong>{n.title}</strong>
        <p>{n.text}</p>
      </div>
      {read ? (
        <button className="btn ghost small" onClick={onUnread}>
          Marcar como não lido
        </button>
      ) : (
        <button className="btn ghost small" onClick={onRead} title="Esconde este aviso até algo mudar">
          <Icon name="check" /> Marcar como lido
        </button>
      )}
    </div>
  );
}
