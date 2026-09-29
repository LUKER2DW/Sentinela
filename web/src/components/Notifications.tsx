import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { get } from "../lib/api";
import { disablePush, enablePush, getMode, isApp, pushState, putMode, refreshNativePush, testPush, type ModeSettings, type PushState } from "../lib/push";
import { useSession, useToast } from "../lib/session";
import { Icon } from "./Icon";

export const MODES: { value: ModeSettings["mode"]; label: string; hint: string }[] = [
  { value: "home", label: "Casa", hint: "Só avisa se mexerem na câmera." },
  { value: "night", label: "Noite", hint: "Avisa alertas: pessoa, linha cruzada, área invadida, câmera mexida." },
  { value: "away", label: "Fora", hint: "Avisa tudo, inclusive movimento." },
];

const MODE_CARDS: { value: ModeSettings["mode"]; label: string; icon: string; when: string; notifies: string[]; quiet: string }[] = [
  {
    value: "home",
    label: "Casa",
    icon: "home",
    when: "Tem gente em casa, de dia.",
    notifies: ["Câmera obstruída ou mexida"],
    quiet: "Pessoas, carros e movimento só ficam registrados.",
  },
  {
    value: "night",
    label: "Noite",
    icon: "moon",
    when: "Todos dormindo.",
    notifies: ["Pessoa", "Linha cruzada ou área invadida", "Câmera obstruída"],
    quiet: "Veículos, animais e movimento só ficam registrados.",
  },
  {
    value: "away",
    label: "Fora",
    icon: "away",
    when: "Casa vazia.",
    notifies: ["Pessoa, veículo e animal", "Linha, área e obstrução", "Movimento (se o detector estiver parado)"],
    quiet: "Tudo o que acontece avisa.",
  },
];

/**
 * Big, explicit Casa / Noite / Fora picker for the Eventos page: what each
 * mode sends to the phone, which one is on now, and the automatic night.
 */
export function ModeCard() {
  const { can } = useSession();
  const qc = useQueryClient();
  const toast = useToast();
  const q = useQuery({ queryKey: ["notify-mode"], queryFn: getMode, staleTime: 30_000 });
  const set = useMutation({
    mutationFn: putMode,
    onSuccess: (d) => {
      qc.setQueryData(["notify-mode"], d);
      toast(`Modo ${MODES.find((m) => m.value === d.settings.mode)?.label} ativado`);
    },
    onError: (e: Error) => toast(e.message, true),
  });
  const s = q.data?.settings;
  if (!s) return null;
  const effective = q.data!.effective;
  const autoNight = s.mode === "home" && effective === "night";
  return (
    <section className="card mode-card" aria-label="Modo de avisos da casa">
      <div className="mode-card-head">
        <div>
          <h2>Modo da casa</h2>
          <p className="muted small">
            Define o que manda aviso para o celular. Tudo continua gravado e aparece aqui nos Eventos, em qualquer modo.
          </p>
        </div>
        <span className="pill ok">
          <span className="dot ok" /> Agora: {MODES.find((m) => m.value === effective)?.label}
          {autoNight && ` (automático até ${s.night_to})`}
        </span>
      </div>
      <div className="mode-options" role="radiogroup" aria-label="Modo">
        {MODE_CARDS.map((m) => {
          const on = s.mode === m.value;
          return (
            <button
              key={m.value}
              role="radio"
              aria-checked={on}
              className={`mode-option${on ? " on" : ""}${effective === m.value && !on ? " auto" : ""}`}
              disabled={!can("operator") || set.isPending}
              onClick={() => !on && set.mutate({ ...s, mode: m.value })}
            >
              <span className="mode-option-head">
                <Icon name={m.icon} />
                <strong>{m.label}</strong>
                {on && <span className="mode-on">Ativo</span>}
                {!on && effective === m.value && <span className="mode-on soft">Automático</span>}
              </span>
              <span className="muted small">{m.when}</span>
              <span className="mode-list-title small">Avisa:</span>
              <ul className="mode-list">
                {m.notifies.map((n) => (
                  <li key={n}>{n}</li>
                ))}
              </ul>
              <span className="muted small">{m.quiet}</span>
            </button>
          );
        })}
      </div>
      <p className="muted small mode-foot">
        {s.night_auto
          ? `No modo Casa, a Noite liga sozinha das ${s.night_from} às ${s.night_to}.`
          : "A Noite automática está desligada."}{" "}
        Ajuste em Minha conta → Notificações.
        {!can("operator") && " Seu perfil só permite ver o modo."}
      </p>
    </section>
  );
}

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
  "no-ntfy": "No app, os avisos chegam pelo ntfy. Instale o ntfy e, nas configurações dele, mude o Servidor padrão para o endereço abaixo.",
  "no-relay": "O servidor da casa ainda não tem o ntfy configurado (scripts/setup-ntfy.sh).",
};

/** Notifications card for the System ("Mais") page. */
export function NotificationsCard() {
  const { can } = useSession();
  const qc = useQueryClient();
  const toast = useToast();
  const [state, setState] = useState<PushState | null>(null);
  const [busy, setBusy] = useState(false);
  const mode = useQuery({ queryKey: ["notify-mode"], queryFn: getMode, staleTime: 30_000 });
  const info = useQuery({ queryKey: ["push-info"], queryFn: () => get<{ ntfy_url: string }>("/api/push"), enabled: isApp(), staleTime: 300_000 });

  useEffect(() => {
    let live = true;
    void pushState()
      .then((s) => {
        if (!live) return;
        setState(s);
        if (s === "on") void refreshNativePush().catch(() => {});
      })
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
      {state === "no-ntfy" && info.data?.ntfy_url && <code className="copy-line">{info.data.ntfy_url}</code>}
      <div className="row-actions">
        {state === "no-ntfy" && (
          <>
            <a className="btn primary" href="https://play.google.com/store/apps/details?id=io.heckel.ntfy" target="_blank" rel="noreferrer">
              Instalar o ntfy
            </a>
            <button className="btn" disabled={busy} onClick={() => void run(pushState)}>
              Já instalei
            </button>
          </>
        )}
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
      <p className="muted small card-foot">
        {isApp()
          ? "Os avisos passam pelo ntfy da própria casa, cifrados; a imagem vem direto do servidor pelo Tailscale."
          : "A imagem do evento vem direto do servidor pelo Tailscale; o serviço de avisos do navegador só vê texto cifrado."}
      </p>
    </section>
  );
}
