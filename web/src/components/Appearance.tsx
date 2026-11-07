import { PALETTES, setPalette, setThemeMode, usePalette, useThemeMode, type Palette, type ThemeMode } from "../lib/theme";
import { LANGS, getLangChoice, setLang, t, type Lang } from "../lib/i18n";
import { CardHead } from "./CardHead";
import { Icon } from "./Icon";

const THEMES: Record<Palette, { label: string; hint: string }> = {
  mar: { label: t("Tide"), hint: t("Sea green and sand, the lighthouse theme") },
  oceano: { label: t("Ocean"), hint: t("Deep, calm blue") },
  floresta: { label: t("Forest"), hint: t("Moss green, woodland tones") },
  areia: { label: t("Dune"), hint: t("Warm late-afternoon gold") },
  lavanda: { label: t("Lavender"), hint: t("Soft, serene lilac") },
  grafite: { label: t("Graphite"), hint: t("No colour, only greys") },
};

const MODES: { value: ThemeMode; label: string; icon: string }[] = [
  { value: "light", label: t("Light"), icon: "sun" },
  { value: "dark", label: t("Dark"), icon: "moon" },
  { value: "black", label: t("Black"), icon: "moon" },
  { value: "system", label: t("Automatic"), icon: "monitor" },
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
    <section className="card set-card" aria-label={t("Appearance")}>
      <CardHead icon="sun" title={t("Theme")} sub={t("Only on this device. Everyone picks their own.")} />
      <div className="theme-grid" role="radiogroup" aria-label={t("Theme")}>
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
        <h3 className="set-group-title">{t("Brightness")}</h3>
        <div className="segmented mode-seg" role="radiogroup" aria-label={t("Brightness")}>
          {MODES.map((m) => (
            <button key={m.value} role="radio" aria-checked={mode === m.value} aria-pressed={mode === m.value} onClick={() => setThemeMode(m.value)}>
              <Icon name={m.icon} /> {t(m.label)}
            </button>
          ))}
        </div>
        <p className="muted small">{t(mode === "black" ? "True black: saves battery on OLED screens." : mode === "system" ? "Follows the device's light or dark setting." : t("Every theme has a light and a dark version."))}</p>
      </div>
      <div className="set-group">
        <h3 className="set-group-title">{t("Language")}</h3>
        <select className="input" aria-label={t("Language")} defaultValue={getLangChoice()} onChange={(e) => {
            // Reload so text built at load time (menus, labels) follows too.
            setLang(e.target.value as Lang | "auto");
            window.location.reload();
          }}>
          {LANGS.map((l) => (
            <option key={l.id} value={l.id}>
              {l.id === "auto" ? t(l.label) : l.label}
            </option>
          ))}
        </select>
        <p className="muted small">{t("Automatic uses Portuguese on Portuguese devices and English on the others.")}</p>
      </div>
    </section>
  );
}
