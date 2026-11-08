import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, get } from "../lib/api";
import { useToast } from "../lib/session";
import { isPublicZone } from "../lib/access";
import type { SettingsDoc } from "./SettingsEditor";
import { Icon } from "./Icon";
import { t } from "../lib/i18n";

/** What the host's Tailscale agent last saw (GET /api/tailscale). */
interface TailscaleStatus {
  checked: string;
  installed: boolean;
  version: string;
  state: string;
  dns_name: string;
  ip: string;
  tags: string[];
  magic_dns: boolean;
  https: boolean;
  serve: boolean;
  busy: boolean;
  action: string;
  message: string;
  enable_url: string;
}
interface TailscaleInfo {
  host: boolean;
  status: TailscaleStatus | null;
  pending: boolean;
  configured: string;
  suggested: string;
  check: "ok" | "mismatch" | "unknown";
}

const ADMIN = "https://login.tailscale.com/admin";
const KEY_RE = /^tskey-auth-[A-Za-z0-9_-]{10,200}$/;

const messageText: Record<string, string> = {
  bad_key: t("That auth key was not accepted: copy it again from the Keys page (it starts with tskey-auth-)."),
  install_failed: t("Could not install Tailscale on the server. Check its internet connection and try again."),
  key_refused: t("Tailscale refused the key: it may have expired or been used already (create a new one, not ephemeral)."),
  needs_approval: t("The server joined, but your tailnet requires approving new devices: approve it in Machines, then press Check again."),
  https_disabled: t("Connected, but HTTPS certificates are off in your tailnet. Turn them on (DNS page), then press Publish again."),
  not_installed: t("Tailscale is not installed on the server yet: connect with an auth key first."),
  disconnected: t("The server left the tailnet."),
  "installing Tailscale": t("Installing Tailscale on the server…"),
  "joining the tailnet": t("Joining your tailnet…"),
};

/** Copies the settings, changes the Tailscale address and saves them through
 *  the normal settings flow (validated by the server, applied by the host). */
async function saveTailnetURL(url: string) {
  const info = await get<{ settings: SettingsDoc }>("/api/settings");
  await api("PUT", "/api/settings", { ...info.settings, tailnet_url: url });
}

function Check({ ok, children }: { ok: boolean | null; children: React.ReactNode }) {
  return (
    <li className={`ts-check ${ok === null ? "" : ok ? "ok" : "no"}`}>
      <Icon name={ok ? "check" : ok === false ? "x" : "info"} />
      <span>{children}</span>
    </li>
  );
}

/** Tailscale step by step: what to create in Tailscale, the key to paste,
 *  and live checks of what the server reports. Used in the welcome guide and
 *  in System -> Server. */
