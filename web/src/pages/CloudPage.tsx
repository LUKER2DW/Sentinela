import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { fetchSystem, type DiskHealth, type Volume } from "../lib/api";
import { fmtBytes } from "../lib/time";
import { Icon } from "../components/Icon";
import { Whale } from "../components/Doodles";
import { FileBrowser } from "../components/FileBrowser";
import { navigate } from "../lib/router";
import { useSession, useToast } from "../lib/session";

import { t } from "../lib/i18n";
import { copyText } from "../lib/clipboard";
export function CopyField({ value, label }: { value: string; label: string }) {
  const [done, setDone] = useState(false);
  const toast = useToast();
  return (
    <div className="copy-field">
      <code aria-label={label}>{value}</code>
      <button
        className="btn ghost icon small"
        title={t("Copy")}
        aria-label={t("Copy {label}", { label })}
        onClick={() => {
          void copyText(value).then((ok) => {
            if (!ok) return toast(t("Could not copy."), true);
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

const healthLabel = { ok: t("Healthy"), warn: t("Attention"), fail: t("Failing") } as const;

function healthTitle(h: DiskHealth) {
  return t("{model} ({device}): {reallocated} sectors reallocated, {pending} pending, {uncorrectable} read errors{v5}. Last test: {v6}.", { model: h.model, device: h.device, reallocated: h.reallocated, pending: h.pending, uncorrectable: h.uncorrectable, v5: h.temperature != null ? `, ${h.temperature} °C` : "", v6: h.last_selftest || "nenhum" });
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
        <span className="muted num">{v.mounted ? t("{v0} free of {v1}", { v0: fmtBytes(v.free), v1: fmtBytes(v.total) }) : t("not mounted")}</span>
      </div>
      <div className="meter" role="meter" aria-label={t("Usage of {name}", { name: v.name })} aria-valuenow={Math.round(v.pct)} aria-valuemin={0} aria-valuemax={100}>
        <span style={{ width: `${v.mounted ? v.pct : 0}%` }} />
      </div>
    </div>
  );
}

export function CloudPage({ params }: { params: URLSearchParams }) {
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
    <div className="cloud-page">
      <header className="page-head">
        <div className="title">
          <h1>{t("Cloud")}</h1>
          <span className="sub">{t("Your files at home and on the phone, without a third-party cloud.")}</span>
        </div>
        <span className="pill">
          <span className={`dot ${nas.every((v) => v.mounted) && nas.length ? "ok" : "warn"}`} />
          {nas.length && nas.every((v) => v.mounted) ? t("Sharing on the network") : t("Disks not found")}
        </span>
      </header>

      <FileBrowser
        path={folder}
        canWrite={can("operator")}
        onNavigate={(p) => navigate("cloud", p ? { p } : undefined, { push: true })}
        summary={
          total > 0 ? (
            <>
              <div className="space-line">
                <Whale className="space-whale" />
                <div>
                  <strong className="num">{t("{size} free", { size: fmtBytes(free) })}</strong>
                  <span className="muted small">
                    {" "}
                    {t("of {total} · {used} used", { total: fmtBytes(total), used: fmtBytes(used) })}
                  </span>
                  {health && (
                    <span className={`health ${health.state}`} title={healthTitle(health)}>
                      {healthLabel[health.state]}
                    </span>
                  )}
                </div>
              </div>
              <div className="meter" role="meter" aria-label={t("Cloud usage")} aria-valuenow={Math.round(pct)} aria-valuemin={0} aria-valuemax={100}>
                <span style={{ width: `${pct}%` }} />
              </div>
            </>
          ) : (
            <p className="muted small">{t("The space shows up when Sentinela runs on the server.")}</p>
          )
        }
      />

    </div>
  );
}
