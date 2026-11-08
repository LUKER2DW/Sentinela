import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, get } from "../lib/api";
import { useToast } from "../lib/session";
import { isPublicZone } from "../lib/access";
import { CopyField } from "../pages/CloudPage";
import type { SettingsDoc } from "./SettingsEditor";
import { Icon } from "./Icon";
import { t } from "../lib/i18n";

/** Remote access in the settings (ADR-0019, ADR-0025). */
export interface SettingsRemote {
  public: { domain: string; port: number; url: string; self_signed: boolean; http01_port: number; email: string };
  proxy: { url: string; port: number; kind: "cloudflare" | "other" };
}

interface RemoteCheckResult {
  target: string;
  url: string;
  ok: boolean;
  reason?: "off" | "dns" | "connect" | "tls" | "other" | "http";
  detail?: string;
  ips?: string[];
  home_ip?: string;
  hint?: string;
  took_ms: number;
}

const IPV4 = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/;
const HOST = /^(?=.{1,253}$)([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$/i;
const HTTPS_URL = /^https:\/\/[A-Za-z0-9.-]+(:\d{1,5})?\/?$/;

function privateIPv4(s: string) {
  const m = IPV4.exec(s);
  if (!m) return false;
  const [a, b] = [Number(m[1]), Number(m[2])];
  return a === 10 || a === 127 || (a === 172 && b >= 16 && b <= 31) || (a === 192 && b === 168) || (a === 100 && b >= 64 && b <= 127);
}

/** Saves only remote access, through the normal settings flow (validated by
 *  the server, applied by the host with a health check and rollback). */
async function saveRemote(patch: (r: SettingsRemote) => SettingsRemote) {
  const info = await get<{ settings: SettingsDoc & { remote?: SettingsRemote } }>("/api/settings");
  const cur = info.settings.remote;
  if (!cur) throw new Error(t("This server does not edit remote access yet: update it first."));
  await api("PUT", "/api/settings", { ...info.settings, remote: patch(cur) });
}

function useRemoteSettings() {
  return useQuery({
    queryKey: ["settings"],
    queryFn: () => get<{ settings: SettingsDoc & { remote?: SettingsRemote }; host: boolean; pending: boolean }>("/api/settings"),
  });
}

function useSaveRemote(done: () => void) {
  const toast = useToast();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: saveRemote,
    onSuccess: () => {
      toast(t("Saved. Applying on the server; the panel comes back in a few seconds."));
      done();
      void qc.invalidateQueries({ queryKey: ["settings"] });
    },
    onError: (e: Error) => toast(e.message, true),
  });
}

const reasonText: Record<string, string> = {
  dns: t("The name does not point anywhere yet. Create the DNS record and wait a few minutes."),
  connect: t("Nothing answered at this address. Check the router's port forward (or the tunnel) and the server's firewall."),
  tls: t("Something answered, but with a certificate that is not valid for this address."),
  other: t("Another server answered at this address, not this Sentinela."),
  http: t("Something else answered at this address (another site or the router's page)."),
};

/** Asks the configured address, over the internet, for this server's id. */
function RemoteCheck({ target }: { target: "public" | "proxy" }) {
  const toast = useToast();
  const [res, setRes] = useState<RemoteCheckResult | null>(null);
  const run = useMutation({
    mutationFn: () => get<RemoteCheckResult>(`/api/remote/check?target=${target}`),
    onSuccess: setRes,
    onError: (e: Error) => toast(e.message, true),
  });
  return (
    <div className="remote-check">
      <button className="btn small" disabled={run.isPending} onClick={() => run.mutate()}>
        <Icon name="network" />{" "}{run.isPending ? t("Checking…") : t("Test from the internet")}
      </button>
      {res && res.reason !== "off" && (
        <div className={`notice${res.ok ? " ok" : " error"}`} role="status">
          {res.ok ? (
            <>
              <Icon name="check" />{" "}{t("{url} reaches this server ({ms} ms).", { url: res.url, ms: res.took_ms })}
            </>
          ) : (
            <>
              {reasonText[res.reason ?? ""] ?? res.detail}
              {res.hint === "loopback" && (
                <span className="muted small">
                  {" "}
                  {t("Many routers do not open their own public address from inside the home: test from a phone on mobile data before changing anything.")}
                </span>
              )}
            </>
          )}
          {(res.ips?.length || res.home_ip) && (
            <span className="muted small remote-facts">
              {res.ips?.length ? t("The name points to {ips}.", { ips: res.ips.join(", ") }) : ""}{" "}
              {res.home_ip ? t("This home's public IP is {ip}.", { ip: res.home_ip }) : ""}
              {res.home_ip && res.ips?.length && !res.ips.includes(res.home_ip) && target === "public" ? " " + t("They differ: point the DNS record (A) to the home's IP.") : ""}
            </span>
          )}
        </div>
      )}
    </div>
  );
}

