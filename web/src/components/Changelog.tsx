import { useMemo, useState, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import changelog from "../../../CHANGELOG.md?raw";
import { api, get, type SystemInfo } from "../lib/api";
import { useToast } from "../lib/session";
import { isPublicZone } from "../lib/access";
import { Icon } from "./Icon";

/** One version of CHANGELOG.md: its title and its "### group" lists. */
export interface ChangelogVersion {
  title: string;
  intro: string[];
  groups: { title: string; items: string[] }[];
}

/** Parses the Keep a Changelog subset the project uses: "## [version]",
 *  "### group", "- item" (continuation lines indented), plain paragraphs. */
export function parseChangelog(md: string): ChangelogVersion[] {
  const out: ChangelogVersion[] = [];
  let v: ChangelogVersion | null = null;
  let g: ChangelogVersion["groups"][number] | null = null;
  for (const raw of md.split(/\r?\n/)) {
    const line = raw.trimEnd();
    if (line.startsWith("## ")) {
      v = { title: line.slice(3).replace(/^\[|\]/g, "").trim(), intro: [], groups: [] };
      out.push(v);
      g = null;
    } else if (!v || line.startsWith("[") || line.startsWith("# ")) {
      continue;
    } else if (line.startsWith("### ")) {
      g = { title: line.slice(4).trim(), items: [] };
      v.groups.push(g);
    } else if (line.startsWith("- ")) {
      if (!g) {
        g = { title: "", items: [] };
        v.groups.push(g);
      }
      g.items.push(line.slice(2).trim());
    } else if (/^\s+\S/.test(line) && g && g.items.length) {
      g.items[g.items.length - 1] += " " + line.trim();
    } else if (line.trim() && !g) {
      v.intro.push(line.trim());
    }
  }
  return out;
}

/** `code` spans only; everything else stays plain text (no HTML injection). */
function inline(text: string): ReactNode[] {
  return text.split(/(`[^`]+`)/).map((part, i) =>
    part.startsWith("`") && part.endsWith("`") ? <code key={i}>{part.slice(1, -1)}</code> : part,
  );
}

const versionLabel = (t: string) => (t.toLowerCase() === "unreleased" ? "Próxima versão" : t);

/** "Novidades": the changelog shipped with this build. */
export function ChangelogCard() {
  const versions = useMemo(() => parseChangelog(changelog), []);
  const [open, setOpen] = useState(1);
  return (
    <section className="card" aria-label="Novidades">
      <div className="card-head">
        <h2>Novidades</h2>
        <span className="muted small">O que mudou em cada versão</span>
      </div>
      {versions.slice(0, open).map((v) => (
        <article key={v.title} className="changelog-version">
          <h3>{versionLabel(v.title)}</h3>
          {v.intro.map((p, i) => (
            <p key={i} className="muted small">
              {inline(p)}
            </p>
          ))}
          {v.groups.map((g, i) => (
            <div key={i} className="changelog-group">
              {g.title && <h4>{g.title}</h4>}
              <ul>
                {g.items.map((it, j) => (
                  <li key={j}>{inline(it)}</li>
                ))}
              </ul>
            </div>
          ))}
        </article>
      ))}
      {open < versions.length && (
        <button className="btn ghost small" onClick={() => setOpen(versions.length)}>
          Ver versões anteriores
        </button>
      )}
    </section>
  );
}

interface UpdateLast {
  checked: string;
  state: "current" | "updated" | "updating" | "manual" | "major" | "failed" | "error";
  current: string;
  latest: string;
  message: string;
}
interface UpdateSettings {
  enabled: boolean;
  at: string;
  major: boolean;
  replace_manual: boolean;
}
interface UpdatesInfo {
  version: string;
  settings: UpdateSettings;
  last: UpdateLast | null;
  host: boolean;
}

const stateText: Record<UpdateLast["state"], string> = {
  current: "Em dia",
  updated: "Atualizado",
  updating: "Atualizando agora",
  manual: "Versão instalada a partir de um computador: só é trocada se você permitir abaixo",
  major: "Há uma versão nova com mudanças grandes: leia as notas e permita versões maiores para instalar",
  failed: "A última atualização falhou e foi desfeita",
  error: "Não foi possível verificar",
};

const hours = Array.from({ length: 24 }, (_, h) => `${String(h).padStart(2, "0")}:00`);

/** Server version and automatic updates (ADR-0023). Admins switch them on,
 *  pick the hour and can check right away; the host does the work. */
export function UpdatesCard({ s, admin }: { s: SystemInfo; admin: boolean }) {
  const toast = useToast();
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["updates"], queryFn: () => get<UpdatesInfo>("/api/updates"), enabled: admin, refetchInterval: 30_000 });
  const save = useMutation({
    mutationFn: (next: UpdateSettings) => api<UpdateSettings>("PUT", "/api/updates", next),
    onSuccess: () => {
      toast("Preferências de atualização salvas");
      void qc.invalidateQueries({ queryKey: ["updates"] });
    },
    onError: (e: Error) => toast(`Atualizações: ${e.message}`, true),
  });
  const check = useMutation({
    mutationFn: () => api("POST", "/api/updates/check"),
    onSuccess: () => toast("Verificando agora; a página mostra o resultado em instantes"),
    onError: (e: Error) => toast(`Atualizações: ${e.message}`, true),
  });
  const info = q.data;
  const st = info?.settings;
  const last = (info?.last ?? (s.update?.last as UpdateLast | null | undefined)) || null;
  const set = (patch: Partial<UpdateSettings>) => st && save.mutate({ ...st, ...patch });
  const outside = isPublicZone();

  return (
    <section className="card" aria-label="Atualizações">
      <div className="card-head">
        <h2>Atualizações</h2>
        <span className="pill">Versão {s.version}</span>
      </div>
      {admin && st && (
        <>
          <div className="toggle-row">
            <span>
              <Icon name="refresh" /> Atualizar automaticamente
            </span>
            <button
              className={`switch${st.enabled ? " on" : ""}`}
              role="switch"
              aria-checked={st.enabled}
              aria-label="Atualizar automaticamente"
              disabled={save.isPending || outside}
              onClick={() => set({ enabled: !st.enabled })}
            />
          </div>
          <div className="row-gap" style={{ marginTop: 8 }}>
            <label className="field">
              <span>Horário (o de menos uso da casa)</span>
              <select className="input" value={st.at} disabled={save.isPending || outside} onChange={(e) => set({ at: e.target.value })}>
                {hours.map((h) => (
                  <option key={h} value={h}>
                    {h}
                  </option>
                ))}
              </select>
            </label>
          </div>
          <label className="check-row">
            <input type="checkbox" checked={st.major} disabled={save.isPending || outside} onChange={(e) => set({ major: e.target.checked })} />
            Instalar também versões maiores (podem mudar configurações)
          </label>
          <label className="check-row">
            <input
              type="checkbox"
              checked={st.replace_manual}
              disabled={save.isPending || outside}
              onChange={(e) => set({ replace_manual: e.target.checked })}
            />
            Substituir uma versão instalada a partir de um computador
          </label>
          <div className="row-gap" style={{ marginTop: 10 }}>
            <button className="btn small" disabled={check.isPending || outside || !info?.host} onClick={() => check.mutate()}>
              <Icon name="refresh" /> Verificar agora
            </button>
          </div>
          {outside && <p className="muted small">Mude isto em casa ou pelo Tailscale.</p>}
          {info && !info.host && (
            <p className="notice">
              Este servidor ainda não tem o serviço de atualização. Rode uma vez{" "}
              <code>sudo /opt/sentinela/scripts/setup-auto-update.sh install</code> (ou faça um deploy).
            </p>
          )}
        </>
      )}
      {last && (
        <dl className="setup-facts">
          <dt>Última verificação</dt>
          <dd>{new Date(last.checked).toLocaleString()}</dd>
          <dt>Situação</dt>
          <dd>
            {stateText[last.state] ?? last.state}
            {last.message && last.state !== "manual" && last.state !== "major" ? ` (${last.message})` : ""}
          </dd>
          {last.latest && (
            <>
              <dt>Última versão publicada</dt>
              <dd>{last.latest}</dd>
            </>
          )}
        </dl>
      )}
      <p className="muted small">
        O servidor só baixa versões prontas (nada é compilado nele), confere as somas SHA-256, reinicia em segundos e
        volta à versão anterior se algo falhar.
      </p>
    </section>
  );
}
