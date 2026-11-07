import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, get } from "../lib/api";
import { useToast } from "../lib/session";
import { fmtDate } from "../lib/time";
import { Icon } from "./Icon";
import { CardHead } from "./CardHead";

interface DeviceSession {
  id: string;
  created_at: string;
  expires_at: string;
  user_agent: string;
  current: boolean;
  /** Signed in from outside with two-step login (ADR-0019). */
  mfa: boolean;
}

/**
 * A readable name for a session's device, from its user agent. The Android
 * app adds "SentinelaApp/1"; older app sessions (user agent cut at 200
 * characters) are still recognised by the WebView marker "; wv)".
 */
export function deviceName(ua: string): string {
  const model = /Android [\d.]+; ([^;)]+?)(?: Build\/[^;)]*)?[;)]/.exec(ua)?.[1]?.trim();
  const phone = model && !/^(K|wv|Linux)$/.test(model) ? ` (${model})` : "";
  if (/SentinelaApp/.test(ua) || (/Android/.test(ua) && /; wv\)/.test(ua))) return `Aplicativo Android${phone}`;
  const os = /Android/.test(ua)
    ? "Android"
    : /iPhone/.test(ua)
      ? "iPhone"
      : /iPad/.test(ua)
        ? "iPad"
        : /Windows/.test(ua)
          ? "Windows"
          : /CrOS/.test(ua)
            ? "Chromebook"
            : /Mac OS X/.test(ua)
              ? "Mac"
              : /Linux/.test(ua)
                ? "Linux"
                : "Aparelho";
  const browser = /Edg(A|iOS)?\//.test(ua)
    ? "Edge"
    : /OPR\/|Opera/.test(ua)
      ? "Opera"
      : /SamsungBrowser\//.test(ua)
        ? "Samsung Internet"
        : /Firefox\/|FxiOS/.test(ua)
          ? "Firefox"
          : /Chrome\/|CriOS/.test(ua)
            ? "Chrome"
            : /Safari\//.test(ua)
              ? "Safari"
              : "";
  return browser ? `${browser} no ${os}${os === "Android" ? phone : ""}` : os;
}

/** Icon for a device row. */
export function deviceIcon(ua: string): string {
  return /SentinelaApp|Android|iPhone/.test(ua) ? "phone" : "monitor";
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
    <section className="card set-card" aria-label="Aparelhos conectados">
      <CardHead
        icon="monitor"
        title="Aparelhos conectados"
        sub="Onde esta conta está aberta agora. Desconecte o que você não reconhece."
        aside={q.data && <span className="pill num">{q.data.length}</span>}
      />
      <div className="rows">
        {(q.data ?? []).map((s) => (
          <div key={s.id} className="status-row device-row">
            <span className="device-icon" aria-hidden="true">
              <Icon name={deviceIcon(s.user_agent)} />
            </span>
            <div className="grow">
              <strong>
                {deviceName(s.user_agent)}
                {s.current && <span className="tag-inline">Este aparelho</span>}
              </strong>
              <p className="muted small">
                {s.mfa ? "De fora de casa. " : ""}Entrou em {fmtDate(s.created_at)}, vale até {fmtDate(s.expires_at)}
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
      <p className="muted small card-foot">Perdeu o celular? Desconecte-o aqui e, se ele tinha o app autenticador, refaça a verificação em 2 etapas.</p>
    </section>
  );
}
