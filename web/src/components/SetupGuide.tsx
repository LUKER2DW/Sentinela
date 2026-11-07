import { useState, type ReactNode } from "react";
import { t } from "../lib/i18n";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, get } from "../lib/api";
import { useToast } from "../lib/session";
import { AddCameraCard } from "./AddCamera";
import { DeviceNotifyCard } from "./Notifications";
import { SecurityCard } from "./Security";
import { AppPairingCard } from "./AppPairing";
import { Icon } from "./Icon";
import { LighthouseMark } from "./Lighthouse";

/** GET /api/onboarding: what the server already has. */
export interface Onboarding {
  pending: boolean;
  cameras: { id: string; name: string; vendor: string; control: boolean; recording: boolean }[];
  timezone: string;
  max_disk_percent: number;
  retention_days: number;
  volume_marker: boolean;
  nuvem: boolean;
  nas_host: string;
  push: boolean;
  hosting: boolean;
  totp: boolean;
  zone: string;
  tailnet_url: string;
  public_url: string;
  public_pinned: boolean;
}

const STEPS = [
  { id: "welcome", label: "Boas-vindas" },
  { id: "cameras", label: "Câmeras" },
  { id: "recording", label: "Gravação" },
  { id: "nuvem", label: "Nuvem" },
  { id: "remote", label: "Acesso remoto" },
  { id: "app", label: "App" },
  { id: "notify", label: "Notificações" },
  { id: "security", label: "Segurança" },
  { id: "done", label: "Pronto" },
] as const;

const vendorLabel: Record<string, string> = {
  xmeye: "XMEye",
  hikvision: "Hikvision",
  dahua: "Dahua",
  generic: "RTSP genérica",
};

/** First-run guide for the admin of a new install. Every step can be
 *  skipped; settings that live in the server's files are shown with the
 *  file or command that changes them. */
export function SetupGuide({ onClose }: { onClose: () => void }) {
  const toast = useToast();
  const qc = useQueryClient();
  const [step, setStep] = useState(0);
  const info = useQuery({ queryKey: ["onboarding"], queryFn: () => get<Onboarding>("/api/onboarding") });
  const finish = useMutation({
    mutationFn: () => api("PUT", "/api/onboarding", { pending: false }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["me"] });
      onClose();
    },
    onError: (e: Error) => toast(`Não foi possível concluir: ${e.message}`, true),
  });
  const o = info.data;
  const last = STEPS.length - 1;
  const next = () => setStep((s) => Math.min(s + 1, last));
  const back = () => setStep((s) => Math.max(s - 1, 0));
  const id = STEPS[step].id;

  return (
    <div className="setup-guide">
      <header className="setup-head">
        <LighthouseMark />
        <div>
          <h1>{t("Configurar o Sentinela")}</h1>
          <p className="muted">{t("Alguns minutos para deixar a casa pronta. Pule o que quiser e volte depois em Sistema.")}</p>
        </div>
        <button className="btn ghost small" disabled={finish.isPending} onClick={() => finish.mutate()}>
          Pular configuração
        </button>
      </header>

      <ol className="stepper" aria-label="Etapas">
        {STEPS.map((s, i) => (
          <li key={s.id} className={i === step ? "on" : i < step ? "done" : ""}>
            <button type="button" onClick={() => setStep(i)}>
              <span className="step-n">{i < step ? <Icon name="check" /> : i + 1}</span>
              {t(s.label)}
            </button>
          </li>
        ))}
      </ol>

      {info.isError && <p className="notice error">Não foi possível ler o servidor. {(info.error as Error).message}</p>}
      {o && (
        <div className="setup-body">
          {id === "welcome" && <Welcome o={o} />}
          {id === "cameras" && <Cameras o={o} />}
          {id === "recording" && <Recording o={o} />}
          {id === "nuvem" && <Nuvem o={o} />}
          {id === "remote" && <Remote o={o} />}
          {id === "app" && <AppStep />}
          {id === "notify" && <Notify o={o} />}
          {id === "security" && <Security o={o} />}
          {id === "done" && <Done o={o} />}
        </div>
      )}

      <footer className="setup-foot">
        {step > 0 && (
          <button className="btn ghost" onClick={back}>
            {t("Voltar")}
          </button>
        )}
        <span className="spacer" />
        {step < last ? (
          <>
            {step > 0 && (
              <button className="btn ghost" onClick={next}>
                Pular esta etapa
              </button>
            )}
            <button className="btn primary" onClick={next}>
              {t(step === 0 ? "Começar" : "Continuar")}
            </button>
          </>
        ) : (
          <button className="btn primary" disabled={finish.isPending} onClick={() => finish.mutate()}>
            {t("Ir para Ao vivo")}
          </button>
        )}
      </footer>
    </div>
  );
}

