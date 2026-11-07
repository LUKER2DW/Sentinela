import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Icon } from "./Icon";
import { hosting, type EnvVar, type Service, type ServiceSecrets as Secrets } from "../lib/hosting";
import { useToast } from "../lib/session";

export function ServiceSecrets({ svc }: { svc: Service }) {
  const q = useQuery({ queryKey: ["hosting", "secrets", svc.id], queryFn: () => hosting.secrets(svc.id) });
  return <div className="stack-sm">
    <p className="notice small"><Icon name="lock" /> Secrets criptografados no servidor. Os arquivos .env são importados e removidos do projeto. Valores guardados não são exibidos; deixe vazio para manter ou preencha para trocar.</p>
    {q.error && <p className="notice error" role="alert">{q.error.message} <button className="btn ghost small" onClick={() => void q.refetch()}>Tentar novamente</button></p>}
    {q.isLoading && <p className="muted small">Abrindo gerenciador de secrets…</p>}
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
    onSuccess: (result) => { setDirty(false); void qc.invalidateQueries({ queryKey: ["hosting"] }); toast(result.restarting ? "Secrets salvos. Aplicando e reiniciando o serviço; acompanhe o Deploy." : "Secrets salvos. Serão usados no próximo início."); },
  });
  const busy = svc.status.state === "deploying" || save.isPending;
  return <form className="stack-sm svc-secrets" onSubmit={(e) => { e.preventDefault(); if (!invalid) save.mutate(); }}>
    {data.files?.length ? <p className="hint">Arquivos importados: {data.files.join(", ")}.</p> : null}
    {env.length === 0 && <p className="muted small">Nenhum secret cadastrado. Adicione as variáveis que seu projeto precisa.</p>}
    {env.map((entry, i) => <div className="svc-secret-row" key={i}>
      <input className="input svc-code" aria-label={`Nome do secret ${i + 1}`} value={entry.key} disabled={busy} spellCheck={false} onChange={(e) => change(env.map((v,j) => j===i ? { ...v, key:e.target.value } : v))} placeholder="DISCORD_TOKEN" />
      <input className="input svc-code" type="password" aria-label={`Novo valor de ${entry.key || "secret"}`} autoComplete="new-password" value={entry.value} disabled={busy} onChange={(e) => change(env.map((v,j) => j===i ? { ...v, value:e.target.value } : v))} placeholder={data.env.some((v) => v.key===entry.key) ? "Guardado · vazio mantém" : "Novo valor"} />
      <button type="button" className="btn ghost icon small danger" aria-label={`Excluir secret ${entry.key || i + 1}`} disabled={busy} onClick={() => change(env.filter((_,j) => j!==i))}><Icon name="trash" /></button>
    </div>)}
    <button type="button" className="btn ghost small" disabled={busy || env.length>=64} onClick={() => change([...env,{ key:"", value:"", secret:true }])}><Icon name="plus" /> Adicionar secret</button>
    {invalid && <p className="hint bad">Use nomes de variáveis válidos, sem repetições.</p>}
    {save.error && <p className="notice error" role="alert">{save.error.message} <button type="button" className="btn ghost small" onClick={() => void qc.invalidateQueries({ queryKey:["hosting","secrets",svc.id] })}>Recarregar secrets</button></p>}
    <div className="dialog-actions"><button type="button" className="btn ghost" disabled={busy || !dirty} onClick={() => { setEnv(data.env.map((e) => ({ ...e }))); setDirty(false); }}>Desfazer</button><button className="btn primary" disabled={busy || invalid}><Icon name="lock" /> {save.isPending ? "Salvando…" : "Salvar e aplicar secrets"}</button></div>
    <p className="hint">Excluir uma linha remove o secret ao salvar. Serviços ligados reiniciam para aplicar as alterações.</p>
  </form>;
}
