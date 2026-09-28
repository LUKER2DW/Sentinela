import { useCallback, useEffect, useState } from "react";
import { QueryClient, QueryClientProvider, useQueryClient } from "@tanstack/react-query";
import { api, get, onUnauthorized, setCsrf, type User } from "./lib/api";
import { href, useRoute } from "./lib/router";
import { SessionProvider, ToastProvider, useSession } from "./lib/session";
import { LiveEventsProvider } from "./lib/liveEvents";
import { Icon } from "./components/Icon";
import { Mark, Waves } from "./components/Doodles";
import { ThemeToggle } from "./components/ThemeToggle";
import { AuthPage } from "./pages/AuthPage";
import { LivePage } from "./pages/LivePage";
import { RecordingsPage } from "./pages/RecordingsPage";
import { EventsPage } from "./pages/EventsPage";
import { SystemPage } from "./pages/SystemPage";

const queryClient = new QueryClient({
  defaultOptions: { queries: { refetchOnWindowFocus: true, retry: 1, staleTime: 5_000 } },
});

type Boot = { kind: "loading" } | { kind: "setup" } | { kind: "login" } | { kind: "ready"; user: User } | { kind: "down"; message: string };

async function bootstrap(): Promise<Boot> {
  try {
    const s = await get<{ needs_setup: boolean }>("/api/setup");
    if (s.needs_setup) return { kind: "setup" };
    const me = await get<{ user: User; csrf: string }>("/api/me").catch(() => null);
    if (!me) return { kind: "login" };
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
      setBoot({ kind: "login" });
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
  if (boot.kind !== "ready") return <AuthPage mode={boot.kind} onDone={(user) => setBoot({ kind: "ready", user })} />;

  const logout = () => {
    void api("POST", "/api/logout").finally(() => {
      queryClient.clear();
      setBoot({ kind: "login" });
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
  { page: "system", label: "Sistema", icon: "system" },
];

const roleLabel = { admin: "Administrador", operator: "Operador", viewer: "Visualizador" } as const;

function Shell() {
  const route = useRoute();
  const { user, logout } = useSession();
  const qc = useQueryClient();
  useEffect(() => {
    document.title = `${NAV.find((n) => n.page === route.page)?.label ?? "Sentinela"} – Sentinela`;
  }, [route.page]);

  return (
    <div className="shell">
      <nav className="side-nav" aria-label="Principal">
        <a className="brand" href={href("live")}>
          <Mark />
          <span>Sentinela</span>
        </a>
        {NAV.map((n) => (
          <a
            key={n.page}
            className="nav-item"
            href={href(n.page)}
            aria-current={route.page === n.page ? "page" : undefined}
            onClick={() => void qc.invalidateQueries()}
          >
            <Icon name={n.icon} />
            {n.label}
          </a>
        ))}
        <span className="side-spacer" />
        <div className="side-foot">
          <ThemeToggle />
          <div className="who">
            <div className="who-text">
              <div className="who-name">{user.username}</div>
              <div className="who-role">{roleLabel[user.role]}</div>
            </div>
            <button className="btn ghost icon small" onClick={logout} title="Sair" aria-label="Sair">
              <Icon name="logout" />
            </button>
          </div>
        </div>
        <Waves className="side-waves" />
      </nav>
      <main className="main">
        <div className="page">
          {route.page === "recordings" ? (
            <RecordingsPage params={route.params} />
          ) : route.page === "events" ? (
            <EventsPage />
          ) : route.page === "system" ? (
            <SystemPage />
          ) : (
            <LivePage params={route.params} />
          )}
        </div>
      </main>
    </div>
  );
}
