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

const STEPS = ["Encontrar", "Acesso", "Teste e instalação"];

/** Find and test a camera (read-only); adding it happens on the server. */
export function AddCameraCard() {
  const toast = useToast();
  const [step, setStep] = useState(0);
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
    onSuccess: () => setStep(2),
    onError: (e: Error) => toast(e.message, true),
  });
  const r = test.data;
  const validHost = /^[\w.-]+$/.test(host);

  return (
    <section className="card add-cam">
      <div className="card-head">
        <h2>Procurar e testar câmeras na rede</h2>
        <span className="muted small">Nada é gravado na câmera: é só leitura. Para adicionar, use o editor acima.</span>
      </div>
      <ol className="stepper" aria-label="Etapas">
        {STEPS.map((s, i) => (
          <li key={s} className={i === step ? "on" : i < step ? "done" : ""}>
            <button type="button" disabled={i > step || test.isPending} onClick={() => setStep(i)}>
              <span className="step-n">{i < step ? <Icon name="check" /> : i + 1}</span>
              {s}
            </button>
          </li>
        ))}
      </ol>

      {step === 0 && (
        <div className="step-body">
          <p className="muted">Procure câmeras XMEye/ONVIF ligadas na mesma rede do servidor, ou digite o IP se já souber.</p>
          <div className="row-gap">
            <button className="btn primary" disabled={search.isPending} onClick={() => search.mutate()}>
              {search.isPending ? <span className="spinner small" /> : <Icon name="search" />} {search.isPending ? "Procurando (até 5 s)…" : "Procurar na rede"}
            </button>
            <button className="btn" onClick={() => setStep(1)}>
              <Icon name="edit" /> Digitar o IP
            </button>
          </div>
          {search.data && search.data.length === 0 && <p className="notice">Nenhuma câmera respondeu. Confira se ela está ligada e na mesma rede, ou digite o IP.</p>}
          {search.data && search.data.length > 0 && (
            <ul className="found-list">
              {search.data.map((d) => (
                <li key={`${d.host}:${d.port}`}>
                  <button
                    className="found-cam"
                    disabled={!!d.known}
                    onClick={() => {
                      setHost(d.host);
                      setPort(d.port);
                      if (d.name) setName(d.name);
                      setStep(1);
                    }}
                  >
                    <span className="found-ico">
                      <Icon name="video" />
                    </span>
                    <span className="found-text">
                      <strong>{d.name || "Câmera ONVIF"}</strong>
                      <span className="muted small">
                        {d.host}:{d.port}
                        {d.hardware ? ` · ${d.hardware}` : ""}
                      </span>
                    </span>
                    {d.known ? <span className="pill">já cadastrada ({d.known})</span> : <Icon name="chevronRight" />}
                  </button>
                </li>
              ))}
            </ul>
          )}
        </div>
      )}

      {step === 1 && (
        <form
          className="step-body"
          onSubmit={(e) => {
            e.preventDefault();
            if (validHost && pw) test.mutate();
          }}
        >
          <div className="form-grid">
            <div className="field">
              <label htmlFor="ac-host">Endereço IP</label>
              <input id="ac-host" className="input" placeholder="192.168.1.21" value={host} onChange={(e) => setHost(e.target.value.trim())} required autoFocus />
            </div>
            <div className="field">
              <label htmlFor="ac-port">Porta ONVIF</label>
              <input id="ac-port" className="input" type="number" value={port} onChange={(e) => setPort(Number(e.target.value) || 8899)} />
              <span className="hint">As XMEye usam 8899.</span>
            </div>
            <div className="field">
              <label htmlFor="ac-name">Nome</label>
              <input id="ac-name" className="input" placeholder="ex.: Fundo, Garagem" value={name} onChange={(e) => setName(e.target.value)} maxLength={40} />
            </div>
            <div className="field">
              <label htmlFor="ac-user">Usuário da câmera</label>
              <input id="ac-user" className="input" value={user} onChange={(e) => setUser(e.target.value)} required autoComplete="off" />
            </div>
            <PasswordField label="Senha da câmera" value={pw} onChange={setPw} autoComplete="new-password" />
          </div>
          <p className="muted small">A senha só é usada para este teste; no servidor ela é pedida de novo pelo script e nunca aparece na tela.</p>
          <div className="row-gap">
            <button type="button" className="btn ghost" onClick={() => setStep(0)}>
              Voltar
            </button>
            <button className="btn primary" disabled={test.isPending || !validHost || !pw}>
              {test.isPending ? <span className="spinner small" /> : <Icon name="scan" />} {test.isPending ? "Testando (até 30 s)…" : "Testar a câmera"}
            </button>
          </div>
        </form>
      )}

      {step === 2 && r && (
        <div className="step-body">
          {r.known && <p className="notice">Esta câmera já está cadastrada como {r.known}.</p>}
          <ul className="check-list big">
            <li className={r.onvif ? "ok" : "fail"}>
              <strong>Identificação (ONVIF)</strong>
              <span className="muted small">{r.onvif ? `${r.onvif.manufacturer} ${r.onvif.model}, firmware ${r.onvif.firmware}` : r.onvif_error}</span>
            </li>
            <li className={r.model ? "ok" : "fail"}>
              <strong>Senha e controle (DVRIP)</strong>
              <span className="muted small">{r.model ? `${r.model}${r.control_approved ? ": PTZ e luz liberados" : ": modelo não verificado, só vídeo"}` : r.dvrip_error}</span>
            </li>
            {r.sessions.map((s, i) => (
              <li key={i} className={s.ok ? "ok" : "fail"}>
                <strong>
                  Conexão {i + 1} ({s.stream === "main" ? "HD" : "SD"})
                </strong>
                <span className="muted small">{s.ok ? [s.video, s.audio].filter(Boolean).join(" + ") : s.error}</span>
              </li>
            ))}
          </ul>
          {r.sessions_ok && !r.known ? (
            <div className="install-box">
              <strong>Tudo certo. Para adicionar, rode no servidor:</strong>
              <CopyField label="comando" value={addCameraCommand(r.suggested_id, host, name, port)} />
              <span className="muted small">A senha é pedida lá, sem aparecer. Depois disso a câmera aparece em Ao vivo e começa a gravar.</span>
            </div>
          ) : (
            !r.sessions_ok && <p className="notice error">A câmera não abriu as 3 conexões ao mesmo tempo. Feche outros apps (VLC, XMEye) ligados a ela e teste de novo.</p>
          )}
          <div className="row-gap">
            <button className="btn ghost" onClick={() => setStep(1)}>
              Voltar
            </button>
            <button className="btn" disabled={test.isPending} onClick={() => test.mutate()}>
              <Icon name="refresh" /> Testar de novo
            </button>
          </div>
        </div>
      )}
    </section>
  );
}
