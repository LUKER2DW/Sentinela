import { useState, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, get } from "../lib/api";
import { useToast } from "../lib/session";
import { isPublicZone } from "../lib/access";
import { Icon } from "./Icon";

// Settings editors for System (ADR-0024). The API validates a change and
// leaves it for the host, which applies it, restarts what needs it and
// rolls back on failure; the dashboard reloads by itself meanwhile.

export interface SettingsCamera {
  id: string;
  name: string;
  host: string;
  vendor: string;
  channel: number;
  rtsp_main: string;
  rtsp_sub: string;
  rtsp_port: number;
  dvrip_port: number;
  onvif_port: number;
  retention_days: number;
  record: boolean;
  record_audio: boolean;
  preload: boolean;
  invert_pan_tilt: boolean;
  user?: string;
  password?: string;
}
export interface SettingsVolume {
  id: string;
  name: string;
  path: string;
  kind: string;
  share: string;
}
export interface SettingsDoc {
  timezone: string;
  nas_host: string;
  tailnet_url: string;
  recording: {
    segment_seconds: number;
    max_disk_percent: number;
    default_retention_days: number;
    alert_retention_days: number;
    volume_marker: string;
  };
  cameras: SettingsCamera[];
  volumes: SettingsVolume[];
}
interface SettingsInfo {
  settings: SettingsDoc;
  vendors: string[];
  pending: boolean;
  last: { at: string; state: "applying" | "applied" | "failed"; message: string } | null;
  host: boolean;
}

const vendorLabel: Record<string, string> = { xmeye: "XMEye", hikvision: "Hikvision", dahua: "Dahua", generic: "RTSP genérica" };

function useSettings() {
  return useQuery({
    queryKey: ["settings"],
    queryFn: () => get<SettingsInfo>("/api/settings"),
    // While the host applies a change the API restarts: keep asking.
    refetchInterval: (q) => (q.state.data?.pending || q.state.data?.last?.state === "applying" || q.state.error ? 3000 : false),
    retry: 10,
  });
}

function useApply() {
  const toast = useToast();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (doc: SettingsDoc) => api("PUT", "/api/settings", doc),
    onSuccess: () => {
      toast("Salvo. Aplicando no servidor; o painel volta em alguns segundos.");
      void qc.invalidateQueries({ queryKey: ["settings"] });
    },
    onError: (e: Error) => toast(e.message, true),
  });
}

/** Where a change stands, above each editor. */
function ApplyState({ info }: { info?: SettingsInfo }) {
  if (!info) return null;
  if (!info.host)
    return (
      <p className="notice">
        Este servidor ainda não aplica mudanças do painel. Rode uma vez{" "}
        <code>sudo /opt/sentinela/scripts/setup-config-apply.sh</code> (ou faça um deploy).
      </p>
    );
  if (info.pending || info.last?.state === "applying")
    return (
      <p className="setup-status">
        <Icon name="refresh" /> Aplicando as mudanças… gravação e ao vivo voltam em segundos.
      </p>
    );
  if (info.last?.state === "failed")
    return (
      <p className="notice error">
        A última mudança não foi aplicada e o servidor voltou à anterior: {info.last.message}
      </p>
    );
  return null;
}

/** Shared shell: a card with the editor and its save button. */
function EditorCard(props: {
  title: string;
  hint: string;
  info?: SettingsInfo;
  dirty: boolean;
  onSave: () => void;
  onReset: () => void;
  busy: boolean;
  children: ReactNode;
}) {
  const locked = !props.info?.host || props.info.pending || isPublicZone();
  return (
    <section className="card settings-card">
      <div className="card-head">
        <h2>{props.title}</h2>
        <span className="muted small">{props.hint}</span>
      </div>
      <ApplyState info={props.info} />
      {props.children}
      <div className="settings-actions">
        {isPublicZone() && <span className="muted small">Mude as configurações em casa ou pelo Tailscale.</span>}
        <span className="spacer" />
        {props.dirty && (
          <button className="btn ghost small" onClick={props.onReset} disabled={props.busy}>
            Descartar
          </button>
        )}
        <button className="btn primary small" onClick={props.onSave} disabled={!props.dirty || props.busy || locked}>
          Salvar e aplicar
        </button>
      </div>
    </section>
  );
}

/** The editor's working copy. It follows the server whenever the saved
 *  settings change (after an apply), and keeps unsaved edits otherwise. */
function useDraft(info?: SettingsInfo) {
  const saved = info && !info.pending ? JSON.stringify(info.settings) : "";
  const [state, setState] = useState<{ saved: string; draft: SettingsDoc | null }>({ saved: "", draft: null });
  if (saved && saved !== state.saved) setState({ saved, draft: JSON.parse(saved) as SettingsDoc });
  const draft = state.draft;
  const setDraft = (d: SettingsDoc) => setState((s) => ({ ...s, draft: d }));
  const dirty = !!draft && !!saved && JSON.stringify(draft) !== saved;
  return { draft, setDraft, dirty, reset: () => saved && setDraft(JSON.parse(saved) as SettingsDoc) };
}

