import { createContext, useCallback, useContext, useState, type ReactNode } from "react";
import type { Role, User } from "./api";

interface SessionCtx {
  user: User;
  can: (role: Role) => boolean;
  logout: () => void;
}

const rank: Record<Role, number> = { viewer: 1, operator: 2, admin: 3 };

const Ctx = createContext<SessionCtx | null>(null);

export function SessionProvider({ user, onLogout, children }: { user: User; onLogout: () => void; children: ReactNode }) {
  const can = useCallback((role: Role) => rank[user.role] >= rank[role], [user.role]);
  return <Ctx.Provider value={{ user, can, logout: onLogout }}>{children}</Ctx.Provider>;
}

export function useSession(): SessionCtx {
  const v = useContext(Ctx);
  if (!v) throw new Error("useSession outside SessionProvider");
  return v;
}

// ---------- toasts ----------

interface Toast {
  id: number;
  text: string;
  error?: boolean;
}

const ToastCtx = createContext<(text: string, error?: boolean) => void>(() => {});

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toast, setToast] = useState<Toast | null>(null);
  const show = useCallback((text: string, error = false) => {
    const id = Date.now();
    setToast({ id, text, error });
    window.setTimeout(() => setToast((t) => (t?.id === id ? null : t)), error ? 6000 : 3000);
  }, []);
  return (
    <ToastCtx.Provider value={show}>
      {children}
      {toast && (
        <div className={`toast${toast.error ? " error" : ""}`} role="status" aria-live="polite">
          {toast.text}
        </div>
      )}
    </ToastCtx.Provider>
  );
}

export const useToast = () => useContext(ToastCtx);