/** Port forward on the router: domain or fixed IP, the port, the certificate. */
export function PublicAccessCard() {
  const q = useRemoteSettings();
  // Unsaved edits; null = show what is saved.
  const [edit, setEdit] = useState<{ pub: SettingsRemote["public"]; wan: number } | null>(null);
  const save = useSaveRemote(() => setEdit(null));
  const outside = isPublicZone();
  const cur = q.data?.settings.remote?.public;
  const savedWan = cur ? Number(/:(\d+)\/?$/.exec(cur.url)?.[1] ?? (cur.self_signed && cur.domain ? cur.port : 443)) : 443;
  const d = edit?.pub ?? cur ?? null;
  const wan = edit?.wan ?? savedWan;
  const setD = (pub: SettingsRemote["public"]) => setEdit({ pub, wan });
  const setWan = (w: number) => d && setEdit({ pub: d, wan: w });
  if (!q.data) return null;
  if (!cur || !d)
    return (
      <section className="card">
        <h2>{t("Public address (router port)")}</h2>
        <p className="muted small">{t("This server does not edit remote access yet: update it first.")}</p>
      </section>
    );
  const on = d.domain.trim() !== "";
  const host = d.domain.trim();
  const isIP = IPV4.test(host);
  const hostOk = !on || (isIP ? !privateIPv4(host) : HOST.test(host));
  const portOk = d.port >= 1024 && d.port <= 65535 && wan >= 1 && wan <= 65535;
  const url = on ? `https://${host}${wan === 443 ? "" : `:${wan}`}` : "";
  const next: SettingsRemote["public"] = { ...d, domain: host, url: on && url !== `https://${host}` ? url : "" };
  const dirty = JSON.stringify(next) !== JSON.stringify({ ...cur, url: cur.url.replace(/\/$/, "") });
  const busy = save.isPending || !!q.data.pending;

  return (
    <section className="card" aria-label={t("Public address (router port)")}>
      <div className="card-head">
        <h2>{t("Public address (router port)")}</h2>
        <span className={`pill${cur.domain ? " ok" : ""}`}>{cur.domain ? t("On") : t("Off")}</span>
      </div>
      <p className="muted small">
        {t("The router forwards one port to this server, which answers with HTTPS. From outside only the sign-in screen is visible; turn on two-step verification for the accounts used outside. Use it when the internet provider gives the home a public IP (no CGNAT).")}
      </p>
      <div className="settings-grid">
        <label className="field">
          <span>{t("Domain or the home's fixed public IP")}</span>
          <input
            className="input"
            value={d.domain}
            spellCheck={false}
            autoComplete="off"
            placeholder="cam.example.com"
            onChange={(e) => setD({ ...d, domain: e.target.value.trim().toLowerCase() })}
          />
          {!hostOk ? (
            <span className="small ts-error">{isIP ? t("That is a private address: use the home's public IP or a domain.") : t("Type a name like cam.example.com, without https://.")}</span>
          ) : (
            <span className="muted small">{t("Empty = off. A domain needs a DNS A record pointing to the home's public IP.")}</span>
          )}
        </label>
        <label className="field">
          <span>{t("Port on this server")}</span>
          <input className="input" type="number" min={1024} max={65535} value={d.port} onChange={(e) => setD({ ...d, port: Number(e.target.value) })} />
          <span className="muted small">{t("The router sends the outside port here (1024–65535; default 7443).")}</span>
        </label>
        <label className="field">
          <span>{t("Port people use from outside (router)")}</span>
          <input className="input" type="number" min={1} max={65535} value={wan} onChange={(e) => setWan(Number(e.target.value))} />
          <span className="muted small">{t("443 gives an address without a port. Use another (e.g. 8443) if the provider blocks 443.")}</span>
        </label>
        <label className="field">
          <span>{t("Certificate")}</span>
          <select className="input" value={d.self_signed ? "self" : d.http01_port ? "http01" : "acme"} onChange={(e) => {
            const v = e.target.value;
            setD({ ...d, self_signed: v === "self", http01_port: v === "http01" ? d.http01_port || 7080 : 0 });
          }}>
            <option value="acme">{t("Let's Encrypt (automatic, valid in every browser)")}</option>
            <option value="http01">{t("Let's Encrypt over port 80 (provider blocks 443)")}</option>
            <option value="self" disabled={!isIP}>{t("The home's own certificate (the app pins it; browsers warn)")}</option>
          </select>
        </label>
      </div>
      {on && hostOk && portOk && (
        <div className="remote-howto">
          <strong>{t("On the router (port forwarding / virtual server)")}</strong>
          <ul className="small">
            <li>{t("TCP {wan} → this server, port {port}", { wan, port: d.port })}</li>
            {d.http01_port ? <li>{t("TCP 80 → this server, port {port} (certificate only)", { port: d.http01_port })}</li> : null}
            <li>{t("TCP and UDP 8555 → this server, port 8555 (smooth live video; optional)")}</li>
          </ul>
          {!isIP && (
            <>
              <strong>{t("At your domain's DNS")}</strong>
              <ul className="small">
                <li>{t("Record A: {name} → the home's public IP (“Test from the internet” shows it). If the IP changes, use your router's or provider's dynamic DNS.", { name: host })}</li>
              </ul>
            </>
          )}
          <span className="muted small">
            {t("Address people will use:")} <code>{url}</code>
          </span>
        </div>
      )}
      <div className="row-gap">
        <button className="btn primary small" disabled={!dirty || !hostOk || (on && !portOk) || busy || outside} onClick={() => save.mutate((r) => ({ ...r, public: next }))}>
          {t("Save and apply")}
        </button>
        {dirty && (
          <button className="btn ghost small" onClick={() => setEdit(null)}>
            {t("Undo")}
          </button>
        )}
        {cur.domain && !dirty && <RemoteCheck target="public" />}
      </div>
      {outside && <p className="muted small">{t("Change this at home or over Tailscale.")}</p>}
    </section>
  );
}

