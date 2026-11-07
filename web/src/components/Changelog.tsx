import { useMemo, useState, type ReactNode } from "react";
import changelog from "../../../CHANGELOG.md?raw";
import type { SystemInfo } from "../lib/api";
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
interface UpdateSchedule {
  enabled: boolean;
  at: string;
  repo: string;
}

const stateText: Record<UpdateLast["state"], string> = {
  current: "Em dia",
  updated: "Atualizado",
  updating: "Atualizando agora",
  manual: "Versão instalada a partir de um computador: não é trocada automaticamente",
  major: "Há uma versão nova com mudanças grandes: atualize manualmente depois de ler as notas",
  failed: "A última atualização falhou e foi desfeita",
  error: "Não foi possível verificar",
};

/** Server version and the automatic-update schedule (ADR-0023). */
export function UpdatesCard({ s }: { s: SystemInfo }) {
  const sched = s.update?.schedule as UpdateSchedule | null | undefined;
  const last = s.update?.last as UpdateLast | null | undefined;
  return (
    <section className="card" aria-label="Atualizações">
      <div className="card-head">
        <h2>Atualizações</h2>
        <span className="pill">Versão {s.version}</span>
      </div>
      <p className={`setup-status${sched?.enabled ? " ok" : ""}`}>
        <Icon name={sched?.enabled ? "check" : "info"} />{" "}
        {sched?.enabled
          ? `Automáticas: todo dia por volta das ${sched.at}, a partir das versões publicadas.`
          : "Automáticas desligadas."}
      </p>
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
        {sched?.enabled ? "Para desligar" : "Para ligar"}, no servidor:{" "}
        <code>sudo /opt/sentinela/scripts/setup-auto-update.sh {sched?.enabled ? "disable" : "enable 04:00"}</code>. O
        servidor só baixa versões prontas (nada é compilado nele), confere as somas SHA-256 e volta à versão anterior se
        algo falhar.
      </p>
    </section>
  );
}