export function TailscaleSetup({ compact = false }: { compact?: boolean }) {
  const toast = useToast();
  const qc = useQueryClient();
  const [watch, setWatch] = useState(false);
  useEffect(() => {
    if (!watch) return;
    const id = window.setTimeout(() => setWatch(false), 180_000);
    return () => window.clearTimeout(id);
  }, [watch]);
  const q = useQuery({
    queryKey: ["tailscale"],
    queryFn: () => get<TailscaleInfo>("/api/tailscale"),
    refetchInterval: (x) => (watch || x.state.data?.pending || x.state.data?.status?.busy ? 1500 : 30_000),
  });
  const [key, setKey] = useState("");
  const ask = useMutation({
    mutationFn: (body: { action: string; auth_key?: string }) => api("POST", "/api/tailscale", body),
    onSuccess: (_, body) => {
      setWatch(true);
      if (body.action === "connect") setKey("");
      void qc.invalidateQueries({ queryKey: ["tailscale"] });
    },
    onError: (e: Error) => toast(e.message, true),
  });
  const use = useMutation({
    mutationFn: saveTailnetURL,
    onSuccess: () => {
      toast(t("Saved. Applying on the server; the panel comes back in a few seconds."));
      void qc.invalidateQueries({ queryKey: ["settings"] });
      window.setTimeout(() => void qc.invalidateQueries({ queryKey: ["tailscale"] }), 8000);
    },
    onError: (e: Error) => toast(e.message, true),
  });

  const info = q.data;
  const st = info?.status ?? null;
  const outside = isPublicZone();
  const running = st?.state === "Running";
  const working = !!info?.pending || !!st?.busy;
  const keyOk = KEY_RE.test(key.trim());

  if (q.isLoading) return <p className="muted">{t("Loading…")}</p>;
  if (!info) return <p className="notice error">{t("Could not read the Tailscale status.")}</p>;

  return (
    <div className="ts-setup">
      {!compact && (
        <p className="muted small">
          {t("Tailscale is a private network between your devices: the app and the browser reach the home from anywhere, with HTTPS, and nothing is opened on the router. It is free for personal use.")}
        </p>
      )}

      {info.host && (
        <ul className="ts-checks">
          <Check ok={st ? st.installed : null}>{t("Tailscale installed on the server")}{st?.version ? ` (${st.version})` : ""}</Check>
          <Check ok={st ? running : null}>
            {running ? (
              <>
                {t("Connected as")}{" "}<code>{st?.dns_name}</code>
                {st?.ip ? <span className="muted small">{" "}· {st.ip}</span> : null}
              </>
            ) : (
              t("Connected to your tailnet")
            )}
          </Check>
          <Check ok={st && running ? st.https : null}>{t("HTTPS certificates on (Tailscale DNS page)")}</Check>
          <Check ok={st && running ? st.serve : null}>{t("Sentinela published over HTTPS on the tailnet")}</Check>
          <Check ok={info.check === "ok" ? true : info.configured ? info.check === "mismatch" ? false : null : false}>
            {info.check === "ok"
              ? t("The app and links use this address")
              : info.check === "mismatch"
                ? t("The saved Tailscale address ({url}) is not this server's", { url: info.configured })
                : t("Tailscale address saved in the settings")}
          </Check>
        </ul>
      )}

      {working && (
        <p className="setup-status" role="status">
          <Icon name="refresh" />{" "}
          {st?.busy && messageText[st.message] ? messageText[st.message] : t("The server is working on it…")}
        </p>
      )}
      {!working && st?.message && st.message !== "ok" && messageText[st.message] && (
        <p className={`notice${st.message === "disconnected" ? "" : " error"}`}>
          {messageText[st.message]}
          {st.message === "https_disabled" && (
            <>
              {" "}
              <a href={st.enable_url || `${ADMIN}/dns`} target="_blank" rel="noreferrer noopener">
                {t("Turn on HTTPS")}
              </a>
            </>
          )}
          {st.message === "needs_approval" && (
            <>
              {" "}
              <a href={`${ADMIN}/machines`} target="_blank" rel="noreferrer noopener">
                {t("Open Machines")}
              </a>
            </>
          )}
        </p>
      )}

      {!info.host ? (
        <div className="notice">
          {t("This server cannot set up Tailscale from the panel yet. Run once on the server")}{" "}
          <code>sudo /opt/sentinela/scripts/setup-tailscale.sh units</code>{" "}{t("(or run a deploy). Or do everything by hand:")}{" "}
          <code>sudo /opt/sentinela/scripts/setup-tailscale.sh</code>
        </div>
      ) : running && st?.serve ? (
        <div className="stack ts-done">
          {info.check !== "ok" && info.suggested && (
            <div className="row-gap">
              <button className="btn primary small" disabled={use.isPending || outside} onClick={() => use.mutate(info.suggested)}>
                <Icon name="check" />{" "}{t("Use {url}", { url: info.suggested })}
              </button>
            </div>
          )}
          <p className="muted small">
            {t("On each phone or computer: install Tailscale, sign in with the same account and open")}{" "}
            <a href={info.suggested} target="_blank" rel="noreferrer noopener">
              {info.suggested}
            </a>
            {t(". In the Android app, System → App shows the Tailscale QR code.")}{" "}
            <a href="https://tailscale.com/download" target="_blank" rel="noreferrer noopener">
              {t("Download Tailscale")}
            </a>
          </p>
        </div>
      ) : (
        <ol className="ts-steps">
          <li>
            <strong>{t("Create a Tailscale account")}</strong>
            <span className="muted small">
              {t("Sign in with Google, Microsoft, GitHub or Apple; that creates your tailnet.")}{" "}
              <a href="https://login.tailscale.com/start" target="_blank" rel="noreferrer noopener">
                login.tailscale.com/start
              </a>
            </span>
          </li>
          <li>
            <strong>{t("Turn on MagicDNS and HTTPS certificates")}</strong>
            <span className="muted small">
              {t("Admin console → DNS: MagicDNS on, and under HTTPS Certificates press Enable. That gives the server a name like sentinela.your-tailnet.ts.net with a valid certificate.")}{" "}
              <a href={`${ADMIN}/dns`} target="_blank" rel="noreferrer noopener">
                {t("Open the DNS page")}
              </a>
            </span>
          </li>
          <li>
            <strong>{t("Create an auth key for the server")}</strong>
            <span className="muted small">
              {t("Admin console → Settings → Keys → Generate auth key. Leave Reusable and Ephemeral off, turn Pre-approved on if your tailnet approves devices, and copy the key (it starts with tskey-auth- and is shown only once).")}{" "}
              <a href={`${ADMIN}/settings/keys`} target="_blank" rel="noreferrer noopener">
                {t("Open Keys")}
              </a>
            </span>
          </li>
          <li>
            <strong>{t("Paste it here and connect")}</strong>
            <span className="muted small">
              {t("The server installs Tailscale if needed, joins as sentinela and publishes this panel over HTTPS. The key is used once and not kept.")}
            </span>
            <form
              className="ts-key"
              onSubmit={(e) => {
                e.preventDefault();
                if (keyOk) ask.mutate({ action: "connect", auth_key: key.trim() });
              }}
            >
              <input
                className="input"
                type="password"
                autoComplete="off"
                spellCheck={false}
                placeholder="tskey-auth-…"
                aria-label={t("Tailscale auth key")}
                value={key}
                onChange={(e) => setKey(e.target.value)}
                disabled={outside || working}
              />
              <button className="btn primary" type="submit" disabled={!keyOk || ask.isPending || outside || working}>
                {t("Connect")}
              </button>
            </form>
            {key.trim() !== "" && !keyOk && <span className="small ts-error">{t("An auth key starts with tskey-auth-.")}</span>}
          </li>
        </ol>
      )}

      {info.host && (
        <div className="row-gap">
          <button className="btn ghost small" disabled={ask.isPending || working} onClick={() => ask.mutate({ action: "status" })}>
            <Icon name="refresh" />{" "}{t("Check again")}
          </button>
          {running && !st?.serve && (
            <button className="btn small" disabled={ask.isPending || working || outside} onClick={() => ask.mutate({ action: "serve" })}>
              {t("Publish again")}
            </button>
          )}
          {running && !compact && (
            <button
              className="btn ghost small"
              disabled={ask.isPending || working || outside}
              onClick={() => window.confirm(t("Disconnect the server from Tailscale? Phones that use the Tailscale address stop reaching it.")) && ask.mutate({ action: "disconnect" })}
            >
              {t("Disconnect")}
            </button>
          )}
          {st?.checked && <span className="muted small">{t("Checked {time}", { time: new Date(st.checked).toLocaleTimeString() })}</span>}
        </div>
      )}
      {outside && <p className="muted small">{t("Set up Tailscale at home: it cannot be changed from outside.")}</p>}
    </div>
  );
}

/** System -> Server: the Tailscale card. */
export function TailscaleCard() {
  return (
    <section className="card" aria-label="Tailscale">
      <div className="card-head">
        <h2>{t("Remote access with Tailscale")}</h2>
        <a className="muted small" href="https://tailscale.com/kb/1017/install" target="_blank" rel="noreferrer noopener">
          {t("What is Tailscale?")}
        </a>
      </div>
      <TailscaleSetup />
    </section>
  );
}