interface CloudflaredInfo {
  host: boolean;
  status: { installed: boolean; version: string; configured: boolean; running: boolean; connections: number; busy: boolean; message: string; checked: string } | null;
  pending: boolean;
  service: string;
  url: string;
  kind: string;
}

const cfMessage: Record<string, string> = {
  bad_token: t("That token was not accepted: copy the install command from Cloudflare again."),
  install_failed: t("Could not install cloudflared on the server. Check its internet connection and try again."),
  no_connection: t("cloudflared started but did not connect to Cloudflare. Check the token (or create a new one) and the server's internet."),
  disconnected: t("The tunnel was stopped and its token removed from the server."),
  "installing cloudflared": t("Installing cloudflared on the server…"),
  starting: t("Starting the tunnel…"),
};

/** Cloudflare Tunnel (guided) or any other reverse proxy on this server. */
export function TunnelCard() {
  const toast = useToast();
  const qc = useQueryClient();
  const settings = useRemoteSettings();
  const [edit, setEdit] = useState<{ kind: "cloudflare" | "other"; url: string; port: number } | null>(null);
  const save = useSaveRemote(() => setEdit(null));
  const outside = isPublicZone();
  const [watch, setWatch] = useState(false);
  useEffect(() => {
    if (!watch) return;
    const id = window.setTimeout(() => setWatch(false), 180_000);
    return () => window.clearTimeout(id);
  }, [watch]);
  const cf = useQuery({
    queryKey: ["cloudflared"],
    queryFn: () => get<CloudflaredInfo>("/api/cloudflared"),
    refetchInterval: (x) => (watch || x.state.data?.pending || x.state.data?.status?.busy ? 1500 : 30_000),
  });
  const ask = useMutation({
    mutationFn: (body: { action: string; token?: string }) => api("POST", "/api/cloudflared", body),
    onSuccess: (_, b) => {
      setWatch(true);
      if (b.action === "connect") setToken("");
      void qc.invalidateQueries({ queryKey: ["cloudflared"] });
    },
    onError: (e: Error) => toast(e.message, true),
  });
  const cur = settings.data?.settings.remote?.proxy;
  const [token, setToken] = useState("");
  const v = edit ?? (cur ? { kind: cur.kind, url: cur.url, port: cur.port } : { kind: "cloudflare" as const, url: "", port: 7444 });
  const { kind, url, port } = v;
  const setKind = (k: "cloudflare" | "other") => setEdit({ ...v, kind: k });
  const setUrl = (u: string) => setEdit({ ...v, url: u });
  const setPort = (p: number) => setEdit({ ...v, port: p });
  if (!settings.data) return null;
  if (!cur)
    return (
      <section className="card">
        <h2>{t("Tunnel or reverse proxy")}</h2>
        <p className="muted small">{t("This server does not edit remote access yet: update it first.")}</p>
      </section>
    );
  const st = cf.data?.status ?? null;
  const working = !!cf.data?.pending || !!st?.busy;
  const urlOk = url.trim() === "" || HTTPS_URL.test(url.trim());
  const portOk = port >= 1024 && port <= 65535;
  const next = { url: url.trim().replace(/\/$/, ""), port, kind };
  const dirty = JSON.stringify(next) !== JSON.stringify({ ...cur, url: cur.url.replace(/\/$/, "") });
  const service = `http://127.0.0.1:${port}`;
  const busy = save.isPending || !!settings.data.pending;

  return (
    <section className="card" aria-label={t("Tunnel or reverse proxy")}>
      <div className="card-head">
        <h2>{t("Tunnel or reverse proxy")}</h2>
        <span className={`pill${cur.url ? " ok" : ""}`}>{cur.url ? t("On") : t("Off")}</span>
      </div>
      <p className="muted small">
        {t("For homes without a public IP (CGNAT) or that prefer not to open ports: a tunnel or reverse proxy on this server serves your domain with HTTPS and sends it to a private port. Sentinela treats everything that arrives there as coming from the internet: only the sign-in screen, login limits and two-step verification where it is on, no camera ports. Live video uses the compatible mode (no WebRTC, no talk).")}
      </p>
      <div className="segmented" role="tablist">
        <button role="tab" aria-selected={kind === "cloudflare"} onClick={() => setKind("cloudflare")}>
          Cloudflare Tunnel
        </button>
        <button role="tab" aria-selected={kind === "other"} onClick={() => setKind("other")}>
          {t("Other (Caddy, nginx, ngrok…)")}
        </button>
      </div>

      {kind === "cloudflare" ? (
        <ol className="ts-steps">
          <li>
            <strong>{t("Put your domain on Cloudflare")}</strong>
            <span className="muted small">
              {t("A free Cloudflare account with your domain added (its DNS managed by Cloudflare).")}{" "}
              <a href="https://dash.cloudflare.com/" target="_blank" rel="noreferrer noopener">dash.cloudflare.com</a>
            </span>
          </li>
          <li>
            <strong>{t("Create a tunnel")}</strong>
            <span className="muted small">
              {t("Zero Trust → Networks → Tunnels → Create a tunnel → Cloudflared. Name it sentinela, choose Debian, and copy the install command (it contains the token).")}{" "}
              <a href="https://one.dash.cloudflare.com/" target="_blank" rel="noreferrer noopener">one.dash.cloudflare.com</a>
            </span>
          </li>
          <li>
            <strong>{t("Paste the command or token here")}</strong>
            {cf.data && !cf.data.host ? (
              <span className="notice small">
                {t("This server cannot run the tunnel from the panel yet. Run once on the server")}{" "}<code>sudo /opt/sentinela/scripts/setup-cloudflared.sh units</code>{" "}{t("(or run a deploy).")}
              </span>
            ) : (
              <>
                <span className="muted small">{t("The server installs cloudflared and keeps the tunnel running. The token stays only on the server.")}</span>
                <form
                  className="ts-key"
                  onSubmit={(e) => {
                    e.preventDefault();
                    if (/eyJ[A-Za-z0-9+/=_-]{50,}/.test(token)) ask.mutate({ action: "connect", token });
                  }}
                >
                  <input
                    className="input"
                    type="password"
                    autoComplete="off"
                    spellCheck={false}
                    placeholder="cloudflared service install eyJ…"
                    aria-label={t("Tunnel token")}
                    value={token}
                    onChange={(e) => setToken(e.target.value)}
                    disabled={outside || working}
                  />
                  <button className="btn primary" type="submit" disabled={!/eyJ[A-Za-z0-9+/=_-]{50,}/.test(token) || ask.isPending || outside || working}>
                    {st?.configured ? t("Replace") : t("Connect")}
                  </button>
                </form>
                {st && (
                  <ul className="ts-checks">
                    <li className={`ts-check ${st.installed ? "ok" : ""}`}><Icon name={st.installed ? "check" : "info"} /><span>{t("cloudflared installed")}{st.version ? ` (${st.version})` : ""}</span></li>
                    <li className={`ts-check ${st.running && st.connections > 0 ? "ok" : st.configured ? "no" : ""}`}>
                      <Icon name={st.running && st.connections > 0 ? "check" : st.configured ? "x" : "info"} />
                      <span>{st.running && st.connections > 0 ? t("Tunnel connected to Cloudflare ({n} connections)", { n: st.connections }) : t("Tunnel connected to Cloudflare")}</span>
                    </li>
                  </ul>
                )}
                {working && (
                  <p className="setup-status" role="status">
                    <Icon name="refresh" />{" "}{st?.busy && cfMessage[st.message] ? cfMessage[st.message] : t("The server is working on it…")}
                  </p>
                )}
                {!working && st?.message && st.message !== "ok" && cfMessage[st.message] && (
                  <p className={`notice${st.message === "disconnected" ? "" : " error"}`}>{cfMessage[st.message]}</p>
                )}
              </>
            )}
          </li>
          <li>
            <strong>{t("Add a public hostname to the tunnel")}</strong>
            <span className="muted small">
              {t("In the tunnel → Public Hostname → Add: choose the subdomain and domain (e.g. cam.example.com), Service type HTTP, URL:")}
            </span>
            <CopyField value={service.replace("http://", "")} label={t("Service URL")} />
          </li>
          <li>
            <strong>{t("Type that address here and save")}</strong>
          </li>
        </ol>
      ) : (
        <div className="remote-howto">
          <span className="muted small">
            {t("Run your proxy on this server and send your domain to the address below, keeping the Host header and adding X-Forwarded-For and X-Forwarded-Proto: https. The proxy provides the HTTPS certificate. WebSockets must pass through.")}
          </span>
          <CopyField value={service} label={t("Proxy target")} />
          <span className="muted small">{t("Caddy example:")}</span>
          <code className="remote-snippet">{`${(url.trim() || "https://cam.example.com").replace(/^https:\/\//, "").replace(/\/$/, "")} {\n  reverse_proxy ${service.replace("http://", "")}\n}`}</code>
        </div>
      )}

      <div className="settings-grid">
        <label className="field">
          <span>{t("Public address (https://…)")}</span>
          <input className="input" value={url} spellCheck={false} autoComplete="off" placeholder="https://cam.example.com" onChange={(e) => setUrl(e.target.value.trim())} />
          {!urlOk ? <span className="small ts-error">{t("Use https:// and only the name (and port), no path.")}</span> : <span className="muted small">{t("Empty = off. Offered to the app and used in invite links.")}</span>}
        </label>
        <label className="field">
          <span>{t("Private port on this server")}</span>
          <input className="input" type="number" min={1024} max={65535} value={port} onChange={(e) => setPort(Number(e.target.value))} />
          <span className="muted small">{t("Only reachable from this server (127.0.0.1); default 7444.")}</span>
        </label>
      </div>
      <div className="row-gap">
        <button className="btn primary small" disabled={!dirty || !urlOk || !portOk || busy || outside} onClick={() => save.mutate((r) => ({ ...r, proxy: next }))}>
          {t("Save and apply")}
        </button>
        {cur.url && !dirty && <RemoteCheck target="proxy" />}
        {kind === "cloudflare" && st?.configured && (
          <button
            className="btn ghost small"
            disabled={ask.isPending || working || outside}
            onClick={() => window.confirm(t("Stop the tunnel and remove its token from the server?")) && ask.mutate({ action: "disconnect" })}
          >
            {t("Stop the tunnel")}
          </button>
        )}
      </div>
      {outside && <p className="muted small">{t("Change this at home or over Tailscale.")}</p>}
    </section>
  );
}