function Step({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="card setup-step">
      <h2>{title}</h2>
      {children}
    </section>
  );
}

function Status({ ok, children }: { ok: boolean; children: ReactNode }) {
  return (
    <p className={`setup-status${ok ? " ok" : ""}`}>
      <Icon name={ok ? "check" : "info"} /> {children}
    </p>
  );
}

function Welcome({ o }: { o: Onboarding }) {
  return (
    <Step title="Bem-vindo">
      <p>A conta de administrador está criada. Agora vamos conferir:</p>
      <ul className="setup-list">
        <li>as câmeras e como adicionar outras;</li>
        <li>onde e por quanto tempo as gravações ficam;</li>
        <li>a Nuvem, o armazenamento de arquivos da casa;</li>
        <li>como acessar de fora de casa (opcional) e conectar o app;</li>
        <li>as notificações deste aparelho;</li>
        <li>a verificação em duas etapas (opcional).</li>
      </ul>
      <p className="muted small">
        {o.cameras.length === 0
          ? "Nenhuma câmera configurada ainda."
          : `${o.cameras.length} câmera${o.cameras.length > 1 ? "s" : ""} configurada${o.cameras.length > 1 ? "s" : ""} pelo instalador.`}
      </p>
    </Step>
  );
}

function Cameras({ o }: { o: Onboarding }) {
  return (
    <div className="stack">
      <Step title="Câmeras">
        {o.cameras.length === 0 ? (
          <Status ok={false}>Nenhuma câmera ainda. Adicione a primeira abaixo.</Status>
        ) : (
          <ul className="setup-cams">
            {o.cameras.map((c) => (
              <li key={c.id}>
                <Icon name="live" />
                <strong>{c.name}</strong>
                <span className="muted small">
                  {vendorLabel[c.vendor] ?? c.vendor} · {c.recording ? "gravando" : "sem gravação"}
                  {c.control ? " · PTZ e luz" : ""}
                </span>
              </li>
            ))}
          </ul>
        )}
        <p className="muted small">
          Câmeras XMEye podem ser encontradas e testadas aqui. Para Hikvision, Dahua ou qualquer câmera RTSP, use no
          servidor <code>sudo VENDOR=hikvision /opt/sentinela/scripts/add-camera.sh id IP "Nome"</code> (veja o guia de
          instalação). As senhas ficam só no servidor.
        </p>
      </Step>
      <AddCameraCard />
    </div>
  );
}

function Recording({ o }: { o: Onboarding }) {
  return (
    <Step title="Gravação">
      <Status ok={o.volume_marker}>
        {o.volume_marker
          ? "As gravações só acontecem no disco de gravações: se ele não montar, a gravação pausa em vez de encher o disco do sistema."
          : "A verificação do disco de gravações está desligada (volume_marker: none)."}
      </Status>
      <dl className="setup-facts">
        <dt>Por quanto tempo</dt>
        <dd>
          {o.retention_days > 0
            ? `${o.retention_days} dias`
            : `até o disco chegar a ${o.max_disk_percent}% (depois as mais antigas são substituídas)`}
        </dd>
        <dt>Fuso horário</dt>
        <dd>{o.timezone}</dd>
      </dl>
      <p className="muted small">
        Para mudar, edite <code>/etc/sentinela/config.yaml</code> no servidor (<code>recording</code> e{" "}
        <code>timezone</code>) e reinicie com <code>./scripts/deploy.sh</code>.
      </p>
    </Step>
  );
}

function Nuvem({ o }: { o: Onboarding }) {
  return (
    <Step title="Nuvem">
      {o.nuvem ? (
        <>
          <Status ok>A Nuvem está ativa: arquivos pelo navegador, pelo app e na rede da casa.</Status>
          {o.nas_host && (
            <p>
              No computador, abra <code>\\{o.nas_host}\Nuvem</code>. A senha da pasta de rede é definida no servidor com{" "}
              <code>sudo smbpasswd -a seu-usuario</code>.
            </p>
          )}
        </>
      ) : (
        <>
          <Status ok={false}>A Nuvem está desligada: nenhum disco em /srv/nas.</Status>
          <p className="muted small">
            Ela é opcional. Para ligar, monte um disco em <code>/srv/nas</code> e rode{" "}
            <code>sudo /opt/sentinela/scripts/setup-nas.sh</code>. Pode pular esta etapa.
          </p>
        </>
      )}
    </Step>
  );
}

