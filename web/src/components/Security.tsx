import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, get, type User } from "../lib/api";
import { useToast } from "../lib/session";
import { canChangeConnection, changeConnection, isPublicZone, zoneLabel } from "../lib/access";
import { PasswordField } from "./PasswordField";
import { CardHead } from "./CardHead";

import { t } from "../lib/i18n";
interface TotpSetup {
  secret: string;
  uri: string;
  qr_svg: string;
}

/**
 * How this device is connected, and two-step login (ADR-0019): required to
 * sign in from outside (Funnel or the home's domain), enrolled only at home
 * or over Tailscale.
 */
export function SecurityCard() {
  const qc = useQueryClient();
  const toast = useToast();
  const me = useQuery({ queryKey: ["me"], queryFn: () => get<{ user: User }>("/api/me") });
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [setup, setSetup] = useState<TotpSetup | null>(null);
  const [off, setOff] = useState(false);
  const outside = isPublicZone();
  const on = !!me.data?.user.totp;

  const done = (text: string) => {
    toast(text);
    setSetup(null);
    setOff(false);
    setPassword("");
    setCode("");
    void qc.invalidateQueries({ queryKey: ["me"] });
    void qc.invalidateQueries({ queryKey: ["my-sessions"] });
  };
  const start = useMutation({
    mutationFn: () => api<TotpSetup>("POST", "/api/me/totp/setup", { password }),
    onSuccess: (r) => {
      setSetup(r);
      setPassword("");
    },
    onError: (e: Error) => toast(e.message.includes("password") ? t("Wrong password.") : e.message, true),
  });
  const enable = useMutation({
    mutationFn: () => api("POST", "/api/me/totp/enable", { code: code.replace(/\s/g, "") }),
    onSuccess: () => done(t("Two-step verification turned on.")),
    onError: (e: Error) => toast(e.message.includes("invalid code") ? t("Wrong code. Check the phone's clock and try the next one.") : e.message, true),
  });
  const disable = useMutation({
    mutationFn: () => api("POST", "/api/me/totp/disable", { password }),
    onSuccess: () => done(t("Two-step verification turned off.")),
    onError: (e: Error) => toast(e.message.includes("password") ? t("Wrong password.") : e.message, true),
  });

  return (
    <section className="card set-card" aria-label={t("Two-step verification")}>
      <CardHead
        icon="shield"
        tone={on ? "kelp" : "amber"}
        title={t("Two-step verification")}
        sub={on ? t("Sign-in asks for the authenticator app code, at home and outside.") : t("Optional: with it, sign-in also asks for an authenticator app code.")}
        aside={
          <span className={`pill${on ? " ok" : ""}`}>
            <span className={`dot ${on ? "ok" : "warn"}`} /> {on ? t("Active") : t("Off")}
          </span>
        }
      />
      <div className="set-row">
        <div>
          <strong>{t("Connected by")}</strong>
          <p className="muted small">{zoneLabel()}</p>
        </div>
        {canChangeConnection() && (
          <button className="btn small ghost" onClick={changeConnection}>
            {t("Change")}
          </button>
        )}
      </div>

      {outside ? (
        <p className="muted small card-foot">{t("Turning two-step verification on or off can only be done on the home Wi-Fi (for safety, not over the public address).")}</p>
      ) : setup ? (
        <div className="stack-sm card-foot totp-setup">
          <p className="small">
            {t("1. In your authenticator app (Google Authenticator, Microsoft Authenticator, Aegis…), add an account by scanning this QR code.")}
          </p>
          <img className="totp-qr" alt={t("QR code for two-step verification")} src={`data:image/svg+xml;utf8,${encodeURIComponent(setup.qr_svg)}`} />
          <p className="muted small">
            {t("On the phone itself?")}{" "}<a href={setup.uri}>{t("Open in the authenticator app")}</a>{" "}{t("or type the key:")}{" "}<code className="totp-secret">{setup.secret.replace(/(.{4})/g, "$1 ").trim()}</code>
          </p>
          <div className="field">
            <label htmlFor="totp-code">{t("2. Enter the 6-digit code that appears")}</label>
            <input
              id="totp-code"
              className="input otp"
              inputMode="numeric"
              autoComplete="one-time-code"
              maxLength={7}
              placeholder="000 000"
              value={code}
              onChange={(e) => setCode(e.target.value)}
            />
          </div>
          <div className="row-gap">
            <button className="btn primary" disabled={enable.isPending || code.replace(/\s/g, "").length !== 6} onClick={() => enable.mutate()}>
              {t("Enable")}
            </button>
            <button className="btn ghost" onClick={() => setSetup(null)}>
              {t("Cancel")}
            </button>
          </div>
        </div>
      ) : (
        <form
          className="stack-sm card-foot"
          onSubmit={(e) => {
            e.preventDefault();
            if (on) disable.mutate();
            else start.mutate();
          }}
        >
          {(!on || off) && <PasswordField label={t("Your password")} value={password} onChange={setPassword} autoComplete="current-password" />}
          {on && !off ? (
            <button type="button" className="btn ghost danger" onClick={() => setOff(true)}>
              {t("Turn off verification")}
            </button>
          ) : (
            <button className={`btn${on ? " ghost danger" : " primary"}`} disabled={!password || start.isPending || disable.isPending}>
              {on ? t("Turn off verification") : t("Turn on two-step verification")}
            </button>
          )}
        </form>
      )}
    </section>
  );
}
