import { useState, type ReactNode } from "react";
import { t } from "../lib/i18n";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, get } from "../lib/api";
import { useToast } from "../lib/session";
import { AddCameraCard } from "./AddCamera";
import { CamerasEditor } from "./SettingsEditor";
import { DeviceNotifyCard } from "./Notifications";
import { SecurityCard } from "./Security";
import { AppPairingCard } from "./AppPairing";
import { TailscaleSetup } from "./TailscaleSetup";
import { Icon } from "./Icon";
import { LighthouseMark } from "./Lighthouse";

/** GET /api/onboarding: what the server already has. */
export interface Onboarding {
  pending: boolean;
  cameras: { id: string; name: string; vendor: string; control: boolean; recording: boolean }[];
  timezone: string;
  max_disk_percent: number;
  retention_days: number;
  volume_marker: boolean;
  cloud: boolean;
  nas_host: string;
  push: boolean;
  hosting: boolean;
  totp: boolean;
  zone: string;
  tailnet_url: string;
  proxy_url?: string;
  public_url: string;
  public_pinned: boolean;
}

const STEPS = [
  { id: "welcome", label: t("Welcome") },
  { id: "cameras", label: t("Cameras") },
  { id: "recording", label: t("Recording") },
  { id: "cloud", label: t("Cloud") },
  { id: "services", label: t("Services") },
  { id: "remote", label: t("Remote access") },
  { id: "app", label: "App" },
  { id: "notify", label: t("Notifications") },
  { id: "security", label: t("Security") },
  { id: "done", label: t("Done") },
] as const;

const vendorLabel: Record<string, string> = {
  xmeye: "XMEye",
  hikvision: "Hikvision",
  dahua: "Dahua",
  generic: "ONVIF / RTSP",
};

/** First-run guide for the admin of a new install. Every step can be
 *  skipped; settings that live in the server's files are shown with the
 *  file or command that changes them. */
export function SetupGuide({ onClose }: { onClose: () => void }) {
  const toast = useToast();
  const qc = useQueryClient();
  const [step, setStep] = useState(0);
  const info = useQuery({ queryKey: ["onboarding"], queryFn: () => get<Onboarding>("/api/onboarding") });
  const finish = useMutation({
    mutationFn: () => api("PUT", "/api/onboarding", { pending: false }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["me"] });
      onClose();
    },
    onError: (e: Error) => toast(t("Could not finish: {message}", { message: e.message }), true),
  });
  const o = info.data;
  const last = STEPS.length - 1;
  const next = () => setStep((s) => Math.min(s + 1, last));
  const back = () => setStep((s) => Math.max(s - 1, 0));
  const id = STEPS[step].id;

  return (
    <div className="setup-guide">
      <header className="setup-head">
        <LighthouseMark />
        <div>
          <h1>{t("Set up Sentinela")}</h1>
          <p className="muted">{t("A few minutes to get the home ready. Skip what you like and come back later in System.")}</p>
        </div>
        <button className="btn ghost small" disabled={finish.isPending} onClick={() => finish.mutate()}>
          {t("Skip setup")}
        </button>
      </header>

      <ol className="stepper" aria-label={t("Steps")}>
        {STEPS.map((s, i) => (
          <li key={s.id} className={i === step ? "on" : i < step ? "done" : ""}>
            <button type="button" onClick={() => setStep(i)}>
              <span className="step-n">{i < step ? <Icon name="check" /> : i + 1}</span>
              {t(s.label)}
            </button>
          </li>
        ))}
      </ol>

      {info.isError && <p className="notice error">{t("Could not read the server.")}{" "}{(info.error as Error).message}</p>}
      {o && (
        <div className="setup-body">
          {id === "welcome" && <Welcome o={o} />}
          {id === "cameras" && <Cameras o={o} />}
          {id === "recording" && <Recording o={o} />}
          {id === "cloud" && <CloudStep o={o} />}
          {id === "services" && <ServicesStep o={o} />}
          {id === "remote" && <Remote o={o} />}
          {id === "app" && <AppStep />}
          {id === "notify" && <Notify o={o} />}
          {id === "security" && <Security o={o} />}
          {id === "done" && <Done o={o} />}
        </div>
      )}

      <footer className="setup-foot">
        {step > 0 && (
          <button className="btn ghost" onClick={back}>
            {t("Back")}
          </button>
        )}
        <span className="spacer" />
        {step < last ? (
          <>
            {step > 0 && (
              <button className="btn ghost" onClick={next}>
                {t("Skip this step")}
              </button>
            )}
            <button className="btn primary" onClick={next}>
              {t(step === 0 ? "Get started" : "Continue")}
            </button>
          </>
        ) : (
          <button className="btn primary" disabled={finish.isPending} onClick={() => finish.mutate()}>
            {t("Go to Live")}
          </button>
        )}
      </footer>
    </div>
  );
}

function Step({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="card setup-step">
      <h2>{title}</h2>
      {children}
    </section>
  );
}

