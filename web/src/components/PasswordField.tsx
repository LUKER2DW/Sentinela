import { useId, useState } from "react";
import { passwordStrength } from "../lib/password";
import { Icon } from "./Icon";

import { t } from "../lib/i18n";
interface Props {
  label: string;
  value: string;
  onChange: (v: string) => void;
  autoComplete: "current-password" | "new-password";
  /** Show the strength meter (new passwords). */
  meter?: boolean;
  /** For "repeat password": shows whether it matches. */
  matches?: string;
  minLength?: number;
  autoFocus?: boolean;
  hideLabel?: boolean;
}

export function PasswordField({ label, value, onChange, autoComplete, meter, matches, minLength, autoFocus, hideLabel }: Props) {
  const id = useId();
  const [shown, setShown] = useState(false);
  const st = meter ? passwordStrength(value) : null;
  const match = matches !== undefined && value.length > 0 ? value === matches : null;

  return (
    <div className="field">
      <label htmlFor={id} className={hideLabel ? "sr-only" : undefined}>
        {label}
      </label>
      <div className="pw">
        <input
          id={id}
          className="input"
          type={shown ? "text" : "password"}
          autoComplete={autoComplete}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          minLength={minLength}
          autoFocus={autoFocus}
          placeholder={hideLabel ? label : undefined}
          required
          spellCheck={false}
          aria-describedby={st || match !== null ? `${id}-hint` : undefined}
        />
        {match !== null && (
          <span className={`pw-match ${match ? "ok" : "no"}`} aria-hidden="true">
            <Icon name={match ? "check" : "x"} />
          </span>
        )}
        <button
          type="button"
          className="pw-eye"
          onClick={() => setShown((s) => !s)}
          aria-label={shown ? t("Hide password") : t("Show password")}
          aria-pressed={shown}
          title={shown ? t("Hide password") : t("Show password")}
        >
          <Icon name={shown ? "eyeOff" : "eye"} />
        </button>
      </div>
      {st && (
        <div className="strength" data-score={st.score} id={`${id}-hint`} aria-live="polite">
          <span className="bars" aria-hidden="true">
            <i />
            <i />
            <i />
            <i />
          </span>
          <span className="strength-label">{st.label || t("Use 10 characters or more; passphrases are great.")}</span>
        </div>
      )}
      {match === false && (
        <span className="hint bad" id={`${id}-hint`}>
          {t("The passwords do not match yet.")}
        </span>
      )}
    </div>
  );
}
