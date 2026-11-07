import { useState } from "react";
import { installUpdate, useAppUpdate } from "../lib/appUpdate";
import { isApp } from "../lib/push";
import { useToast } from "../lib/session";
import { fmtBytes } from "../lib/time";
import { CardHead } from "./CardHead";
import { Icon } from "./Icon";

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
        <strong>App {release?.version_name} disponível</strong>
        <span className="muted small">{pct !== null ? `Baixando… ${pct}%` : "Atualiza sem sair do app."}</span>
      </span>
      <button className="btn small primary" disabled={pct !== null} onClick={start}>
        {pct !== null ? `${pct}%` : "Atualizar"}
      </button>
      <button className="btn ghost icon small" aria-label="Agora não" onClick={() => setHidden(true)}>
        <Icon name="x" />
      </button>
    </div>
  );
}

/** Minha conta → Aplicativo: installed version, update, or the APK for a new phone. */
export function AppCard() {
  const { info, release, newer } = useAppUpdate();
  const { start, pct, needsPermission } = useInstall();
  const inApp = isApp();
  const apk = release?.available ? "/api/app/sentinela.apk" : null;
  const android = /Android/i.test(navigator.userAgent);

  return (
    <section className="card set-card" aria-label="Aplicativo Android">
      <CardHead
        icon="app"
        tone={newer ? "amber" : inApp ? "kelp" : undefined}
        title="Aplicativo Android"
        sub={
          inApp
            ? info
              ? `Você usa a versão ${info.versionName}.`
              : "Esta versão do app não se atualiza sozinha: instale a nova uma vez e as próximas chegam por aqui."
            : "O app recebe avisos com o celular bloqueado e fecha sozinho com a trava do celular."
        }
        aside={
          inApp && info ? (
            <span className={`pill${newer ? "" : " ok"}`}>
              <span className={`dot ${newer ? "warn" : "ok"}`} /> {newer ? "Atualização disponível" : "Em dia"}
            </span>
          ) : undefined
        }
      />
      {inApp && info && newer && release && (
        <div className="update-box">
          <div>
            <strong>Versão {release.version_name}</strong>
            <p className="muted small">
              {fmtBytes(release.size ?? 0)}
              {release.notes ? `. ${release.notes}` : ""}
            </p>
          </div>
          <button className="btn primary" disabled={pct !== null} onClick={start}>
            <Icon name="download" /> {pct !== null ? `Baixando ${pct}%` : "Atualizar agora"}
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
          O Android pediu permissão: ative <strong>Permitir desta fonte</strong> para o Sentinela, volte aqui e toque em Atualizar agora de novo. Só é
          preciso uma vez.
        </p>
      )}
      {(!inApp || !info) && apk && (
        <div className="row-actions">
          <a className={`btn${android || inApp ? " primary" : ""}`} href={apk} download="sentinela.apk">
            <Icon name="download" /> Baixar o app {release?.version_name} ({fmtBytes(release?.size ?? 0)})
          </a>
        </div>
      )}
      {!apk && !inApp && <p className="muted small">Nenhuma versão do app foi publicada neste servidor ainda.</p>}
      <p className="muted small card-foot">
        {inApp
          ? "A atualização vem do servidor da casa, não da Play Store. O Android confere a assinatura antes de instalar."
          : "No celular, abra o arquivo baixado e permita instalar. Depois, o próprio app avisa quando houver versão nova."}
      </p>
    </section>
  );
}