function Status({ ok, children }: { ok: boolean; children: ReactNode }) {
  return (
    <p className={`setup-status${ok ? " ok" : ""}`}>
      <Icon name={ok ? "check" : "info"} /> {children}
    </p>
  );
}

function Welcome({ o }: { o: Onboarding }) {
  return (
    <Step title={t("Welcome")}>
      <p>{t("The administrator account is created. Now let's check:")}</p>
      <ul className="setup-list">
        <li>{t("the cameras and how to add others;")}</li>
        <li>{t("where and for how long recordings are kept;")}</li>
        <li>{t("Cloud, the home's file storage;")}</li>
        <li>{t("how to access from outside the home (optional) and connect the app;")}</li>
        <li>{t("the notifications on this device;")}</li>
        <li>{t("two-step verification (optional).")}</li>
      </ul>
      <p className="muted small">
        {o.cameras.length === 0
          ? t("No camera configured yet.")
          : t("{length} camera{v1} configured{v2} by the installer.", { length: o.cameras.length, v1: o.cameras.length > 1 ? "s" : "", v2: o.cameras.length > 1 ? "s" : "" })}
      </p>
    </Step>
  );
}

function Cameras({ o }: { o: Onboarding }) {
  return (
    <div className="stack">
      <Step title={t("Cameras")}>
        {o.cameras.length === 0 ? (
          <Status ok={false}>{t("No cameras yet. Add the first one below.")}</Status>
        ) : (
          <ul className="setup-cams">
            {o.cameras.map((c) => (
              <li key={c.id}>
                <Icon name="live" />
                <strong>{c.name}</strong>
                <span className="muted small">
                  {vendorLabel[c.vendor] ?? c.vendor} · {c.recording ? t("recording") : t("no recording")}
                  {c.control ? t(" · PTZ and light") : ""}
                </span>
              </li>
            ))}
          </ul>
        )}
        <p className="muted small">
          {t("Add cameras of any brand below: with ONVIF (most IP cameras) Sentinela finds the video addresses by itself; Hikvision, Dahua and XMEye also have ready-made models. Passwords stay only on the server.")}
        </p>
      </Step>
      <CamerasEditor />
      <AddCameraCard />
    </div>
  );
}

function Recording({ o }: { o: Onboarding }) {
  return (
    <Step title={t("Recording")}>
      <Status ok={o.volume_marker}>
        {o.volume_marker
          ? t("Recordings only go to the recordings disk: if it does not mount, recording pauses instead of filling the system disk.")
          : t("The recordings disk check is off (volume_marker: none).")}
      </Status>
      <dl className="setup-facts">
        <dt>{t("For how long")}</dt>
        <dd>
          {o.retention_days > 0
            ? t("{retention_days} days", { retention_days: o.retention_days })
            : t("until the disk reaches {max_disk_percent}% (then the oldest are replaced)", { max_disk_percent: o.max_disk_percent })}
        </dd>
        <dt>{t("Time zone")}</dt>
        <dd>{o.timezone}</dd>
      </dl>
      <p className="muted small">
        {t("To change it, edit")}{" "}<code>/etc/sentinela/config.yaml</code>{" "}{t("on the server (")}<code>recording</code> e{" "}
        <code>timezone</code>{t(") and restart with")}{" "}<code>./scripts/deploy.sh</code>.
      </p>
    </Step>
  );
}

function CloudStep({ o }: { o: Onboarding }) {
  return (
    <Step title={t("Cloud")}>
      {o.cloud ? (
        <>
          <Status ok>{t("Cloud is on: files from the browser, the app and the home network.")}</Status>
          {o.nas_host && (
            <p>
              {t("On the computer, open")}{" "}<code>\\{o.nas_host}\Nuvem</code>{t(". The network folder password is set on the server with")}{" "}
              <code>sudo smbpasswd -a {t("your-user")}</code>.
            </p>
          )}
        </>
      ) : (
        <>
          <Status ok={false}>{t("Cloud is off: no disk at /srv/nas.")}</Status>
          <p className="muted small">
            {t("It is optional. To turn it on, mount a disk at")}{" "}<code>/srv/nas</code>{" "}{t("and run")}{" "}
            <code>sudo /opt/sentinela/scripts/setup-nas.sh</code>{t(". If you skip it, the Cloud tab stays hidden until a disk is set up.")}
          </p>
        </>
      )}
    </Step>
  );
}

function ServicesStep({ o }: { o: Onboarding }) {
  return (
    <Step title={t("Services")}>
      <p className="muted small">
        {t("Optional: run your own small projects (a bot, a website, an API) on this server, from GitHub, a folder or a ZIP. Each one runs capped, on the system disk, and never before the cameras after a reboot.")}
      </p>
      {o.hosting ? (
        <Status ok>{t("Services are on: the Services tab is in the menu (on the computer).")}</Status>
      ) : (
        <>
          <Status ok={false}>{t("Services are off.")}</Status>
          <p className="muted small">
            {t("To turn them on, run on the server")}{" "}<code>sudo /opt/sentinela/scripts/setup-apps.sh enable</code>
            {t(". If you skip it, the Services tab stays hidden.")}
          </p>
        </>
      )}
    </Step>
  );
}