function Remote({ o }: { o: Onboarding }) {
  const none = !o.tailnet_url && !o.public_url;
  return (
    <Step title="Acesso de fora de casa">
      <p className="muted small">
        Por padrão o Sentinela só responde na rede da casa. Para usar o app ou o navegador fora de casa, escolha um
        caminho. Nenhum é obrigatório; dá para fazer depois.
      </p>
      <div className="remote-choices">
        <div className={`remote-choice${none ? " on" : ""}`}>
          <strong>Só em casa</strong>
          <span className="muted small">Nada exposto. O app funciona só se o celular estiver numa VPN para casa.</span>
        </div>
        <div className={`remote-choice${o.tailnet_url ? " on" : ""}`}>
          <strong>Tailscale (recomendado)</strong>
          <span className="muted small">
            VPN privada, sem abrir portas. No servidor: <code>sudo /opt/sentinela/scripts/setup-tailscale.sh</code>, depois
            coloque o endereço em <code>remote.tailnet_url</code>.
          </span>
          {o.tailnet_url && <code>{o.tailnet_url}</code>}
        </div>
        <div className={`remote-choice${o.public_url ? " on" : ""}`}>
          <strong>Porta no roteador</strong>
          <span className="muted small">
            Endereço público com HTTPS, de qualquer celular. No servidor:{" "}
            <code>sudo /opt/sentinela/scripts/setup-public.sh</code> e encaminhe a porta no roteador. Só a tela de login
            fica visível de fora; ative a verificação em duas etapas.
          </span>
          {o.public_url && (
            <code>
              {o.public_url}
              {o.public_pinned ? " (certificado próprio)" : ""}
            </code>
          )}
        </div>
      </div>
      <p className="muted small">
        Nunca encaminhe as portas das câmeras, do RTSP ou da pasta de rede: só o Sentinela.
      </p>
    </Step>
  );
}

function AppStep() {
  return (
    <div className="stack">
      <Step title="App Android">
        <p className="muted small">
          O mesmo app serve para qualquer casa: ele se conecta ao endereço que você escolher aqui. Também fica em Sistema
          → Aplicativo, para cada pessoa da casa.
        </p>
      </Step>
      <AppPairingCard />
    </div>
  );
}

function Notify({ o }: { o: Onboarding }) {
  if (!o.push)
    return (
      <Step title="Notificações">
        <Status ok={false}>As notificações do navegador não estão disponíveis neste servidor.</Status>
      </Step>
    );
  return (
    <div className="stack">
      <Step title="Notificações">
        <p className="muted small">
          Receba avisos de movimento e de pessoas neste aparelho. Cada pessoa escolhe os seus em Minha conta.
        </p>
      </Step>
      <DeviceNotifyCard />
    </div>
  );
}

function Security({ o }: { o: Onboarding }) {
  return (
    <div className="stack">
      <Step title="Segurança">
        <p className="muted small">
          A verificação em duas etapas é opcional e recomendada se você abrir o acesso pela internet. Ela usa um app
          como Google Authenticator ou Aegis.
        </p>
        {o.zone !== "lan" && o.zone !== "tailnet" && (
          <Status ok={false}>Ative em casa ou pelo Tailscale: de fora ela não pode ser configurada.</Status>
        )}
      </Step>
      <SecurityCard />
    </div>
  );
}

function Done({ o }: { o: Onboarding }) {
  return (
    <Step title="Tudo pronto">
      <p>O Sentinela está configurado. Próximos passos, quando quiser:</p>
      <ul className="setup-list">
        <li>Convide a família em Sistema → Usuários (cada um escolhe a própria senha).</li>
        <li>Desenhe zonas de detecção em cada câmera, em Sistema → Detecção.</li>
        <li>Instale o app Android para ver as câmeras e receber avisos.</li>
        {o.hosting && <li>Publique seus projetos em Serviços.</li>}
      </ul>
      <p className="muted small">Este assistente pode ser aberto de novo em Sistema → Visão geral.</p>
    </Step>
  );
}

/** Button that reopens the guide (System page, admins). */
export function ReopenSetupGuide() {
  const qc = useQueryClient();
  const toast = useToast();
  const reopen = useMutation({
    mutationFn: () => api("PUT", "/api/onboarding", { pending: true }),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["me"] }),
    onError: (e: Error) => toast(e.message, true),
  });
  return (
    <button className="btn ghost small" disabled={reopen.isPending} onClick={() => reopen.mutate()}>
      <Icon name="system" /> Assistente de configuração
    </button>
  );
}
