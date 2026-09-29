import { useEffect, useRef, useState } from "react";
import { useQueries, useQuery } from "@tanstack/react-query";
import { fetchSystem, get, type ControlState } from "../lib/api";
import { buildNotices, isRead, useNoticeReads, type Notice } from "../lib/notices";
import { href, navigate } from "../lib/router";
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

/** Bell in the top bar: unread notices, opens a small list. */
export function NoticeBell() {
  const { all, unread, read, mark } = useSystemNotices();
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const close = (e: Event) => {
      if (e instanceof KeyboardEvent ? e.key === "Escape" : !ref.current?.contains(e.target as Node)) setOpen(false);
    };
    window.addEventListener("pointerdown", close, true);
    window.addEventListener("keydown", close);
    return () => {
      window.removeEventListener("pointerdown", close, true);
      window.removeEventListener("keydown", close);
    };
  }, [open]);
  return (
    <div className="bell" ref={ref}>
      <button
        className="theme-btn bell-btn"
        aria-label={unread.length ? `${unread.length} avisos não lidos` : "Avisos do sistema"}
        title="Avisos do sistema"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
      >
        <Icon name="bell" />
        {unread.length > 0 && <span className="bell-count">{unread.length}</span>}
      </button>
      {open && (
        <div className="bell-pop" role="dialog" aria-label="Avisos do sistema">
          <div className="bell-head">
            <strong>Avisos</strong>
            {unread.length > 0 && (
              <button className="btn ghost small" onClick={() => mark(unread)}>
                <Icon name="check" /> Marcar todos como lidos
              </button>
            )}
          </div>
          {all.length === 0 ? (
            <p className="muted small bell-empty">Tudo certo com o servidor.</p>
          ) : (
            <ul className="bell-list">
              {all.map((n) => (
                <li key={n.id} className={isRead(n, read) ? "read" : ""}>
                  <a
                    href={href("system", { s: n.section })}
                    onClick={(e) => {
                      e.preventDefault();
                      mark([n]);
                      setOpen(false);
                      navigate("system", { s: n.section }, { push: true });
                    }}
                  >
                    <span className={`dot ${n.level === "error" ? "bad" : "warn"}`} />
                    <span>{n.title}</span>
                  </a>
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
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
      <span className={`dot ${n.level === "error" ? "bad" : "warn"}`} />
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