function Remote({ o }: { o: Onboarding }) {
  const none = !o.tailnet_url && !o.public_url && !o.proxy_url;
  return (
    <Step title={t("Access from outside the home")}>
      <p className="muted small">
        {t("By default Sentinela only answers on the home network. To use the app or the browser outside the home, choose a route. None is required; you can do it later.")}
      </p>
      <div className="remote-choices">
        <div className={`remote-choice${none ? " on" : ""}`}>
          <strong>{t("Home only")}</strong>
          <span className="muted small">{t("Nothing exposed. The app only works if the phone is on a VPN to home.")}</span>
        </div>
        <div className={`remote-choice wide${o.tailnet_url ? " on" : ""}`}>
          <strong>{t("Tailscale (recommended)")}</strong>
          <span className="muted small">{t("Private network between your devices, with HTTPS and without opening ports. Set it up here in a few steps, or later in System → Server and disks.")}</span>
          <TailscaleSetup compact />
        </div>
        <div className={`remote-choice${o.proxy_url ? " on" : ""}`}>
          <strong>{t("Domain with Cloudflare Tunnel or a reverse proxy")}</strong>
          <span className="muted small">
            {t("Your own domain, from any phone, without opening ports (works behind CGNAT). Set it up in System → Remote access. Only the sign-in screen is visible from outside; turn on two-step verification.")}
          </span>
          {o.proxy_url && <code>{o.proxy_url}</code>}
        </div>
        <div className={`remote-choice${o.public_url ? " on" : ""}`}>
          <strong>{t("Router port")}</strong>
          <span className="muted small">
            {t("Public address with HTTPS, from any phone: a domain or the home's fixed IP, and a port forwarded on the router. Set it up in System → Remote access. Only the sign-in screen is visible from outside; turn on two-step verification.")}
          </span>
          {o.public_url && (
            <code>
              {o.public_url}
              {o.public_pinned ? t(" (own certificate)") : ""}
            </code>
          )}
        </div>
      </div>
      <p className="muted small">
        {t("Never forward the cameras', RTSP or network folder ports: only Sentinela.")}
      </p>
    </Step>
  );
}

function AppStep() {
  return (
    <div className="stack">
      <Step title={t("Android app")}>
        <p className="muted small">
          {t("The same app works for any home: it connects to the address you choose here. It is also in System → App, for each person in the home.")}
        </p>
      </Step>
      <AppPairingCard />
    </div>
  );
}

function Notify({ o }: { o: Onboarding }) {
  if (!o.push)
    return (
      <Step title={t("Notifications")}>
        <Status ok={false}>{t("Browser notifications are not available on this server.")}</Status>
      </Step>
    );
  return (
    <div className="stack">
      <Step title={t("Notifications")}>
        <p className="muted small">
          {t("Receive motion and person notices on this device. Each person chooses theirs in My account.")}
        </p>
      </Step>
      <DeviceNotifyCard />
    </div>
  );
}

function Security({ o }: { o: Onboarding }) {
  return (
    <div className="stack">
      <Step title={t("Security")}>
        <p className="muted small">
          {t("Two-step verification is optional and recommended if you open access over the internet. It uses an app such as Google Authenticator or Aegis.")}
        </p>
        {o.zone !== "lan" && o.zone !== "tailnet" && (
          <Status ok={false}>{t("Enable it at home or over Tailscale: it cannot be configured from outside.")}</Status>
        )}
      </Step>
      <SecurityCard />
    </div>
  );
}

function Done({ o }: { o: Onboarding }) {
  return (
    <Step title={t("All ready")}>
      <p>{t("Sentinela is set up. Next steps, whenever you like:")}</p>
      <ul className="setup-list">
        <li>{t("Invite the family in System → Users (each one chooses their own password).")}</li>
        <li>{t("Draw detection zones on each camera, in System → Detection.")}</li>
        <li>{t("Install the Android app to see the cameras and receive notices.")}</li>
        {o.hosting && <li>{t("Publish your projects in Services.")}</li>}
      </ul>
      <p className="muted small">{t("This assistant can be opened again in System → Overview.")}</p>
    </Step>
  );
}

/** Button that reopens the guide (System page, admins). */
export function ReopenSetupGuide() {
  const qc = useQueryClient();
  const toast = useToast();
  const reopen = useMutation({
    mutationFn: () => api("PUT", "/api/onboarding", { pending: true }),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["me"] }),
    onError: (e: Error) => toast(e.message, true),
  });
  return (
    <button className="btn ghost small" disabled={reopen.isPending} onClick={() => reopen.mutate()}>
      <Icon name="system" />{" "}{t("Setup guide")}
    </button>
  );
}
