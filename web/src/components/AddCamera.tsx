import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { api } from "../lib/api";
import { useToast } from "../lib/session";
import { CopyField } from "../pages/NuvemPage";
import { PasswordField } from "./PasswordField";
import { Icon } from "./Icon";
import { addCameraCommand } from "../lib/camera";

interface Found {
  host: string;
  port: number;
  name?: string;
  hardware?: string;
  known?: string;
}

interface Session {
  stream: "main" | "sub";
  ok: boolean;
  video?: string;
  audio?: string;
  error?: string;
}

interface TestResult {
  suggested_id: string;
  known?: string;
  onvif?: { manufacturer: string; model: string; firmware: string };
  onvif_error?: string;
  model?: string;
  dvrip_error?: string;
  control_approved: boolean;
  sessions: Session[];
  sessions_ok: boolean;
}

/** Find and test a second camera (read-only); adding it happens on the server. */
export function AddCameraCard() {
  const toast = useToast();
  const [host, setHost] = useState("");
  const [port, setPort] = useState(8899);
  const [user, setUser] = useState("admin");
  const [pw, setPw] = useState("");
  const [name, setName] = useState("");
  const search = useMutation({
    mutationFn: () => api<Found[]>("POST", "/api/cameras/discover"),
    onError: (e: Error) => toast(e.message, true),
  });
  const test = useMutation({
    mutationFn: () => api<TestResult>("POST", "/api/cameras/test", { host, onvif_port: port, user, password: pw }),
    onError: (e: Error) => toast(e.message, true),
  });
  const r = test.data;

  return (
    <section className="card span-all">
      <h2>Adicionar câmera</h2>
      <p className="muted">
        Procure câmeras XMEye na rede e teste o acesso. Nada é gravado na câmera. Se o teste passar, o comando no fim adiciona a câmera no servidor.
      </p>
      <div className="dialog-actions" style={{ justifyContent: "flex-start", margin: "8px 0" }}>
        <button className="btn" disabled={search.isPending} onClick={() => search.mutate()}>
          <Icon name="refresh" /> {search.isPending ? "Procurando…" : "Procurar na rede"}
        </button>
      </div>
      {search.data && search.data.length === 0 && <p className="muted">Nenhuma câmera respondeu. Digite o IP abaixo.</p>}
      {search.data?.map((d) => (
        <button
          key={`${d.host}:${d.port}`}
          className="list-row"
          disabled={!!d.known}
          onClick={() => {
            setHost(d.host);
            setPort(d.port);
            if (d.name) setName(d.name);
          }}
        >
          <span className="when">{d.host}</span>
          <span>{[d.name, d.hardware].filter(Boolean).join(" · ") || "Câmera ONVIF"}</span>
          {d.known && <span className="tag">já é {d.known}</span>}
        </button>
      ))}
      <form
        className="inline-form"
        onSubmit={(e) => {
          e.preventDefault();
          test.mutate();
        }}
      >
        <input className="input" placeholder="IP (ex.: 192.168.1.21)" aria-label="IP da câmera" value={host} onChange={(e) => setHost(e.target.value.trim())} required />
        <input className="input" type="number" aria-label="Porta ONVIF" title="Porta ONVIF" value={port} onChange={(e) => setPort(Number(e.target.value) || 8899)} style={{ maxWidth: 100 }} />
        <input className="input" placeholder="Nome (ex.: Fundo)" aria-label="Nome da câmera" value={name} onChange={(e) => setName(e.target.value)} maxLength={40} />
        <input className="input" placeholder="Usuário" aria-label="Usuário da câmera" value={user} onChange={(e) => setUser(e.target.value)} required autoComplete="off" />
        <PasswordField label="Senha da câmera" hideLabel value={pw} onChange={setPw} autoComplete="new-password" />
        <button className="btn primary" disabled={test.isPending || !host || !pw}>
          {test.isPending ? "Testando (até 30 s)…" : "Testar"}
        </button>
      </form>
      {r && (
        <div className="stack" style={{ marginTop: 12 }}>
          {r.known && <p className="notice">Esta câmera já está cadastrada como {r.known}.</p>}
          <ul className="check-list">
            <li className={r.onvif ? "ok" : "fail"}>
              Identificação (ONVIF): {r.onvif ? `${r.onvif.model}, firmware ${r.onvif.firmware}` : r.onvif_error}
            </li>
            <li className={r.model ? "ok" : "fail"}>
              Senha e controle (DVRIP): {r.model ? `${r.model}${r.control_approved ? ", PTZ e luz liberados" : ", modelo não verificado: só vídeo"}` : r.dvrip_error}
            </li>
            {r.sessions.map((s, i) => (
              <li key={i} className={s.ok ? "ok" : "fail"}>
                Conexão {i + 1} ({s.stream === "main" ? "HD" : "SD"}): {s.ok ? [s.video, s.audio].filter(Boolean).join(" + ") : s.error}
              </li>
            ))}
          </ul>
          {r.sessions_ok && !r.known ? (
            <>
              <p className="muted">A câmera aguenta as 3 conexões (gravação, ao vivo HD e SD). No servidor, rode (a senha é pedida lá, sem aparecer):</p>
              <CopyField label="comando" value={addCameraCommand(r.suggested_id, host, name, port)} />
            </>
          ) : (
            !r.sessions_ok && <p className="notice error">A câmera não abriu as 3 conexões ao mesmo tempo. Feche outros apps (VLC, XMEye) ligados a ela e teste de novo.</p>
          )}
        </div>
      )}
    </section>
  );
}
