import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  allowBackground,
  hideOngoing,
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
import { CardHead } from "./CardHead";

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
        Ajuste em Minha conta, em Notificações.
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
  off: "Desligadas neste aparelho. Ative para receber os avisos aqui.",
  on: "Este aparelho recebe os avisos, mesmo com o Sentinela fechado.",
};

/** This device's notification state; null while checking. */
export function usePushState() {
  const [state, setState] = useState<PushState | null>(null);
  useEffect(() => {
    let live = true;
    void pushState()
      .then((s) => live && setState(s))
      .catch(() => live && setState("unsupported"));
    return () => {
      live = false;
    };
  }, []);
  return [state, setState] as const;
}

/** Turn notifications on or off on this device, send a test. */
export function DeviceNotifyCard() {
  const toast = useToast();
  const [state, setState] = usePushState();
  const [busy, setBusy] = useState(false);
  const [battery, setBattery] = useState(false);

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
  const on = state === "on";

  return (
    <section className="card set-card" aria-label="Notificações neste aparelho">
      <CardHead
        icon={on ? "bell" : "bellOff"}
        tone={on ? "kelp" : state === "denied" ? "coral" : undefined}
        title="Neste aparelho"
        sub={state ? stateText[state] : "Verificando…"}
        aside={
          state && (
            <span className={`pill${on ? " ok" : ""}`}>
              <span className={`dot ${on ? "ok" : state === "off" ? "" : "bad"}`} /> {on ? "Ligadas" : state === "off" ? "Desligadas" : "Indisponível"}
            </span>
          )
        }
      />
      {on && battery && (
        <p className="notice small">
          O Android pode pausar os avisos para economizar bateria.{" "}
          <button className="linkish" onClick={() => void allowBackground()?.then(() => setBattery(false)).catch(() => {})}>
            Permitir em segundo plano
          </button>
        </p>
      )}
      <div className="row-actions">
        {state === "denied" && isApp() && (
          <button className="btn" onClick={() => void run(enablePush)}>
            Abrir permissões do app
          </button>
        )}
        {state === "off" && (
          <button className="btn primary" disabled={busy} onClick={() => void run(enablePush)}>
            <Icon name="bell" /> Ativar neste aparelho
          </button>
        )}
        {on && (
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
            {isApp() && (
              <button className="btn ghost" onClick={() => void hideOngoing()?.catch(() => toast("Atualize o app para usar esta opção.", true))}>
                Ocultar aviso fixo
              </button>
            )}
          </>
        )}
      </div>
      <p className="muted small card-foot">
        {isApp()
          ? "O app mantém uma conexão direta e cifrada com o servidor da casa; nenhum serviço de terceiros vê os avisos. Tocar num aviso abre a gravação do momento."
          : "A imagem do evento vem direto do servidor; o serviço de avisos do navegador só vê texto cifrado."}
      </p>
    </section>
  );
}

/** Icon and group for each event type on the "what notifies me" list. */
const TYPE_META: Record<string, { icon: string; group: "alert" | "camera" | "other"; hint: string }> = {
  human: { icon: "user", group: "alert", hint: "Alguém aparece na imagem" },
  vehicle: { icon: "car", group: "alert", hint: "Carro, moto ou bicicleta" },
  animal: { icon: "paw", group: "alert", hint: "Cachorro, gato, pássaro" },
  line: { icon: "line", group: "alert", hint: "Alguém cruza a linha desenhada" },
  intrusion: { icon: "zone", group: "alert", hint: "Alguém entra na área marcada" },
  tamper: { icon: "camera", group: "camera", hint: "Câmera coberta, virada ou sem imagem" },
  motion: { icon: "motion", group: "other", hint: "Qualquer mudança na imagem; avisa muito" },
};
const GROUPS: { id: "alert" | "camera" | "other"; label: string }[] = [
  { id: "alert", label: "Detecção" },
  { id: "camera", label: "Câmera" },
  { id: "other", label: "Outros" },
];