function NumField(p: { label: string; value: number; min?: number; max?: number; onChange: (n: number) => void; hint?: string }) {
  return (
    <label className="field">
      <span>{p.label}</span>
      <input
        className="input"
        type="number"
        min={p.min}
        max={p.max}
        value={Number.isFinite(p.value) ? p.value : 0}
        onChange={(e) => p.onChange(Number(e.target.value))}
      />
      {p.hint && <span className="muted small">{p.hint}</span>}
    </label>
  );
}

function TextField(p: { label: string; value: string; onChange: (v: string) => void; placeholder?: string; hint?: string; type?: string; autoComplete?: string }) {
  return (
    <label className="field">
      <span>{p.label}</span>
      <input
        className="input"
        type={p.type ?? "text"}
        value={p.value}
        placeholder={p.placeholder}
        autoComplete={p.autoComplete ?? "off"}
        spellCheck={false}
        onChange={(e) => p.onChange(e.target.value)}
      />
      {p.hint && <span className="muted small">{p.hint}</span>}
    </label>
  );
}

function Check(p: { label: string; checked: boolean; onChange: (v: boolean) => void }) {
  return (
    <label className="check-row">
      <input type="checkbox" checked={p.checked} onChange={(e) => p.onChange(e.target.checked)} /> {p.label}
    </label>
  );
}

/** While the settings load, or when this server cannot edit them. */
function EditorUnavailable({ q }: { q: ReturnType<typeof useSettings> }) {
  if (q.isLoading) return null;
  return (
    <p className="notice">
      As configurações não puderam ser lidas{q.error ? `: ${(q.error as Error).message}` : ""}. Atualize o servidor (deploy) para editar por
      aqui.
    </p>
  );
}

// ---------- recording ----------

export function RecordingEditor() {
  const q = useSettings();
  const apply = useApply();
  const { draft, setDraft, dirty, reset } = useDraft(q.data);
  if (!draft) return <EditorUnavailable q={q} />;
  const r = draft.recording;
  const set = (patch: Partial<SettingsDoc["recording"]>) => setDraft({ ...draft, recording: { ...r, ...patch } });
  return (
    <EditorCard title="Configurar gravação" hint="Vale para todas as câmeras" info={q.data} dirty={dirty} onSave={() => apply.mutate(draft)} onReset={reset} busy={apply.isPending}>
      <div className="settings-grid">
        <NumField label="Usar o disco até (%)" min={50} max={98} value={r.max_disk_percent} onChange={(n) => set({ max_disk_percent: n })} hint="Acima disso as gravações mais antigas são substituídas." />
        <NumField label="Guardar no máximo (dias)" min={0} value={r.default_retention_days} onChange={(n) => set({ default_retention_days: n })} hint="0 = até o disco encher." />
        <NumField label="Guardar alertas por (dias)" min={0} value={r.alert_retention_days} onChange={(n) => set({ alert_retention_days: n })} hint="Trechos com pessoa, veículo ou linha cruzada. 0 = igual aos outros." />
        <NumField label="Tamanho de cada arquivo (s)" min={30} max={3600} value={r.segment_seconds} onChange={(n) => set({ segment_seconds: n })} />
      </div>
      <Check
        label="Gravar só no disco de gravações (pausa se ele não montar)"
        checked={r.volume_marker !== "none"}
        onChange={(v) => set({ volume_marker: v ? ".sentinela-volume" : "none" })}
      />
    </EditorCard>
  );
}

// ---------- cameras ----------

const blankCamera = (n: number): SettingsCamera => ({
  id: `cam${n}`,
  name: `Câmera ${n}`,
  host: "",
  vendor: "xmeye",
  channel: 1,
  rtsp_main: "",
  rtsp_sub: "",
  rtsp_port: 554,
  dvrip_port: 34567,
  onvif_port: 0,
  retention_days: 0,
  record: true,
  record_audio: true,
  preload: true,
  invert_pan_tilt: false,
  user: "admin",
  password: "",
});

