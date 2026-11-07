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

import { t } from "../lib/i18n";
export const MODES: { value: ModeSettings["mode"]; label: string; hint: string }[] = [
  { value: "home", label: t("Home"), hint: t("For the enabled types, only alerts if the camera is moved.") },
  { value: "night", label: t("Night"), hint: t("For the enabled types, allows person, line, area and camera alerts.") },
  { value: "away", label: t("Away"), hint: t("Allows all enabled types, including motion.") },
];

const MODE_CARDS: { value: ModeSettings["mode"]; label: string; icon: string; when: string; notifies: string[]; quiet: string }[] = [
  {
    value: "home",
    label: t("Home"),
    icon: "home",
    when: t("Someone is home, during the day."),
    notifies: [t("Camera obstructed or moved")],
    quiet: t("People, cars and motion are only recorded."),
  },
  {
    value: "night",
    label: t("Night"),
    icon: "moon",
    when: t("Everyone is asleep."),
    notifies: [t("Person"), t("Line crossed or area entered"), t("Camera obstructed")],
    quiet: t("Vehicles, animals and motion are only recorded."),
  },
  {
    value: "away",
    label: t("Away"),
    icon: "away",
    when: t("Home empty."),
    notifies: [t("Person, vehicle and animal"), t("Line, area and tampering"), t("Motion (if the detector is stopped)")],
    quiet: t("Only the types enabled in the account send notices."),
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
      toast(t("{label} mode on", { label: MODES.find((m) => m.value === d.settings.mode)?.label }));
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
      <section className="card mode-card compact" aria-label={t("Home notice mode")}>
        <div className="mode-card-head">
          <h2>{t("Home mode")}</h2>
          <button className="linkish small" onClick={() => setDetails(true)}>
            {t("What each mode alerts for")}
          </button>
        </div>
        <ModeSwitch compact />
        {cur && (
          <p className="muted small">
            {autoNight ? t("Automatic night until {night_to}. ", { night_to: s.night_to }) : ""}{t("Can alert:")}{" "}{cur.notifies.join(", ").toLowerCase()}.
          </p>
        )}
      </section>
    );
  }
  return (
    <section className="card mode-card" aria-label={t("Home notice mode")}>
      <div className="mode-card-head">
        <div>
          <h2>{t("Home mode")}</h2>
          <p className="muted small">
            {t("Filters the notice types each person turned on in their account. Everything stays recorded and shows up here in Events.")}
          </p>
        </div>
        <span className="pill ok">
          <span className="dot ok" />{" "}{t("Now:")}{" "}{MODES.find((m) => m.value === effective)?.label}
          {autoNight && t(" (automatic until {night_to})", { night_to: s.night_to })}
        </span>
      </div>
      <div className="mode-options" role="radiogroup" aria-label={t("Mode")}>
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
                {on && <span className="mode-on">{t("Active")}</span>}
                {!on && effective === m.value && <span className="mode-on soft">{t("Automatic")}</span>}
              </span>
              <span className="muted small">{m.when}</span>
              <span className="mode-list-title small">{t("Can alert:")}</span>
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
          ? t("In Home mode, Night turns on by itself from {night_from} to {night_to}.", { night_from: s.night_from, night_to: s.night_to })
          : t("Automatic night is off.")}{" "}
        {t("Adjust in My account, under Notifications.")}
        {!can("operator") && t(" Your profile only lets you see the mode.")}
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
      toast(t("{label} mode", { label: MODES.find((m) => m.value === d.settings.mode)?.label }));
    },
    onError: (e: Error) => toast(e.message, true),
  });
  const s = q.data?.settings;
  if (!s) return null;
  const autoNight = s.mode === "home" && q.data?.effective === "night";
  return (
    <div className={`mode-switch${compact ? " compact" : ""}`}>
      <div className="segmented" role="group" aria-label={t("Notice mode")}>
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
      {autoNight && <span className="pill small-pill">{t("Automatic night until")}{" "}{s.night_to}</span>}
    </div>
  );
}

const stateText: Record<PushState, string> = {
  unsupported: t("This browser does not receive notifications. On iPhone, add Sentinela to the Home Screen first."),
  insecure: t("Browser notifications only work over the HTTPS address."),
  denied: t("Notifications were blocked in this device's settings."),
  off: t("Off on this device. Turn on to receive notices here."),
  on: t("This device receives notices, even with Sentinela closed."),
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
  const [transport, setTransport] = useState<"firebase" | "direct" | "off">("off");

  useEffect(() => {
    if (state === "on") void nativeStatus()?.then((st) => {
      setBattery(st.batteryOptimized);
      setTransport(st.transport ?? (st.running ? "direct" : "off"));
    }).catch(() => {});
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
    <section className="card set-card" aria-label={t("Notifications on this device")}>
      <CardHead
        icon={on ? "bell" : "bellOff"}
        tone={on ? "kelp" : state === "denied" ? "coral" : undefined}
        title={t("On this device")}
        sub={state === "on" && transport === "firebase"
          ? t("Android receives notices without keeping the app open. The details come straight from your home.")
          : state ? stateText[state] : t("Checking…")}
        aside={
          state && (
            <span className={`pill${on ? " ok" : ""}`}>
              <span className={`dot ${on ? "ok" : state === "off" ? "" : "bad"}`} /> {on ? t("On") : state === "off" ? t("Off") : t("Unavailable")}
            </span>
          )
        }
      />
      {on && battery && (
        <p className="notice small">
          {t("Android may pause notices to save battery.")}{" "}
          <button className="linkish" onClick={() => void allowBackground()?.then(() => setBattery(false)).catch(() => {})}>
            {t("Allow in the background")}
          </button>
        </p>
      )}
      <div className="row-actions">
        {state === "denied" && isApp() && (
          <button className="btn" onClick={() => void run(enablePush)}>
            {t("Open app permissions")}
          </button>
        )}
        {state === "off" && (
          <button className="btn primary" disabled={busy} onClick={() => void run(enablePush)}>
            <Icon name="bell" />{" "}{t("Enable on this device")}
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
                  .then((r) => toast(r.sent ? t("Test sent.") : t("No device received it. Turn it on again."), !r.sent))
                  .catch((e: Error) => toast(e.message, true))
                  .finally(() => setBusy(false));
              }}
            >
              {t("Send test")}
            </button>
            <button className="btn ghost" disabled={busy} onClick={() => void run(disablePush)}>
              {t("Disable")}
            </button>
            {isApp() && transport === "direct" && (
              <button className="btn ghost" onClick={() => void hideOngoing()?.catch(() => toast(t("Update the app to use this option."), true))}>
                {t("Hide pinned notice")}
              </button>
            )}
          </>
        )}
      </div>
      <p className="muted small card-foot">
        {isApp()
          ? transport === "firebase"
            ? t("Firebase only delivers a new-event signal. The title, picture and recording come straight from the home server.")
            : t("The app keeps an encrypted connection to the home server. Tapping a notice opens that moment's recording.")
          : t("The event image comes straight from the server; the browser's notification service only sees encrypted text.")}
      </p>
    </section>
  );
}

