import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, get, type User } from "../lib/api";
import { useToast } from "../lib/session";
import { canChangeConnection, changeConnection, isPublicZone, zoneLabel } from "../lib/access";
import { PasswordField } from "./PasswordField";

interface TotpSetup {
  secret: string;
  uri: string;
  qr_svg: string;
}

/**
 * How this device is connected, and two-step login (ADR-0019): required to
 * sign in from outside (Funnel or the home's domain), enrolled only at home
 * or over Tailscale.
 */
export function SecurityCard() {
  const qc = useQueryClient();
  const toast = useToast();
  const me = useQuery({ queryKey: ["me"], queryFn: () => get<{ user: User }>("/api/me") });
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [setup, setSetup] = useState<TotpSetup | null>(null);
  const [off, setOff] = useState(false);
  const outside = isPublicZone();
  const on = !!me.data?.user.totp;

  const done = (text: string) => {
    toast(text);
    setSetup(null);
    setOff(false);
    setPassword("");
    setCode("");
    void qc.invalidateQueries({ queryKey: ["me"] });
    void qc.invalidateQueries({ queryKey: ["my-sessions"] });
  };
  const start = useMutation({
    mutationFn: () => api<TotpSetup>("POST", "/api/me/totp/setup", { password }),
    onSuccess: (r) => {
      setSetup(r);
      setPassword("");
    },
    onError: (e: Error) => toast(e.message.includes("password") ? "Senha incorreta." : e.message, true),
  });
  const enable = useMutation({
    mutationFn: () => api("POST", "/api/me/totp/enable", { code: code.replace(/\s/g, "") }),
    onSuccess: () => done("Verificação em 2 etapas ativada."),
    onError: (e: Error) => toast(e.message.includes("invalid code") ? "Código incorreto. Confira o relógio do celular e tente o próximo." : e.message, true),
  });
  const disable = useMutation({
    mutationFn: () => api("POST", "/api/me/totp/disable", { password }),
    onSuccess: () => done("Verificação em 2 etapas desativada. Os acessos de fora foram encerrados."),
    onError: (e: Error) => toast(e.message.includes("password") ? "Senha incorreta." : e.message, true),
  });

  return (
    <section className="card" aria-label="Segurança e conexão">
      <h2>Segurança</h2>
      <div className="rows">
        <div className="status-row">
          <div>
            <strong>Conectado por</strong>
            <p className="muted small">{zoneLabel()}</p>
          </div>
          {canChangeConnection() && (
            <button className="btn small ghost" onClick={changeConnection}>
              Trocar
            </button>
          )}
        </div>
        <div className="status-row">
          <div>
            <strong>Verificação em 2 etapas</strong>
            <p className="muted small">
              {on ? "Ativa: fora de casa, o login pede o código do app autenticador." : "Desligada: sem ela, esta conta não entra de fora de casa."}
            </p>
          </div>
          <span className={`pill${on ? " ok" : ""}`}>{on ? "Ativa" : "Desligada"}</span>
        </div>
      </div>

      {outside ? (
        <p className="muted small card-foot">Ativar ou desligar a verificação só pode ser feito em casa ou pelo Tailscale.</p>
      ) : setup ? (
        <div className="stack-sm card-foot totp-setup">
          <p className="small">
            1. No app autenticador (Google Authenticator, Microsoft Authenticator, Aegis…), adicione uma conta lendo este QR code.
          </p>
          <img className="totp-qr" alt="QR code da verificação em 2 etapas" src={`data:image/svg+xml;utf8,${encodeURIComponent(setup.qr_svg)}`} />
          <p className="muted small">
            No próprio celular? <a href={setup.uri}>Abrir no app autenticador</a> ou digite a chave: <code className="totp-secret">{setup.secret.replace(/(.{4})/g, "$1 ").trim()}</code>
          </p>
          <div className="field">
            <label htmlFor="totp-code">2. Digite o código de 6 dígitos que aparece</label>
            <input
              id="totp-code"
              className="input otp"
              inputMode="numeric"
              autoComplete="one-time-code"
              maxLength={7}
              placeholder="000 000"
              value={code}
              onChange={(e) => setCode(e.target.value)}
            />
          </div>
          <div className="row-gap">
            <button className="btn primary" disabled={enable.isPending || code.replace(/\s/g, "").length !== 6} onClick={() => enable.mutate()}>
              Ativar
            </button>
            <button className="btn ghost" onClick={() => setSetup(null)}>
              Cancelar
            </button>
          </div>
        </div>
      ) : (
        <form
          className="stack-sm card-foot"
          onSubmit={(e) => {
            e.preventDefault();
            if (on) disable.mutate();
            else start.mutate();
          }}
        >
          {(!on || off) && <PasswordField label="Sua senha" value={password} onChange={setPassword} autoComplete="current-password" />}
          {on && !off ? (
            <button type="button" className="btn ghost danger" onClick={() => setOff(true)}>
              Desligar a verificação
            </button>
          ) : (
            <button className={`btn${on ? " ghost danger" : " primary"}`} disabled={!password || start.isPending || disable.isPending}>
              {on ? "Desligar a verificação" : "Ativar a verificação em 2 etapas"}
            </button>
          )}
        </form>
      )}
    </section>
  );
}
