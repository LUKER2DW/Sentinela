import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Icon } from "./Icon";
import { hosting, type EnvVar, type Service, type ServiceSecrets as Secrets } from "../lib/hosting";
import { useToast } from "../lib/session";

import { t } from "../lib/i18n";
export function ServiceSecrets({ svc }: { svc: Service }) {
  const q = useQuery({ queryKey: ["hosting", "secrets", svc.id], queryFn: () => hosting.secrets(svc.id) });
  return <div className="stack-sm">
    <p className="notice small"><Icon name="lock" />{" "}{t("Secrets encrypted on the server. .env files are imported and removed from the project. Stored values are not shown; leave empty to keep or fill in to replace.")}</p>
    {q.error && <p className="notice error" role="alert">{q.error.message} <button className="btn ghost small" onClick={() => void q.refetch()}>{t("Try again")}</button></p>}
    {q.isLoading && <p className="muted small">{t("Opening secrets manager…")}</p>}
    {q.data && <SecretEditor key={q.data.revision} svc={svc} data={q.data} />}
  </div>;
}

function SecretEditor({ svc, data }: { svc: Service; data: Secrets }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [env, setEnv] = useState<EnvVar[]>(() => data.env.map((e) => ({ ...e })));
  const [dirty, setDirty] = useState(false);
  const change = (next: EnvVar[]) => { setEnv(next); setDirty(true); };
  const keys = env.map((e) => e.key);
  const invalid = keys.some((k) => !/^[A-Za-z_][A-Za-z0-9_]{0,63}$/.test(k)) || new Set(keys).size !== keys.length;
  const save = useMutation({
    mutationFn: () => hosting.saveSecrets(svc.id, env, data.revision),
    onSuccess: (result) => { setDirty(false); void qc.invalidateQueries({ queryKey: ["hosting"] }); toast(result.restarting ? t("Secrets saved. Applying and restarting the service; follow the Deploy.") : t("Secrets saved. They will be used on the next start.")); },
  });
  const busy = svc.status.state === "deploying" || save.isPending;
  return <form className="stack-sm svc-secrets" onSubmit={(e) => { e.preventDefault(); if (!invalid) save.mutate(); }}>
    {data.files?.length ? <p className="hint">{t("Imported files:")}{" "}{data.files.join(", ")}.</p> : null}
    {env.length === 0 && <p className="muted small">{t("No secrets yet. Add the variables your project needs.")}</p>}
    {env.map((entry, i) => <div className="svc-secret-row" key={i}>
      <input className="input svc-code" aria-label={t("Secret name {v0}", { v0: i + 1 })} value={entry.key} disabled={busy} spellCheck={false} onChange={(e) => change(env.map((v,j) => j===i ? { ...v, key:e.target.value } : v))} placeholder={"DISCORD_TOKEN"} />
      <input className="input svc-code" type="password" aria-label={t("New value for {v0}", { v0: entry.key || "secret" })} autoComplete="new-password" value={entry.value} disabled={busy} onChange={(e) => change(env.map((v,j) => j===i ? { ...v, value:e.target.value } : v))} placeholder={data.env.some((v) => v.key===entry.key) ? t("Saved · empty keeps it") : t("New value")} />
      <button type="button" className="btn ghost icon small danger" aria-label={t("Delete secret {v0}", { v0: entry.key || i + 1 })} disabled={busy} onClick={() => change(env.filter((_,j) => j!==i))}><Icon name="trash" /></button>
    </div>)}
    <button type="button" className="btn ghost small" disabled={busy || env.length>=64} onClick={() => change([...env,{ key:"", value:"", secret:true }])}><Icon name="plus" />{" "}{t("Add secret")}</button>
    {invalid && <p className="hint bad">{t("Use valid variable names, with no repeats.")}</p>}
    {save.error && <p className="notice error" role="alert">{save.error.message} <button type="button" className="btn ghost small" onClick={() => void qc.invalidateQueries({ queryKey:["hosting","secrets",svc.id] })}>{t("Reload secrets")}</button></p>}
    <div className="dialog-actions"><button type="button" className="btn ghost" disabled={busy || !dirty} onClick={() => { setEnv(data.env.map((e) => ({ ...e }))); setDirty(false); }}>{t("Undo")}</button><button className="btn primary" disabled={busy || invalid}><Icon name="lock" /> {save.isPending ? t("Saving…") : t("Save and apply secrets")}</button></div>
    <p className="hint">{t("Deleting a row removes the secret on save. Running services restart to apply the changes.")}</p>
  </form>;
}