/** Which event types notify this user, and whether the house mode applies. */
export function NotifyTypesCard() {
  const qc = useQueryClient();
  const toast = useToast();
  const q = useQuery({ queryKey: ["notify-prefs"], queryFn: getPrefs, staleTime: 60_000 });
  const save = useMutation({
    mutationFn: putPrefs,
    onSuccess: (d) => qc.setQueryData(["notify-prefs"], d),
    onError: (e: Error) => toast(e.message, true),
  });
  const d = q.data;
  const p = d?.prefs;
  const toggle = (t: string) => p && save.mutate({ ...p, types: p.types.includes(t) ? p.types.filter((x) => x !== t) : [...p.types, t] });
  const count = p?.types.filter((t) => d?.types.includes(t)).length ?? 0;
  return (
    <section className="card set-card" aria-label="O que me avisa">
      <CardHead
        icon="events"
        title="O que me avisa"
        sub="Só para você: cada pessoa escolhe os próprios avisos. Tudo continua gravado e aparece em Eventos."
        aside={
          d && (
            <span className="pill num">
              {count} de {d.types.length}
            </span>
          )
        }
      />
      {d && p && (
        <>
          {GROUPS.map((g) => {
            const types = d.types.filter((t) => (TYPE_META[t]?.group ?? "other") === g.id);
            if (!types.length) return null;
            return (
              <div key={g.id} className="set-group">
                <h3 className="set-group-title">{g.label}</h3>
                <div className="toggle-list">
                  {types.map((t) => {
                    const on = p.types.includes(t);
                    return (
                      <div key={t} className={`toggle-item${on ? " on" : ""}`}>
                        <span className="toggle-ico" aria-hidden="true">
                          <Icon name={TYPE_META[t]?.icon ?? "events"} />
                        </span>
                        <span className="toggle-text">
                          <strong>{TYPE_LABEL[t] ?? t}</strong>
                          {TYPE_META[t] && <span className="muted small">{TYPE_META[t].hint}</span>}
                        </span>
                        <button className="switch" role="switch" aria-checked={on} aria-label={TYPE_LABEL[t] ?? t} disabled={save.isPending} onClick={() => toggle(t)} />
                      </div>
                    );
                  })}
                </div>
              </div>
            );
          })}
          <div className="set-group">
            <h3 className="set-group-title">Modo da casa</h3>
            <div className="toggle-list">
              <div className={`toggle-item${p.follow_mode ? " on" : ""}`}>
                <span className="toggle-ico" aria-hidden="true">
                  <Icon name="home" />
                </span>
                <span className="toggle-text">
                  <strong>Respeitar o modo da casa</strong>
                  <span className="muted small">
                    {p.follow_mode ? "No modo Casa, por exemplo, só câmera mexida avisa." : "Avisa os tipos marcados em qualquer modo."}
                  </span>
                </span>
                <button
                  className="switch"
                  role="switch"
                  aria-checked={p.follow_mode}
                  aria-label="Respeitar o modo da casa"
                  disabled={save.isPending}
                  onClick={() => save.mutate({ ...p, follow_mode: !p.follow_mode })}
                />
              </div>
            </div>
          </div>
        </>
      )}
    </section>
  );
}

/** Casa / Noite / Fora for the house, and the automatic night. */
export function HouseModeCard() {
  const { can } = useSession();
  const qc = useQueryClient();
  const toast = useToast();
  const mode = useQuery({ queryKey: ["notify-mode"], queryFn: getMode, staleTime: 30_000 });
  const saveNight = useMutation({
    mutationFn: putMode,
    onSuccess: (d) => qc.setQueryData(["notify-mode"], d),
    onError: (e: Error) => toast(e.message, true),
  });
  const s = mode.data?.settings;
  const effective = mode.data?.effective;
  return (
    <section className="card set-card" aria-label="Modo da casa">
      <CardHead
        icon={effective === "night" ? "moon" : effective === "away" ? "away" : "home"}
        tone="sea"
        title="Modo da casa"
        sub="Vale para todos da casa. Muda o que manda aviso, não o que é gravado."
        aside={effective && <span className="pill ok">Agora: {MODES.find((m) => m.value === effective)?.label}</span>}
      />
      <ModeSwitch />
      {s && (
        <div className="toggle-list">
          <div className={`toggle-item${s.night_auto ? " on" : ""}`}>
            <span className="toggle-ico" aria-hidden="true">
              <Icon name="moon" />
            </span>
            <span className="toggle-text">
              <strong>Noite automática</strong>
              <span className="muted small">No modo Casa, liga a Noite no horário abaixo.</span>
            </span>
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
      {!can("operator") && <p className="muted small card-foot">Seu perfil só permite ver o modo.</p>}
    </section>
  );
}
