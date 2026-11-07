import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { api } from "../lib/api";
import { useToast } from "../lib/session";
import { CopyField } from "../pages/CloudPage";
import { PasswordField } from "./PasswordField";
import { Icon } from "./Icon";
import { addCameraCommand } from "../lib/camera";

import { t } from "../lib/i18n";
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

const STEPS = [t("Find"), t("Access"), t("Test and install")];

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
        <h2>{t("Find and test cameras on the network")}</h2>
        <span className="muted small">{t("Nothing is written to the camera: it is read only. To add one, use the editor above.")}</span>
      </div>
      <ol className="stepper" aria-label={t("Steps")}>
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
          <p className="muted">{t("Look for XMEye/ONVIF cameras on the same network as the server, or type the IP if you already know it.")}</p>
          <div className="row-gap">
            <button className="btn primary" disabled={search.isPending} onClick={() => search.mutate()}>
              {search.isPending ? <span className="spinner small" /> : <Icon name="search" />} {search.isPending ? t("Searching (up to 5 s)…") : t("Search the network")}
            </button>
            <button className="btn" onClick={() => setStep(1)}>
              <Icon name="edit" />{" "}{t("Type the IP")}
            </button>
          </div>
          {search.data && search.data.length === 0 && <p className="notice">{t("No camera responded. Check that it is on and on the same network, or type the IP.")}</p>}
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
                      <strong>{d.name || t("ONVIF camera")}</strong>
                      <span className="muted small">
                        {d.host}:{d.port}
                        {d.hardware ? ` · ${d.hardware}` : ""}
                      </span>
                    </span>
                    {d.known ? <span className="pill">{t("already registered (")}{d.known})</span> : <Icon name="chevronRight" />}
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
              <label htmlFor="ac-host">{t("IP address")}</label>
              <input id="ac-host" className="input" placeholder="192.168.1.21" value={host} onChange={(e) => setHost(e.target.value.trim())} required autoFocus />
            </div>
            <div className="field">
              <label htmlFor="ac-port">{t("ONVIF port")}</label>
              <input id="ac-port" className="input" type="number" value={port} onChange={(e) => setPort(Number(e.target.value) || 8899)} />
              <span className="hint">{t("XMEye cameras use 8899.")}</span>
            </div>
            <div className="field">
              <label htmlFor="ac-name">{t("Name")}</label>
              <input id="ac-name" className="input" placeholder={t("e.g. Backyard, Garage")} value={name} onChange={(e) => setName(e.target.value)} maxLength={40} />
            </div>
            <div className="field">
              <label htmlFor="ac-user">{t("Camera username")}</label>
              <input id="ac-user" className="input" value={user} onChange={(e) => setUser(e.target.value)} required autoComplete="off" />
            </div>
            <PasswordField label={t("Camera password")} value={pw} onChange={setPw} autoComplete="new-password" />
          </div>
          <p className="muted small">{t("The password is only used for this test; on the server the script asks for it again and it never appears on screen.")}</p>
          <div className="row-gap">
            <button type="button" className="btn ghost" onClick={() => setStep(0)}>
              {t("Back")}
            </button>
            <button className="btn primary" disabled={test.isPending || !validHost || !pw}>
              {test.isPending ? <span className="spinner small" /> : <Icon name="scan" />} {test.isPending ? t("Testing (up to 30 s)…") : t("Test the camera")}
            </button>
          </div>
        </form>
      )}

      {step === 2 && r && (
        <div className="step-body">
          {r.known && <p className="notice">{t("This camera is already registered as")}{" "}{r.known}.</p>}
          <ul className="check-list big">
            <li className={r.onvif ? "ok" : "fail"}>
              <strong>{t("Identification (ONVIF)")}</strong>
              <span className="muted small">{r.onvif ? t("{manufacturer} {model}, firmware {firmware}", { manufacturer: r.onvif.manufacturer, model: r.onvif.model, firmware: r.onvif.firmware }) : r.onvif_error}</span>
            </li>
            <li className={r.model ? "ok" : "fail"}>
              <strong>{t("Password and control (DVRIP)")}</strong>
              <span className="muted small">{r.model ? r.model + (r.control_approved ? t(": PTZ and light enabled") : t(": model not verified, video only")) : r.dvrip_error}</span>
            </li>
            {r.sessions.map((s, i) => (
              <li key={i} className={s.ok ? "ok" : "fail"}>
                <strong>
                  {t("Connection")}{" "}{i + 1} ({s.stream === "main" ? t("HD") : t("SD")})
                </strong>
                <span className="muted small">{s.ok ? [s.video, s.audio].filter(Boolean).join(" + ") : s.error}</span>
              </li>
            ))}
          </ul>
          {r.sessions_ok && !r.known ? (
            <div className="install-box">
              <strong>{t("All good. To add it, run on the server:")}</strong>
              <CopyField label={t("command")} value={addCameraCommand(r.suggested_id, host, name, port)} />
              <span className="muted small">{t("The password is asked there, without showing. After that the camera appears in Live and starts recording.")}</span>
            </div>
          ) : (
            !r.sessions_ok && <p className="notice error">{t("The camera did not open the 3 connections at the same time. Close other apps (VLC, XMEye) connected to it and test again.")}</p>
          )}
          <div className="row-gap">
            <button className="btn ghost" onClick={() => setStep(1)}>
              {t("Back")}
            </button>
            <button className="btn" disabled={test.isPending} onClick={() => test.mutate()}>
              <Icon name="refresh" />{" "}{t("Test again")}
            </button>
          </div>
        </div>
      )}
    </section>
  );
}