export function CamerasEditor() {
  const q = useSettings();
  const apply = useApply();
  const { draft, setDraft, dirty, reset } = useDraft(q.data);
  const [open, setOpen] = useState<string | null>(null);
  const [remove, setRemove] = useState<string | null>(null);
  if (!draft) return <EditorUnavailable q={q} />;
  const known = new Set(q.data?.settings.cameras.map((c) => c.id));
  const setCam = (i: number, patch: Partial<SettingsCamera>) =>
    setDraft({ ...draft, cameras: draft.cameras.map((c, j) => (j === i ? { ...c, ...patch } : c)) });
  const add = () => {
    let n = draft.cameras.length + 1;
    while (draft.cameras.some((c) => c.id === `cam${n}`)) n++;
    const c = blankCamera(n);
    setDraft({ ...draft, cameras: [...draft.cameras, c] });
    setOpen(c.id);
  };
  return (
    <EditorCard title="Configurar câmeras" hint="Endereço, marca, gravação e acesso" info={q.data} dirty={dirty} onSave={() => apply.mutate(draft)} onReset={reset} busy={apply.isPending}>
      <ul className="settings-list">
        {draft.cameras.map((c, i) => {
          const isNew = !known.has(c.id);
          const expanded = open === c.id;
          return (
            <li key={i} className={expanded ? "open" : ""}>
              <button className="settings-row" onClick={() => setOpen(expanded ? null : c.id)} aria-expanded={expanded}>
                <Icon name="video" />
                <strong>{c.name || c.id}</strong>
                <span className="muted small">
                  {vendorLabel[c.vendor] ?? c.vendor} · {c.host || "sem endereço"} · {c.record ? "gravando" : "sem gravação"}
                  {isNew ? " · nova" : ""}
                </span>
                <Icon name={expanded ? "up" : "down"} />
              </button>
              {expanded && (
                <div className="settings-body">
                  <div className="settings-grid">
                    <TextField label="Nome" value={c.name} onChange={(v) => setCam(i, { name: v })} />
                    <TextField label="Identificador" value={c.id} onChange={(v) => setCam(i, { id: v.toLowerCase() })} hint={isNew ? "Letras, números, - e _ (não muda depois)." : "Não pode ser mudado."} />
                    <TextField label="Endereço IP" value={c.host} onChange={(v) => setCam(i, { host: v.trim() })} placeholder="192.168.1.20" />
                    <label className="field">
                      <span>Marca</span>
                      <select className="input" value={c.vendor} onChange={(e) => setCam(i, { vendor: e.target.value })}>
                        {(q.data?.vendors ?? []).map((v) => (
                          <option key={v} value={v}>
                            {vendorLabel[v] ?? v}
                          </option>
                        ))}
                      </select>
                    </label>
                    {c.vendor === "generic" && (
                      <>
                        <TextField label="Caminho do stream principal" value={c.rtsp_main} onChange={(v) => setCam(i, { rtsp_main: v })} placeholder="/stream1" />
                        <TextField label="Caminho do stream leve (opcional)" value={c.rtsp_sub} onChange={(v) => setCam(i, { rtsp_sub: v })} placeholder="/stream2" />
                      </>
                    )}
                    <NumField label="Canal (NVR/DVR)" min={1} max={64} value={c.channel} onChange={(n) => setCam(i, { channel: n })} />
                    <NumField label="Porta RTSP" min={1} max={65535} value={c.rtsp_port} onChange={(n) => setCam(i, { rtsp_port: n })} />
                    {c.vendor === "xmeye" && <NumField label="Porta DVRIP" min={1} max={65535} value={c.dvrip_port} onChange={(n) => setCam(i, { dvrip_port: n })} />}
                    <NumField label="Porta ONVIF (eventos)" min={0} max={65535} value={c.onvif_port} onChange={(n) => setCam(i, { onvif_port: n })} hint="0 = padrão da marca." />
                    <NumField label="Guardar por (dias)" min={0} value={c.retention_days} onChange={(n) => setCam(i, { retention_days: n })} hint="0 = até o disco encher." />
                  </div>
                  <div className="settings-checks">
                    <Check label="Gravar" checked={c.record} onChange={(v) => setCam(i, { record: v })} />
                    <Check label="Gravar o áudio" checked={c.record_audio} onChange={(v) => setCam(i, { record_audio: v })} />
                    <Check label="Ao vivo instantâneo (mantém a conexão aberta)" checked={c.preload} onChange={(v) => setCam(i, { preload: v })} />
                    {c.vendor === "xmeye" && <Check label="Câmera de cabeça para baixo (inverte o PTZ)" checked={c.invert_pan_tilt} onChange={(v) => setCam(i, { invert_pan_tilt: v })} />}
                  </div>
                  <div className="settings-grid">
                    <TextField label="Usuário da câmera" value={c.user ?? ""} onChange={(v) => setCam(i, { user: v })} placeholder={isNew ? "admin" : "deixe em branco para manter"} />
                    <TextField
                      label="Senha da câmera"
                      type="password"
                      autoComplete="new-password"
                      value={c.password ?? ""}
                      onChange={(v) => setCam(i, { password: v })}
                      placeholder={isNew ? "" : "deixe em branco para manter"}
                      hint="Fica só no servidor (secrets.env); nunca volta para o navegador."
                    />
                  </div>
                  <button className="btn ghost small danger" onClick={() => setRemove(c.id)}>
                    <Icon name="trash" /> Remover câmera
                  </button>
                </div>
              )}
            </li>
          );
        })}
      </ul>
      <button className="btn ghost small" onClick={add}>
        <Icon name="plus" /> Adicionar câmera
      </button>
      {remove && (
        <div className="dialog-backdrop" role="dialog" aria-modal="true" onClick={(e) => e.target === e.currentTarget && setRemove(null)}>
          <div className="dialog">
            <h2>Remover a câmera {remove}?</h2>
            <p className="muted">Ela para de gravar e some do ao vivo quando você salvar. As gravações já feitas ficam no disco até a limpeza normal.</p>
            <div className="dialog-actions">
              <button className="btn ghost" onClick={() => setRemove(null)}>
                Cancelar
              </button>
              <button
                className="btn primary danger-fill"
                onClick={() => {
                  setDraft({ ...draft, cameras: draft.cameras.filter((c) => c.id !== remove) });
                  setRemove(null);
                }}
              >
                Remover
              </button>
            </div>
          </div>
        </div>
      )}
    </EditorCard>
  );
}

