import { useCompact } from "../lib/viewport";
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { fetchSystem, type DiskHealth, type Volume } from "../lib/api";
import { fmtBytes } from "../lib/time";
import { Icon } from "../components/Icon";
import { Whale } from "../components/Doodles";
import { FileBrowser } from "../components/FileBrowser";
import { navigate } from "../lib/router";
import { useSession } from "../lib/session";

type OS = "windows" | "mac" | "iphone" | "android" | "linux";

const OS_LABEL: Record<OS, string> = {
  windows: "Windows",
  mac: "Mac",
  iphone: "iPhone e iPad",
  android: "Android",
  linux: "Linux",
};

function guessOS(): OS {
  const ua = navigator.userAgent;
  if (/iPhone|iPad/i.test(ua)) return "iphone";
  if (/Android/i.test(ua)) return "android";
  if (/Mac/i.test(ua)) return "mac";
  if (/Linux/i.test(ua)) return "linux";
  return "windows";
}

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
  const compact = useCompact();
  const folder = params.get("p") ?? "";
  // SMB works only at home; over Tailscale show the server's LAN address.
  const remote = window.location.hostname.endsWith(".ts.net");
  const sys = useQuery({ queryKey: ["system"], queryFn: fetchSystem, refetchInterval: 30_000 });
  const [os, setOs] = useState<OS>(guessOS);
  const nas = (sys.data?.volumes ?? []).filter((v) => v.kind === "nas");
  const total = nas.reduce((a, v) => a + v.total, 0);
  const free = nas.reduce((a, v) => a + v.free, 0);
  const ip = remote ? "192.168.1.10" : window.location.hostname;
  const name = sys.data?.nas_host || "SENTINELA";

  const steps: Record<OS, React.ReactNode> = {
    windows: (
      <>
        <p>Abra o Explorador de Arquivos, clique na barra de endereço e cole:</p>
        <CopyField label="endereço Windows" value={`\\\\${ip}\\Nuvem`} />
        <p className="muted">
          Para deixar fixo como uma unidade (por exemplo Z:), clique com o botão direito em “Este Computador”, escolha “Mapear unidade
          de rede” e use o mesmo endereço. O servidor também aparece em “Rede” como {name}.
        </p>
      </>
    ),
    mac: (
      <>
        <p>No Finder, aperte ⌘K e cole:</p>
        <CopyField label="endereço Mac" value={`smb://${ip}/Nuvem`} />
        <p className="muted">Marque “Lembrar esta senha nas Chaves” para conectar direto das próximas vezes.</p>
      </>
    ),
    iphone: (
      <>
        <p>
          No app Arquivos, toque em ⋯ no canto de cima, depois em “Conectar ao Servidor”, e use:
        </p>
        <CopyField label="endereço iPhone" value={`smb://${ip}`} />
        <p className="muted">Escolha “Usuário registrado” e entre com o seu usuário da Nuvem.</p>
      </>
    ),
    android: (
      <>
        <p>O Android não abre pastas de rede sozinho. Instale um gerenciador com SMB (Material Files, que é gratuito e sem anúncios) e adicione:</p>
        <CopyField label="servidor Android" value={ip} />
        <p className="muted">Tipo: SMB. Compartilhamento: Nuvem.</p>
      </>
    ),
    linux: (
      <>
        <p>No gerenciador de arquivos, em “Outros locais”, conecte a:</p>
        <CopyField label="endereço Linux" value={`smb://${ip}/Nuvem`} />
      </>
    ),
  };

  const [connect, setConnect] = useState(false);
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
        onConnect={compact ? undefined : () => setConnect(true)}
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

      {connect && (
        <div className="dialog-backdrop" role="dialog" aria-modal="true" aria-labelledby="smb-title" onMouseDown={(e) => e.target === e.currentTarget && setConnect(false)}>
          <div className="dialog wide connect-dialog">
            <div className="dialog-head">
              <div>
                <h2 id="smb-title">Acessar pelo computador</h2>
                <p className="muted small">Pastas de rede (SMB): funcionam só na rede de casa.</p>
              </div>
              <button className="btn ghost icon small" aria-label="Fechar" onClick={() => setConnect(false)}>
                <Icon name="x" />
              </button>
            </div>
            <div className="segmented wrap" role="tablist" aria-label="Sistema do aparelho">
              {(Object.keys(OS_LABEL) as OS[]).map((k) => (
                <button key={k} role="tab" aria-selected={os === k} aria-pressed={os === k} onClick={() => setOs(k)}>
                  {OS_LABEL[k]}
                </button>
              ))}
            </div>
            <div className="stack-sm connect-steps">{steps[os]}</div>
            <p className="muted small">
              Usuário: <strong>ana</strong>. A senha da Nuvem é definida no servidor com <code>sudo smbpasswd -a ana</code>.
            </p>
            <div className="stack-sm">
              <h3 className="card-sub">Gravações pela rede (somente leitura)</h3>
              <CopyField label="gravações" value={os === "windows" ? `\\\\${ip}\\Gravacoes` : `smb://${ip}/Gravacoes`} />
            </div>
            <ul className="tips">
              <li>Use cabo no computador: a Wi-Fi costuma ser o gargalo, não o servidor.</li>
              <li>A Nuvem não é backup. Guarde uma segunda cópia do que for importante.</li>
              <li>Fora de casa, use esta página pelo app ou pelo endereço público: as pastas de rede funcionam só em casa.</li>
              <li>Apagar aqui ou pela rede manda para a Lixeira, que guarda tudo por 30 dias.</li>
            </ul>
          </div>
        </div>
      )}
    </div>
  );
}
