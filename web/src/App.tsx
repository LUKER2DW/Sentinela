import { useCallback, useEffect, useState } from "react";
import { QueryClient, QueryClientProvider, useQueryClient } from "@tanstack/react-query";
import { api, get, onUnauthorized, setCsrf, type User } from "./lib/api";
import { href, useRoute } from "./lib/router";
import { SessionProvider, ToastProvider, useSession } from "./lib/session";
import { LiveEventsProvider } from "./lib/liveEvents";
import { Icon, Mark } from "./components/Icon";
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
      <div className="auth">
        <div className="auth-card">
          <p className="notice error">O servidor não respondeu ({boot.message}).</p>
          <button className="btn" onClick={load}>
            Tentar de novo
          </button>
        </div>
      </div>
    );
  if (boot.kind !== "ready")
    return <AuthPage mode={boot.kind} onDone={(user) => setBoot({ kind: "ready", user })} />;

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

function Shell() {
  const route = useRoute();
  const { logout } = useSession();
  const qc = useQueryClient();
  useEffect(() => {
    document.title = `${NAV.find((n) => n.page === route.page)?.label ?? "Sentinela"} · Sentinela`;
  }, [route.page]);

  return (
    <div className="shell">
      <nav className="rail" aria-label="Principal">
        <a className="brand-mark" href={href("live")} aria-label="Sentinela">
          <Mark />
        </a>
        {NAV.map((n) => (
          <a key={n.page} className="nav-item" href={href(n.page)} aria-current={route.page === n.page ? "page" : undefined} onClick={() => void qc.invalidateQueries()}>
            <Icon name={n.icon} />
            {n.label}
          </a>
        ))}
        <span className="rail-spacer" />
        <button className="nav-item btn ghost" style={{ border: 0, minHeight: 0 }} onClick={logout} title="Sair">
          <Icon name="logout" />
          Sair
        </button>
      </nav>
      <main className="main">
        {route.page === "recordings" ? (
          <RecordingsPage params={route.params} />
        ) : route.page === "events" ? (
          <EventsPage />
        ) : route.page === "system" ? (
          <SystemPage />
        ) : (
          <LivePage params={route.params} />
        )}
      </main>
    </div>
  );
}
