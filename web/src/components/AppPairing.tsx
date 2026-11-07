import { useQuery } from "@tanstack/react-query";
import { get } from "../lib/api";
import { CopyField } from "../pages/NuvemPage";
import { isApp } from "../lib/push";
import { canChangeConnection, changeConnection } from "../lib/access";
import { Icon } from "./Icon";

/** GET /api/app/pairing: the ways the Android app can reach this house. */
interface PairingOption {
  kind: "tailnet" | "public";
  url: string;
  pin?: string;
  link: string;
  qr_svg: string;
}

const kindLabel = {
  tailnet: { title: "Pelo Tailscale", note: "Só para aparelhos na sua rede Tailscale (com o app Tailscale ligado)." },
  public: { title: "Pela internet", note: "Pelo endereço público da casa, de qualquer lugar." },
} as const;

/** Pairing QR codes for the one Sentinela APK: the phone's camera opens the
 *  app with the house's address and certificate; the app asks before it
 *  switches. */
export function AppPairingCard() {
  // Inside the app the QR code is something to scan, not to show: this phone
  // is already paired; it can switch houses from the start screen.
  if (isApp()) return <AppConnectionCard />;
  return <PairingCodes />;
}

function AppConnectionCard() {
  return (
    <section className="card" aria-label="Conexão do app">
      <div className="card-head">
        <h2>Conexão deste celular</h2>
      </div>
      <p className="setup-status ok">
        <Icon name="check" /> Conectado a <code>{location.host}</code>
      </p>
      <p className="muted small">
        Para conectar outro celular, abra Sistema → Aplicativo no computador e aponte a câmera desse celular para o QR
        code. Se a casa mudar de endereço, este app procura os outros endereços dela sozinho.
      </p>
      {canChangeConnection() && (
        <button className="btn small" onClick={changeConnection}>
          <Icon name="refresh" /> Trocar de casa ou de endereço
        </button>
      )}
    </section>
  );
}

function PairingCodes() {
  const q = useQuery({ queryKey: ["app", "pairing"], queryFn: () => get<{ options: PairingOption[] }>("/api/app/pairing") });
  const opts = q.data?.options ?? [];
  return (
    <section className="card" aria-label="Conectar o app">
      <div className="card-head">
        <h2>Conectar o app Android</h2>
      </div>
      <p className="muted small">
        Instale o app Sentinela no celular, abra-o e aponte a câmera do celular para o código. O app mostra o endereço e pergunta
        antes de conectar. Ou copie o código de pareamento e cole na tela inicial do app.
      </p>
      {q.isLoading && <p className="muted">Carregando…</p>}
      {q.isError && <p className="notice error">Não foi possível ler os endereços da casa.</p>}
      {q.isSuccess && opts.length === 0 && (
        <p className="notice">
          O app precisa de um caminho de fora de casa: Tailscale (<code>remote.tailnet_url</code>) ou o endereço
          público (<code>remote.public</code>) em <code>/etc/sentinela/config.yaml</code>. Veja o guia de instalação,
          seção Acesso remoto.
        </p>
      )}
      <div className="pairing-grid">
        {opts.map((o) => (
          <figure key={o.kind} className="pairing">
            <img src={`data:image/svg+xml;charset=utf-8,${encodeURIComponent(o.qr_svg)}`} alt={`QR code: ${kindLabel[o.kind].title}`} />
            <figcaption>
              <strong>{kindLabel[o.kind].title}</strong>
              <span className="muted small">{kindLabel[o.kind].note}</span>
              <code className="pairing-url">{o.url}</code>
              {o.pin && <span className="muted small">Certificado próprio da casa: o app confere a impressão digital.</span>}
              <CopyField value={o.link} label="Código de pareamento" />
            </figcaption>
          </figure>
        ))}
      </div>
    </section>
  );
}
