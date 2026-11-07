import { useState } from "react";
import { installUpdate, useAppUpdate } from "../lib/appUpdate";
import { isApp } from "../lib/push";
import { useToast } from "../lib/session";
import { fmtBytes } from "../lib/time";
import { CardHead } from "./CardHead";
import { Icon } from "./Icon";

import { t } from "../lib/i18n";
/** Runs the download + install, with progress and the one-time permission step. */
function useInstall() {
  const toast = useToast();
  const { release } = useAppUpdate();
  const [pct, setPct] = useState<number | null>(null);
  const [needsPermission, setNeedsPermission] = useState(false);
  const start = () => {
    if (!release || pct !== null) return;
    setPct(0);
    installUpdate(release, setPct)
      .then((state) => setNeedsPermission(state === "permission"))
      .catch((e: Error) => toast(e.message, true))
      .finally(() => setPct(null));
  };
  return { start, pct, needsPermission };
}

/** Thin banner in the app when the server has a newer APK. */
export function AppUpdateBanner() {
  const { newer, release } = useAppUpdate();
  const { start, pct } = useInstall();
  const [hidden, setHidden] = useState(false);
  if (!newer || hidden) return null;
  return (
    <div className="app-update" role="status">
      <Icon name="app" />
      <span className="grow">
        <strong>{t("App")}{" "}{release?.version_name}{" "}{t("available")}</strong>
        <span className="muted small">{pct !== null ? t("Downloading… {pct}%", { pct }) : t("Updates without leaving the app.")}</span>
      </span>
      <button className="btn small primary" disabled={pct !== null} onClick={start}>
        {pct !== null ? `${pct}%` : t("Update")}
      </button>
      <button className="btn ghost icon small" aria-label={t("Not now")} onClick={() => setHidden(true)}>
        <Icon name="x" />
      </button>
    </div>
  );
}

/** Sistema → Aplicativo: installed version, update, or the APK for a new phone. */
export function AppCard() {
  const { info, release, newer } = useAppUpdate();
  const { start, pct, needsPermission } = useInstall();
  const inApp = isApp();
  const apk = release?.available ? "/api/app/sentinela.apk" : null;
  const android = /Android/i.test(navigator.userAgent);

  return (
    <section className="card set-card" aria-label={t("Android app")}>
      <CardHead
        icon="app"
        tone={newer ? "amber" : inApp ? "kelp" : undefined}
        title={t("Android app")}
        sub={
          inApp
            ? info
              ? t("You are using version {versionName}.", { versionName: info.versionName })
              : t("This app version does not update by itself: install the new one once and the next ones arrive here.")
            : t("The app receives notices with the phone locked and closes by itself with the phone lock.")
        }
        aside={
          inApp && info ? (
            <span className={`pill${newer ? "" : " ok"}`}>
              <span className={`dot ${newer ? "warn" : "ok"}`} /> {newer ? t("Update available") : t("Up to date")}
            </span>
          ) : undefined
        }
      />
      {inApp && info && newer && release && (
        <div className="update-box">
          <div>
            <strong>{t("Version")}{" "}{release.version_name}</strong>
            <p className="muted small">
              {fmtBytes(release.size ?? 0)}
              {release.notes ? `. ${release.notes}` : ""}
            </p>
          </div>
          <button className="btn primary" disabled={pct !== null} onClick={start}>
            <Icon name="download" /> {pct !== null ? t("Downloading {pct}%", { pct }) : t("Update now")}
          </button>
          {pct !== null && (
            <span className="meter thin update-meter" aria-hidden="true">
              <span style={{ width: `${pct}%` }} />
            </span>
          )}
        </div>
      )}
      {needsPermission && (
        <p className="notice small">
          {t("Android asked for permission: turn on")}{" "}<strong>{t("Allow from this source")}</strong>{" "}{t("to Sentinela, come back here and tap Update now again. It only needs to happen once.")}
        </p>
      )}
      {(!inApp || !info) && apk && (
        <div className="row-actions">
          <a className={`btn${android || inApp ? " primary" : ""}`} href={apk} download="sentinela.apk">
            <Icon name="download" />{" "}{t("Download the app")}{" "}{release?.version_name} ({fmtBytes(release?.size ?? 0)})
          </a>
        </div>
      )}
      {!apk && !inApp && <p className="muted small">{t("No app version has been published on this server yet.")}</p>}
      <p className="muted small card-foot">
        {inApp
          ? t("The update comes from the home server, not the Play Store. Android checks the signature before installing.")
          : t("On the phone, open the downloaded file and allow the install. Afterwards, the app itself tells you when there is a new version.")}
      </p>
    </section>
  );
}
