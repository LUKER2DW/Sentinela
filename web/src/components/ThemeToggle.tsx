import { setTheme, useTheme } from "../lib/theme";
import { Moon, Sun } from "./Doodles";

export function ThemeToggle({ className = "" }: { className?: string }) {
  const theme = useTheme();
  return (
    <div className={`segmented theme-toggle ${className}`} role="group" aria-label="Tema">
      <button aria-pressed={theme === "light"} onClick={() => setTheme("light")} title="Tema claro">
        <Sun />
        <span className="label">Claro</span>
      </button>
      <button aria-pressed={theme === "dark"} onClick={() => setTheme("dark")} title="Tema escuro">
        <Moon />
        <span className="label">Escuro</span>
      </button>
    </div>
  );
}
