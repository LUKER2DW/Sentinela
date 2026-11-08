import { useQuery } from "@tanstack/react-query";
import { get } from "../lib/api";
import { CopyField } from "../pages/CloudPage";
import { isApp } from "../lib/push";
import { canChangeConnection, changeConnection } from "../lib/access";
import { Icon } from "./Icon";

import { t } from "../lib/i18n";
/** GET /api/app/pairing: the ways the Android app can reach this house. */
interface PairingOption {
  kind: "tailnet" | "funnel" | "public" | "proxy" | "lan";
  url: string;
  pin?: string;
  /** Short form of the certificate fingerprint, shown by the app too. */
  code?: string;
  link: string;
  qr_svg: string;
}

const kindLabel = {
  tailnet: { title: t("Over Tailscale"), note: t("Only for devices on your Tailscale network (with the Tailscale app on).") },
  public: { title: t("Over the internet"), note: t("Over the home's public address, from anywhere.") },
  funnel: { title: t("Tailscale public link"), note: t("The Tailscale address through Funnel: from anywhere, without the Tailscale app.") },
  proxy: { title: t("Over the tunnel"), note: t("Over the domain served by the tunnel or reverse proxy, from anywhere.") },
  lan: { title: t("On the home network"), note: t("At home, over the Wi-Fi: no internet needed. Use it when the internet address does not open at home.") },
} as const;

/** Pairing QR codes for the one Sentinela APK: the phone's camera opens the
 *  app with the house's address and certificate; the app asks before it
 *  switches. */
export function AppPairingCard() {
  // Inside the app the QR code is something to scan, not to show: this phone
  // is already paired; it can switch houses from the start screen.
  if (isApp()) return <AppConnectionCard />;
  return <PairingCodes />;
}

function AppConnectionCard() {
  return (
    <section className="card" aria-label={t("App connection")}>
      <div className="card-head">
        <h2>{t("This phone's connection")}</h2>
      </div>
      <p className="setup-status ok">
        <Icon name="check" />{" "}{t("Connected to")}{" "}<code>{location.host}</code>
      </p>
      <p className="muted small">
        {t("To connect another phone, open System → App on the computer and point that phone's camera at the QR code. If the home changes address, this app finds its other addresses by itself.")}
      </p>
      {canChangeConnection() && (
        <button className="btn small" onClick={changeConnection}>
          <Icon name="refresh" />{" "}{t("Switch home or address")}
        </button>
      )}
    </section>
  );
}

function PairingCodes() {
  const q = useQuery({ queryKey: ["app", "pairing"], queryFn: () => get<{ options: PairingOption[] }>("/api/app/pairing") });
  const opts = q.data?.options ?? [];
  return (
    <section className="card" aria-label={t("Connect the app")}>
      <div className="card-head">
        <h2>{t("Connect the Android app")}</h2>
      </div>
      <p className="muted small">
        {t("Install the Sentinela app on the phone, open it and point the phone's camera at the code. The app shows the address and asks before connecting. Or copy the pairing code and paste it on the app's start screen. You can also type just the address: the app shows the home code of the address it reached, and you confirm that it is the one shown here.")}
      </p>
      {q.isLoading && <p className="muted">{t("Loading…")}</p>}
      {q.isError && <p className="notice error">{t("Could not read the home addresses.")}</p>}
      {q.isSuccess && opts.length === 0 && (
        <p className="notice">
          {t("The app needs a way from outside the home: Tailscale (")}<code>remote.tailnet_url</code>{t(") or the public address (")}<code>remote.public</code>{t(") in")}{" "}<code>/etc/sentinela/config.yaml</code>{t(". See the installation guide, section Remote access.")}
        </p>
      )}
      <div className="pairing-grid">
        {opts.map((o) => (
          <figure key={o.kind} className="pairing">
            <img src={`data:image/svg+xml;charset=utf-8,${encodeURIComponent(o.qr_svg)}`} alt={t("QR code: {title}", { title: kindLabel[o.kind].title })} />
            <figcaption>
              <strong>{kindLabel[o.kind].title}</strong>
              <span className="muted small">{kindLabel[o.kind].note}</span>
              <code className="pairing-url">{o.url}</code>
              {o.pin && <span className="muted small">{t("The home's own certificate: the app checks its fingerprint.")}</span>}
              {o.code && (
                <span className="muted small">
                  {t("Home code")}: <code>{o.code}</code>
                </span>
              )}
              <CopyField value={o.link} label={t("Pairing code")} />
            </figcaption>
          </figure>
        ))}
      </div>
    </section>
  );
}
