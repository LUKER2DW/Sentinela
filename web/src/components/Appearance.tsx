import { PALETTES, setPalette, setThemeMode, usePalette, useThemeMode, type Palette, type ThemeMode } from "../lib/theme";
import { LANGS, getLangChoice, setLang, t, type Lang } from "../lib/i18n";
import { CardHead } from "./CardHead";
import { Icon } from "./Icon";

const THEMES: Record<Palette, { label: string; hint: string }> = {
  mar: { label: "Maré", hint: "Verde-mar e areia, o tema do farol" },
  oceano: { label: "Oceano", hint: "Azul profundo e calmo" },
  floresta: { label: "Floresta", hint: "Verde musgo, tons de mata" },
  areia: { label: "Duna", hint: "Dourado quente de fim de tarde" },
  lavanda: { label: "Lavanda", hint: "Lilás suave e sereno" },
  grafite: { label: "Grafite", hint: "Sem cor, só tons de cinza" },
};

const MODES: { value: ThemeMode; label: string; icon: string }[] = [
  { value: "light", label: "Claro", icon: "sun" },
  { value: "dark", label: "Escuro", icon: "moon" },
  { value: "black", label: "Preto", icon: "moon" },
  { value: "system", label: "Automático", icon: "monitor" },
];

/** A small picture of the app in a theme: side menu, a card, a button, the sea. */
function ThemeMock({ palette }: { palette: Palette }) {
  return (
    <span className="theme-mock" data-preview-palette={palette} aria-hidden="true">
      <span className="tm-side">
        <i />
        <i className="on" />
        <i />
        <i />
      </span>
      <span className="tm-main">
        <span className="tm-title" />
        <span className="tm-card">
          <span className="tm-line" />
          <span className="tm-line short" />
          <span className="tm-btn" />
        </span>
        <svg className="tm-sea" viewBox="0 0 100 24" preserveAspectRatio="none">
          <path className="tm-w3" d="M0 9 Q12.5 4 25 9 T50 9 T75 9 T100 9 V24 H0z" />
          <path className="tm-w2" d="M0 13 Q12.5 8 25 13 T50 13 T75 13 T100 13 V24 H0z" />
          <path className="tm-w1" d="M0 17 Q12.5 12 25 17 T50 17 T75 17 T100 17 V24 H0z" />
        </svg>
      </span>
    </span>
  );
}

/** Theme and brightness (the theme used to be a button in the top bar). Per device. */
export function AppearanceCard() {
  const mode = useThemeMode();
  const palette = usePalette();
  return (
    <section className="card set-card" aria-label={t("Aparência")}>
      <CardHead icon="sun" title={t("Tema")} sub={t("Vale só para este aparelho. Cada pessoa escolhe o seu.")} />
      <div className="theme-grid" role="radiogroup" aria-label="Tema">
        {PALETTES.map((p) => (
          <button key={p} role="radio" aria-checked={palette === p} className={`theme-card${palette === p ? " on" : ""}`} onClick={() => setPalette(p)}>
            <ThemeMock palette={p} />
            <span className="theme-card-text">
              <strong>
                {t(THEMES[p].label)}
                {palette === p && <Icon name="check" />}
              </strong>
              <span className="muted small">{t(THEMES[p].hint)}</span>
            </span>
          </button>
        ))}
      </div>
      <div className="set-group">
        <h3 className="set-group-title">{t("Brilho")}</h3>
        <div className="segmented mode-seg" role="radiogroup" aria-label="Brilho">
          {MODES.map((m) => (
            <button key={m.value} role="radio" aria-checked={mode === m.value} aria-pressed={mode === m.value} onClick={() => setThemeMode(m.value)}>
              <Icon name={m.icon} /> {t(m.label)}
            </button>
          ))}
        </div>
        <p className="muted small">{t(mode === "black" ? "Preto total: economiza bateria em telas OLED." : mode === "system" ? "Segue o claro ou escuro do aparelho." : "Todos os temas têm versão clara e escura.")}</p>
      </div>
      <div className="set-group">
        <h3 className="set-group-title">{t("Idioma")}</h3>
        <select className="input" aria-label={t("Idioma")} defaultValue={getLangChoice()} onChange={(e) => setLang(e.target.value as Lang | "auto")}>
          {LANGS.map((l) => (
            <option key={l.id} value={l.id}>
              {l.id === "auto" ? t(l.label) : l.label}
            </option>
          ))}
        </select>
        <p className="muted small">{t("O automático usa português em aparelhos em português e inglês nos outros.")}</p>
      </div>
    </section>
  );
}
