import { setThemeMode, useThemeMode, type ThemeMode } from "../lib/theme";
import { CardHead } from "./CardHead";

const MODES: { value: ThemeMode; label: string; hint: string }[] = [
  { value: "light", label: "Claro", hint: "Papel claro, bom de dia." },
  {
    value: "dark",
    label: "Escuro",
    hint: "Grafite, descansa a vista à noite.",
  },
  { value: "system", label: "Automático", hint: "Segue o tema do aparelho." },
];

/** Theme choice (it used to be a button in the top bar). Per device. */
export function AppearanceCard() {
  const mode = useThemeMode();
  return (
    <section className="card set-card" aria-label="Aparência">
      <CardHead icon="sun" title="Tema" sub="Vale só para este aparelho. Cada pessoa escolhe o seu." />
      <div className="theme-picks" role="radiogroup" aria-label="Tema">
        {MODES.map((m) => (
          <button
            key={m.value}
            role="radio"
            aria-checked={mode === m.value}
            className={`theme-pick${mode === m.value ? " on" : ""}`}
            onClick={() => setThemeMode(m.value)}
          >
            <span className={`theme-swatch ${m.value}`} aria-hidden="true">
              <span className="sw-bar" />
              <span className="sw-line" />
              <span className="sw-line short" />
            </span>
            <strong>{m.label}</strong>
            <span className="muted small">{m.hint}</span>
          </button>
        ))}
      </div>
    </section>
  );
}
