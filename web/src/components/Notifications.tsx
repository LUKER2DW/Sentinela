import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  allowBackground,
  disablePush,
  enablePush,
  getMode,
  getPrefs,
  isApp,
  nativeStatus,
  pushState,
  putMode,
  putPrefs,
  testPush,
  TYPE_LABEL,
  type ModeSettings,
  type PushState,
} from "../lib/push";
import { useSession, useToast } from "../lib/session";
import { useCompact } from "../lib/viewport";
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
  const compact = useCompact();
  const [details, setDetails] = useState(false);
  const s = q.data?.settings;
  if (!s) return null;
  const effective = q.data!.effective;
  const autoNight = s.mode === "home" && effective === "night";
  if (compact && !details) {
    const cur = MODE_CARDS.find((m) => m.value === effective);
    return (
      <section className="card mode-card compact" aria-label="Modo de avisos da casa">
        <div className="mode-card-head">
          <h2>Modo da casa</h2>
          <button className="linkish small" onClick={() => setDetails(true)}>
            O que cada modo avisa
          </button>
        </div>
        <ModeSwitch compact />
        {cur && (
          <p className="muted small">
            {autoNight ? `Noite automática até ${s.night_to}. ` : ""}Avisa: {cur.notifies.join(", ").toLowerCase()}.
          </p>
        )}
      </section>
    );
  }
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
  insecure: "Notificações no navegador só funcionam pelo endereço HTTPS.",
  denied: "As notificações foram bloqueadas nas configurações deste aparelho.",
  off: "Desligadas neste aparelho.",
  on: "Ligadas neste aparelho.",
};

/** Which event types notify this user, and whether the house mode applies. */
function EventChoices() {
  const qc = useQueryClient();
  const toast = useToast();
  const q = useQuery({ queryKey: ["notify-prefs"], queryFn: getPrefs, staleTime: 60_000 });
  const save = useMutation({
    mutationFn: putPrefs,
    onSuccess: (d) => qc.setQueryData(["notify-prefs"], d),
    onError: (e: Error) => toast(e.message, true),
  });
  const d = q.data;
  if (!d) return null;
  const p = d.prefs;
  const toggle = (t: string) => save.mutate({ ...p, types: p.types.includes(t) ? p.types.filter((x) => x !== t) : [...p.types, t] });
  return (
    <>
      <h3 className="card-sub">O que me avisa</h3>
      <div className="choice-list notify-types">
        {d.types.map((t) => (
          <label key={t} className={`choice${p.types.includes(t) ? " on" : ""}`}>
            <input type="checkbox" checked={p.types.includes(t)} onChange={() => toggle(t)} disabled={save.isPending} />
            <span>{TYPE_LABEL[t] ?? t}</span>
          </label>
        ))}
      </div>
      <div className="toggle-row">
        <span>
          Respeitar o modo da casa
          <span className="muted small"> · {p.follow_mode ? "no modo Casa, por exemplo, só câmera mexida avisa" : "avisa os tipos marcados em qualquer modo"}</span>
        </span>
        <button className="switch" role="switch" aria-checked={p.follow_mode} aria-label="Respeitar o modo da casa" onClick={() => save.mutate({ ...p, follow_mode: !p.follow_mode })} />
      </div>
      <p className="muted small">Só para você: cada pessoa escolhe os próprios avisos. Tudo continua gravado e aparece em Eventos.</p>
    </>
  );
}

/** Notifications card for the System ("Mais") page. */
export function NotificationsCard() {
  const { can } = useSession();
  const qc = useQueryClient();
  const toast = useToast();
  const [state, setState] = useState<PushState | null>(null);
  const [busy, setBusy] = useState(false);
  const mode = useQuery({ queryKey: ["notify-mode"], queryFn: getMode, staleTime: 30_000 });
  const [battery, setBattery] = useState(false);

  useEffect(() => {
    let live = true;
    void pushState()
      .then((s) => {
        if (!live) return;
        setState(s);
      })
      .catch(() => live && setState("unsupported"));
    return () => {
      live = false;
    };
  }, []);

  useEffect(() => {
    if (state === "on") void nativeStatus()?.then((st) => setBattery(st.batteryOptimized)).catch(() => {});
  }, [state]);

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
      {state === "on" && battery && (
        <p className="notice small">
          O Android pode pausar os avisos para economizar bateria.{" "}
          <button className="linkish" onClick={() => void allowBackground()?.then(() => setBattery(false)).catch(() => {})}>
            Permitir em segundo plano
          </button>
        </p>
      )}
      {state === "denied" && isApp() && (
        <button className="btn" onClick={() => void run(enablePush)}>
          Abrir permissões do app
        </button>
      )}
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

      <EventChoices />
      <h3 className="card-sub">Modo da casa</h3>
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
          ? "O app mantém uma conexão direta e cifrada com o servidor da casa; nenhum serviço de terceiros vê os avisos. Tocar num aviso abre a gravação do momento."
          : "A imagem do evento vem direto do servidor; o serviço de avisos do navegador só vê texto cifrado."}
      </p>
    </section>
  );
}