/** Icon and group for each notice choice. */
const TYPE_META: Record<string, { icon: string; group: "alert" | "camera" | "other"; hint: string }> = {
  human: { icon: "user", group: "alert", hint: t("Someone appears in the picture") },
  vehicle: { icon: "car", group: "alert", hint: t("Car, motorcycle or bicycle") },
  animal: { icon: "paw", group: "alert", hint: t("Dog, cat, bird") },
  line: { icon: "line", group: "alert", hint: t("Someone crosses the drawn line") },
  intrusion: { icon: "zone", group: "alert", hint: t("Someone enters the marked area") },
  tamper: { icon: "camera", group: "camera", hint: t("Camera covered, turned or without picture") },
  motion: { icon: "motion", group: "other", hint: t("Any change in the picture; alerts a lot") },
  login: { icon: "lock", group: "other", hint: t("Entry through public access; independent of the home mode") },
};
const GROUPS: { id: "alert" | "camera" | "other"; label: string }[] = [
  { id: "alert", label: t("Detection") },
  { id: "camera", label: t("Camera") },
  { id: "other", label: t("Other") },
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
    <section className="card set-card" aria-label={t("What alerts me")}>
      <CardHead
        icon="events"
        title={t("What alerts me")}
        sub={t("No notices are on. Tick the types you want to receive; everything is still recorded and shows up in Events.")}
        aside={
          d && (
            <span className="pill num">
              {t("{count} of {total}", { count, total: d.types.length })}
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
            <h3 className="set-group-title">{t("Home mode")}</h3>
            <div className="toggle-list">
              <div className={`toggle-item${p.follow_mode ? " on" : ""}`}>
                <span className="toggle-ico" aria-hidden="true">
                  <Icon name="home" />
                </span>
                <span className="toggle-text">
                  <strong>{t("Respect the home mode")}</strong>
                  <span className="muted small">
                    {p.follow_mode ? t("In Home mode, for example, only a moved camera alerts.") : t("Alerts for the ticked types in any mode.")}
                  </span>
                </span>
                <button
                  className="switch"
                  role="switch"
                  aria-checked={p.follow_mode}
                  aria-label={t("Respect the home mode")}
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
    <section className="card set-card" aria-label={t("Home mode")}>
      <CardHead
        icon={effective === "night" ? "moon" : effective === "away" ? "away" : "home"}
        tone="sea"
        title={t("Home mode")}
        sub={t("Applies to everyone in the home. Filters the notices each person turned on; does not change what is recorded.")}
        aside={effective && <span className="pill ok">{t("Now:")}{" "}{MODES.find((m) => m.value === effective)?.label}</span>}
      />
      <ModeSwitch />
      {s && (
        <div className="toggle-list">
          <div className={`toggle-item${s.night_auto ? " on" : ""}`}>
            <span className="toggle-ico" aria-hidden="true">
              <Icon name="moon" />
            </span>
            <span className="toggle-text">
              <strong>{t("Automatic night")}</strong>
              <span className="muted small">{t("In Home mode, turns Night on at the time below.")}</span>
            </span>
            <button
              className="switch"
              role="switch"
              aria-checked={s.night_auto}
              aria-label={t("Automatic night")}
              disabled={!can("operator")}
              onClick={() => saveNight.mutate({ ...s, night_auto: !s.night_auto })}
            />
          </div>
          <div className="night-range">
            <span className="muted">{t("From the")}</span>
            <input
              className="input"
              type="time"
              aria-label={t("Start of night")}
              value={s.night_from}
              disabled={!can("operator") || !s.night_auto}
              onChange={(e) => e.target.value && saveNight.mutate({ ...s, night_from: e.target.value })}
            />
            <span className="muted">{t("at")}</span>
            <input
              className="input"
              type="time"
              aria-label={t("End of night")}
              value={s.night_to}
              disabled={!can("operator") || !s.night_auto}
              onChange={(e) => e.target.value && saveNight.mutate({ ...s, night_to: e.target.value })}
            />
          </div>
        </div>
      )}
      {!can("operator") && <p className="muted small card-foot">{t("Your profile only lets you see the mode.")}</p>}
    </section>
  );
}
