import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { disablePush, enablePush, getMode, pushState, putMode, testPush, type ModeSettings, type PushState } from "../lib/push";
import { useSession, useToast } from "../lib/session";
import { Icon } from "./Icon";

export const MODES: { value: ModeSettings["mode"]; label: string; hint: string }[] = [
  { value: "home", label: "Casa", hint: "Só avisa se mexerem na câmera." },
  { value: "night", label: "Noite", hint: "Avisa alertas: pessoa, linha cruzada, área invadida, câmera mexida." },
  { value: "away", label: "Fora", hint: "Avisa tudo, inclusive movimento." },
];

/** Quick Casa / Noite / Fora switch (operators change it for the house). */
export function ModeSwitch({ compact }: { compact?: boolean }) {
  const { can } = useSession();
  const qc = useQueryClient();
  const toast = useToast();
  const q = useQuery({ queryKey: ["notify-mode"], queryFn: getMode, staleTime: 30_000 });
  const set = useMutation({
    mutationFn: putMode,
    onSuccess: (d) => {
      qc.setQueryData(["notify-mode"], d);
      toast(`Modo ${MODES.find((m) => m.value === d.settings.mode)?.label}`);
    },
    onError: (e: Error) => toast(e.message, true),
  });
  const s = q.data?.settings;
  if (!s) return null;
  const autoNight = s.mode === "home" && q.data?.effective === "night";
  return (
    <div className={`mode-switch${compact ? " compact" : ""}`}>
      <div className="segmented" role="group" aria-label="Modo de avisos">
        {MODES.map((m) => (
          <button
            key={m.value}
            aria-pressed={s.mode === m.value}
            disabled={!can("operator") || set.isPending}
            title={m.hint}
            onClick={() => set.mutate({ ...s, mode: m.value })}
          >
            {m.label}
          </button>
        ))}
      </div>
      {!compact && <p className="muted small">{MODES.find((m) => m.value === s.mode)?.hint}</p>}
      {autoNight && <span className="pill small-pill">Noite automática até {s.night_to}</span>}
    </div>
  );
}

const stateText: Record<PushState, string> = {
  unsupported: "Este navegador não recebe notificações. No iPhone, instale o Sentinela na tela de início primeiro.",
  insecure: "Notificações só funcionam pelo endereço HTTPS (Tailscale).",
  denied: "As notificações foram bloqueadas nas configurações do navegador para este site.",
  off: "Desligadas neste aparelho.",
  on: "Ligadas neste aparelho.",
};

/** Notifications card for the System ("Mais") page. */
export function NotificationsCard() {
  const { can } = useSession();
  const qc = useQueryClient();
  const toast = useToast();
  const [state, setState] = useState<PushState | null>(null);
  const [busy, setBusy] = useState(false);
  const mode = useQuery({ queryKey: ["notify-mode"], queryFn: getMode, staleTime: 30_000 });

  useEffect(() => {
    let live = true;
    void pushState()
      .then((s) => live && setState(s))
      .catch(() => live && setState("unsupported"));
    return () => {
      live = false;
    };
  }, []);

  const run = async (fn: () => Promise<PushState>) => {
    setBusy(true);
    try {
      setState(await fn());
    } catch (e) {
      toast((e as Error).message, true);
    } finally {
      setBusy(false);
    }
  };

  const saveNight = useMutation({
    mutationFn: putMode,
    onSuccess: (d) => qc.setQueryData(["notify-mode"], d),
    onError: (e: Error) => toast(e.message, true),
  });
  const s = mode.data?.settings;

  return (
    <section className="card" aria-label="Notificações">
      <div className="card-head">
        <h2>Notificações</h2>
        {state === "on" && (
          <span className="pill">
            <span className="dot ok" /> Neste aparelho
          </span>
        )}
      </div>
      <p className="muted">{state ? stateText[state] : "Verificando…"}</p>
      <div className="row-actions">
        {state === "off" && (
          <button className="btn primary" disabled={busy} onClick={() => void run(enablePush)}>
            <Icon name="events" /> Ativar neste aparelho
          </button>
        )}
        {state === "on" && (
          <>
            <button
              className="btn"
              disabled={busy}
              onClick={() => {
                setBusy(true);
                testPush()
                  .then((r) => toast(r.sent ? "Teste enviado." : "Nenhum aparelho recebeu. Ative de novo.", !r.sent))
                  .catch((e: Error) => toast(e.message, true))
                  .finally(() => setBusy(false));
              }}
            >
              Enviar teste
            </button>
            <button className="btn ghost" disabled={busy} onClick={() => void run(disablePush)}>
              Desativar
            </button>
          </>
        )}
      </div>

      <h3 className="card-sub">Quando avisar</h3>
      <ModeSwitch />
      {s && (
        <div className="night-row">
          <div className="toggle-row">
            <span>Noite automática no modo Casa</span>
            <button
              className="switch"
              role="switch"
              aria-checked={s.night_auto}
              aria-label="Noite automática"
              disabled={!can("operator")}
              onClick={() => saveNight.mutate({ ...s, night_auto: !s.night_auto })}
            />
          </div>
          <div className="night-range">
            <span className="muted">Das</span>
            <input
              className="input"
              type="time"
              aria-label="Início da noite"
              value={s.night_from}
              disabled={!can("operator") || !s.night_auto}
              onChange={(e) => e.target.value && saveNight.mutate({ ...s, night_from: e.target.value })}
            />
            <span className="muted">às</span>
            <input
              className="input"
              type="time"
              aria-label="Fim da noite"
              value={s.night_to}
              disabled={!can("operator") || !s.night_auto}
              onChange={(e) => e.target.value && saveNight.mutate({ ...s, night_to: e.target.value })}
            />
          </div>
        </div>
      )}
      <p className="muted small card-foot">A imagem do evento vem direto do servidor pelo Tailscale; o serviço de avisos do navegador só vê texto cifrado.</p>
    </section>
  );
}