// ---------- server and disks ----------

const kinds: Record<string, string> = { recordings: "Gravações", nas: "Nuvem", backup: "Backups" };

function timezones(current: string): string[] {
  const list = typeof Intl.supportedValuesOf === "function" ? Intl.supportedValuesOf("timeZone") : [];
  return list.includes(current) ? list : [current, ...list];
}

export function ServerEditor() {
  const q = useSettings();
  const apply = useApply();
  const { draft, setDraft, dirty, reset } = useDraft(q.data);
  if (!draft) return <EditorUnavailable q={q} />;
  const setVol = (i: number, patch: Partial<SettingsVolume>) => setDraft({ ...draft, volumes: draft.volumes.map((v, j) => (j === i ? { ...v, ...patch } : v)) });
  return (
    <EditorCard title="Configurar servidor e discos" hint="Fuso horário, nomes na rede e discos mostrados" info={q.data} dirty={dirty} onSave={() => apply.mutate(draft)} onReset={reset} busy={apply.isPending}>
      <div className="settings-grid">
        <label className="field">
          <span>Fuso horário</span>
          <select className="input" value={draft.timezone} onChange={(e) => setDraft({ ...draft, timezone: e.target.value })}>
            {timezones(draft.timezone).map((z) => (
              <option key={z} value={z}>
                {z}
              </option>
            ))}
          </select>
        </label>
        <TextField label="Nome do servidor na rede (Nuvem)" value={draft.nas_host} onChange={(v) => setDraft({ ...draft, nas_host: v })} placeholder="SENTINELA" />
        <TextField
          label="Endereço Tailscale"
          value={draft.tailnet_url}
          onChange={(v) => setDraft({ ...draft, tailnet_url: v.trim() })}
          placeholder="https://sentinela.exemplo.ts.net"
          hint="Oferecido ao app no código de pareamento."
        />
      </div>
      <h3 className="settings-sub">Discos</h3>
      <p className="muted small">
        Os discos são montados no servidor (veja o guia de instalação); aqui você escolhe como aparecem no painel e na
        Nuvem. O caminho é o de dentro do container (<code>/data</code>, <code>/srv/nas</code>, <code>/srv/backup</code>).
      </p>
      <ul className="settings-list">
        {draft.volumes.map((v, i) => (
          <li key={i} className="open">
            <div className="settings-body settings-grid">
              <TextField label="Nome" value={v.name} onChange={(x) => setVol(i, { name: x })} />
              <TextField label="Caminho" value={v.path} onChange={(x) => setVol(i, { path: x.trim() })} />
              <label className="field">
                <span>Uso</span>
                <select className="input" value={v.kind} onChange={(e) => setVol(i, { kind: e.target.value })}>
                  {Object.entries(kinds).map(([k, l]) => (
                    <option key={k} value={k}>
                      {l}
                    </option>
                  ))}
                </select>
              </label>
              <TextField label="Pasta de rede (SMB)" value={v.share} onChange={(x) => setVol(i, { share: x })} placeholder="opcional" />
              <button className="btn ghost small danger" onClick={() => setDraft({ ...draft, volumes: draft.volumes.filter((_, j) => j !== i) })}>
                <Icon name="trash" /> Remover da lista
              </button>
            </div>
          </li>
        ))}
      </ul>
      <button
        className="btn ghost small"
        onClick={() => setDraft({ ...draft, volumes: [...draft.volumes, { id: `vol${draft.volumes.length + 1}`, name: "", path: "/srv/", kind: "nas", share: "" }] })}
      >
        <Icon name="plus" /> Adicionar disco
      </button>
    </EditorCard>
  );
}
