import { useCallback, useEffect, useState } from "react";
import { QueryClient, QueryClientProvider, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, fetchSystem, get, onUnauthorized, setCsrf, type ReviewList, type User } from "./lib/api";
import { href, useRoute } from "./lib/router";
import { SessionProvider, ToastProvider, useSession } from "./lib/session";
import { LiveEventsProvider, useActiveEvents } from "./lib/liveEvents";
import { toggleSidebar, useSidebarCollapsed } from "./lib/sidebar";
import { startOfDay, useNow } from "./lib/time";
import { Icon } from "./components/Icon";
import { OceanScene } from "./components/Doodles";
import { LighthouseMark } from "./components/Lighthouse";
import { ThemeToggle } from "./components/ThemeToggle";
import { BgDoodles } from "./components/Decor";
import { AuthPage } from "./pages/AuthPage";
import { LivePage } from "./pages/LivePage";
import { RecordingsPage } from "./pages/RecordingsPage";
import { EventsPage } from "./pages/EventsPage";
import { SystemPage } from "./pages/SystemPage";
import { NuvemPage } from "./pages/NuvemPage";
import { ClockPage } from "./components/DelayMeter";
import { setZone } from "./lib/access";

const queryClient = new QueryClient({
  defaultOptions: { queries: { refetchOnWindowFocus: true, retry: 1, staleTime: 5_000 } },
});

type Boot =
  | { kind: "loading" }
  | { kind: "setup" }
  | { kind: "login"; mfa?: boolean }
  | { kind: "ready"; user: User }
  | { kind: "down"; message: string };

// Version the page was loaded with; a later deploy shows an "update" notice.
let loadedVersion = "";

async function bootstrap(): Promise<Boot> {
  try {
    const s = await get<{ needs_setup: boolean; version?: string; zone?: string; mfa?: boolean }>("/api/setup");
    loadedVersion ||= s.version ?? "";
    setZone(s.zone);
    if (s.needs_setup) return { kind: "setup" };
    const me = await get<{ user: User; csrf: string }>("/api/me").catch(() => null);
    if (!me) return { kind: "login", mfa: s.mfa };
    setCsrf(me.csrf);
    return { kind: "ready", user: me.user };
  } catch (e) {
    return { kind: "down", message: (e as Error).message };
  }
}

export function App() {
  const [boot, setBoot] = useState<Boot>({ kind: "loading" });

  const load = useCallback(() => {
    void bootstrap().then(setBoot);
  }, []);

  useEffect(() => {
    let live = true;
    void bootstrap().then((b) => live && setBoot(b));
    const off = onUnauthorized(() => {
      queryClient.clear();
      setBoot((b) => ({ kind: "login", mfa: b.kind === "login" ? b.mfa : undefined }));
      void bootstrap().then(setBoot);
    });
    return () => {
      live = false;
      off();
    };
  }, []);

  if (boot.kind === "loading") return null;
  if (boot.kind === "down")
    return (
      <AuthPage mode="login" onDone={() => {}} downMessage={boot.message} onRetry={load} />
    );
  if (boot.kind !== "ready")
    return <AuthPage mode={boot.kind} mfa={boot.kind === "login" && !!boot.mfa} onDone={(user) => setBoot({ kind: "ready", user })} />;

  const logout = () => {
    void api("POST", "/api/logout").finally(() => {
      queryClient.clear();
      void bootstrap().then(setBoot);
    });
  };

  return (
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <SessionProvider user={boot.user} onLogout={logout}>
          <LiveEventsProvider>
            <Shell />
          </LiveEventsProvider>
        </SessionProvider>
      </ToastProvider>
    </QueryClientProvider>
  );
}

const NAV = [
  { page: "live", label: "Ao vivo", icon: "live" },
  { page: "recordings", label: "Gravações", icon: "recordings" },
  { page: "events", label: "Eventos", icon: "events" },
  { page: "nuvem", label: "Nuvem", icon: "cloud" },
  { page: "system", label: "Sistema", short: "Mais", icon: "system" },
];

const roleLabel = { admin: "Administrador", operator: "Operador", viewer: "Visualizador" } as const;

/** True when the server runs a newer build than this page (auto deploy). */
function useNewVersion(): boolean {
  const q = useQuery({
    queryKey: ["version"],
    queryFn: () => get<{ version?: string }>("/api/setup"),
    refetchInterval: 120_000,
  });
  const v = q.data?.version;
  return !!v && !!loadedVersion && v !== loadedVersion;
}

