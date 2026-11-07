import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { fetchSystem, type DiskHealth, type Volume } from "../lib/api";
import { fmtBytes } from "../lib/time";
import { Icon } from "../components/Icon";
import { Whale } from "../components/Doodles";
import { FileBrowser } from "../components/FileBrowser";
import { navigate } from "../lib/router";
import { useSession } from "../lib/session";

export function CopyField({ value, label }: { value: string; label: string }) {
  const [done, setDone] = useState(false);
  return (
    <div className="copy-field">
      <code aria-label={label}>{value}</code>
      <button
        className="btn ghost icon small"
        title="Copiar"
        aria-label={`Copiar ${label}`}
        onClick={() => {
          void navigator.clipboard?.writeText(value).then(() => {
            setDone(true);
            window.setTimeout(() => setDone(false), 1500);
          });
        }}
      >
        <Icon name={done ? "check" : "copy"} />
      </button>
    </div>
  );
}

const healthLabel = { ok: "Saudável", warn: "Atenção", fail: "Falhando" } as const;

function healthTitle(h: DiskHealth) {
  return `${h.model} (${h.device}): ${h.reallocated} setores realocados, ${h.pending} pendentes, ${h.uncorrectable} leituras com erro${
    h.temperature != null ? `, ${h.temperature} °C` : ""
  }. Último teste: ${h.last_selftest || "nenhum"}.`;
}

export function VolumeMeter({ v, health }: { v: Volume; health?: DiskHealth }) {
  return (
    <div className="vol">
      <div className="vol-head">
        <span className="vol-name">
          <Icon name="disk" /> {v.name}
          {health && (
            <span className={`health ${health.state}`} title={healthTitle(health)}>
              {healthLabel[health.state]}
            </span>
          )}
        </span>
        <span className="muted num">{v.mounted ? `${fmtBytes(v.free)} livres de ${fmtBytes(v.total)}` : "não montado"}</span>
      </div>
      <div className="meter" role="meter" aria-label={`Uso de ${v.name}`} aria-valuenow={Math.round(v.pct)} aria-valuemin={0} aria-valuemax={100}>
        <span style={{ width: `${v.mounted ? v.pct : 0}%` }} />
      </div>
    </div>
  );
}

export function NuvemPage({ params }: { params: URLSearchParams }) {
  const { can } = useSession();
  const folder = params.get("p") ?? "";
  const sys = useQuery({ queryKey: ["system"], queryFn: fetchSystem, refetchInterval: 30_000 });
  const nas = (sys.data?.volumes ?? []).filter((v) => v.kind === "nas");
  const total = nas.reduce((a, v) => a + v.total, 0);
  const free = nas.reduce((a, v) => a + v.free, 0);
  const health = sys.data?.disk_health?.mounts["/srv/nas"];
  const used = total - free;
  const pct = total > 0 ? (100 * used) / total : 0;

  return (
    <div className="nuvem-page">
      <header className="page-head">
        <div className="title">
          <h1>Nuvem</h1>
          <span className="sub">Seus arquivos em casa e no celular, sem nuvem de terceiros.</span>
        </div>
        <span className="pill">
          <span className={`dot ${nas.every((v) => v.mounted) && nas.length ? "ok" : "warn"}`} />
          {nas.length && nas.every((v) => v.mounted) ? "Compartilhando na rede" : "Discos não encontrados"}
        </span>
      </header>

      <FileBrowser
        path={folder}
        canWrite={can("operator")}
        onNavigate={(p) => navigate("nuvem", p ? { p } : undefined, { push: true })}
        summary={
          total > 0 ? (
            <>
              <div className="space-line">
                <Whale className="space-whale" />
                <div>
                  <strong className="num">{fmtBytes(free)} livres</strong>
                  <span className="muted small">
                    {" "}
                    de {fmtBytes(total)} · {fmtBytes(used)} usados
                  </span>
                  {health && (
                    <span className={`health ${health.state}`} title={healthTitle(health)}>
                      {healthLabel[health.state]}
                    </span>
                  )}
                </div>
              </div>
              <div className="meter" role="meter" aria-label="Uso da Nuvem" aria-valuenow={Math.round(pct)} aria-valuemin={0} aria-valuemax={100}>
                <span style={{ width: `${pct}%` }} />
              </div>
            </>
          ) : (
            <p className="muted small">O espaço aparece quando o Sentinela roda no servidor.</p>
          )
        }
      />

    </div>
  );
}
