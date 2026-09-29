import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, get } from "../lib/api";
import { useToast } from "../lib/session";
import { fmtDate } from "../lib/time";

interface DeviceSession {
  id: string;
  created_at: string;
  expires_at: string;
  user_agent: string;
  current: boolean;
  /** Signed in from outside with two-step login (ADR-0019). */
  mfa: boolean;
}

/** A readable name for a session's device, from its user agent. */
export function deviceName(ua: string): string {
  const app = /SentinelaApp/.test(ua);
  const os = /Android/.test(ua)
    ? "Android"
    : /iPhone|iPad/.test(ua)
      ? "iPhone"
      : /Windows/.test(ua)
        ? "Windows"
        : /Mac OS X/.test(ua)
          ? "Mac"
          : /Linux/.test(ua)
            ? "Linux"
            : "Aparelho";
  if (app) return `App no ${os}`;
  const browser = /Edg\//.test(ua) ? "Edge" : /Firefox\//.test(ua) ? "Firefox" : /Chrome\//.test(ua) ? "Chrome" : /Safari\//.test(ua) ? "Safari" : "";
  return browser ? `${browser} no ${os}` : os;
}

/** The user's logged-in devices, each can be signed out (Stage B2). */
export function DevicesCard() {
  const qc = useQueryClient();
  const toast = useToast();
  const q = useQuery({ queryKey: ["my-sessions"], queryFn: () => get<DeviceSession[]>("/api/me/sessions") });
  const revoke = useMutation({
    mutationFn: (id: string) => api("DELETE", `/api/me/sessions/${id}`),
    onSuccess: () => {
      toast("Aparelho desconectado.");
      void qc.invalidateQueries({ queryKey: ["my-sessions"] });
    },
    onError: (e: Error) => toast(e.message, true),
  });
  return (
    <section className="card" aria-label="Aparelhos conectados">
      <h2>Aparelhos conectados</h2>
      <div className="rows">
        {(q.data ?? []).map((s) => (
          <div key={s.id} className="status-row">
            <div>
              <strong>{deviceName(s.user_agent)}</strong>
              <p className="muted small">
                {s.current ? "Este aparelho · " : ""}
                {s.mfa ? "de fora de casa · " : ""}entrou em {fmtDate(s.created_at)}, vale até {fmtDate(s.expires_at)}
              </p>
            </div>
            {!s.current && (
              <button
                className="btn small ghost danger"
                disabled={revoke.isPending}
                onClick={() => window.confirm(`Desconectar ${deviceName(s.user_agent)}?`) && revoke.mutate(s.id)}
              >
                Desconectar
              </button>
            )}
          </div>
        ))}
      </div>
      <p className="muted small card-foot">Perdeu o celular? Desconecte-o aqui, remova-o no painel do Tailscale e, se ele tinha o app autenticador, refaça a verificação em 2 etapas.</p>
    </section>
  );
}