/** Today's unseen review items, for the Eventos badge (matches "Hoje"). */
function useUnseenEvents(): number {
  const since = startOfDay(useNow()).toISOString();
  const q = useQuery({
    queryKey: ["review", "badge", since],
    queryFn: () => get<ReviewList>(`/api/review?from=${since}&unseen=1`),
    refetchInterval: 60_000,
  });
  return q.data?.unseen ?? 0;
}

function Shell() {
  const route = useRoute();
  const { user, logout } = useSession();
  const qc = useQueryClient();
  const collapsed = useSidebarCollapsed();
  const eventsToday = useUnseenEvents();
  const updated = useNewVersion();
  // Keeps the UI clock on server time on every page (shared cache with the pages).
  useQuery({ queryKey: ["system"], queryFn: fetchSystem, refetchInterval: 60_000 });
  const active = useActiveEvents();
  const motionNow = Object.values(active).some((t) => t.length > 0);
  useEffect(() => {
    const label = route.page === "relogio" ? "Relógio" : NAV.find((n) => n.page === route.page)?.label;
    document.title = label ? `${label} – Sentinela` : "Sentinela";
  }, [route.page]);

  const badge = (page: string) => {
    if (page === "events" && eventsToday > 0) return <span className="nav-badge">{eventsToday > 99 ? "99+" : eventsToday}</span>;
    if (page === "live" && motionNow) return <span className="nav-dot" title="Movimento agora" />;
    return null;
  };

  return (
    <div className={`shell${collapsed ? " collapsed" : ""}`}>
      <nav className="side-nav" aria-label="Principal">
        <div className="side-top">
          <a className="brand" href={href("live")} title="Sentinela">
            <LighthouseMark />
            <span className="brand-text">
              <span className="brand-name">Sentinela</span>
              <span className="brand-sub">Câmeras e nuvem</span>
            </span>
          </a>
          <button className="btn ghost icon small collapse-btn" onClick={toggleSidebar} aria-label={collapsed ? "Expandir menu" : "Recolher menu"} title={collapsed ? "Expandir menu" : "Recolher menu"} aria-expanded={!collapsed}>
            <Icon name="sidebar" />
          </button>
        </div>
        <div className="nav-list">
          {NAV.map((n) => (
            <a
              key={n.page}
              className="nav-item"
              href={href(n.page)}
              title={collapsed ? n.label : undefined}
              aria-current={route.page === n.page ? "page" : undefined}
              onClick={() => void qc.invalidateQueries()}
            >
              <span className="nav-icon">
                <Icon name={n.icon} />
                {badge(n.page)}
              </span>
              <span className="nav-label">
                {"short" in n ? (
                  <>
                    <span className="hide-sm">{n.label}</span>
                    <span className="show-sm">{n.short}</span>
                  </>
                ) : (
                  n.label
                )}
              </span>
            </a>
          ))}
        </div>
        <span className="side-spacer" />
        <div className="side-sea">
          <OceanScene className="side-ocean" />
          <div className="who">
            <span className="avatar" aria-hidden="true">
              {user.username.slice(0, 1).toUpperCase()}
            </span>
            <div className="who-text">
              <div className="who-name">{user.username}</div>
              <div className="who-role">{roleLabel[user.role]}</div>
            </div>
            <button className="btn ghost icon small" onClick={logout} title="Sair" aria-label="Sair">
              <Icon name="logout" />
            </button>
          </div>
        </div>
      </nav>
      <main className="main">
        <BgDoodles />
        {updated && (
          <button className="update-toast" onClick={() => location.reload()}>
            <Icon name="refresh" /> Nova versão do Sentinela. Atualizar a página
          </button>
        )}
        <div className="topbar">
          <ThemeToggle />
        </div>
        <div className="page">
          {route.page === "recordings" ? (
            <RecordingsPage params={route.params} />
          ) : route.page === "events" ? (
            <EventsPage />
          ) : route.page === "nuvem" ? (
            <NuvemPage params={route.params} />
          ) : route.page === "system" ? (
            <SystemPage />
          ) : route.page === "relogio" ? (
            <ClockPage />
          ) : (
            <LivePage params={route.params} />
          )}
        </div>
      </main>
    </div>
  );
}
