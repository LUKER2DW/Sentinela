// In-app updates for the Android app (sideloaded, no Play Store). The server
// offers the newest APK (GET /api/app/latest); the app's SentinelaUpdate
// plugin downloads it, checks the SHA-256 and opens Android's installer.
import { useQuery } from "@tanstack/react-query";
import { get } from "./api";

export interface AppRelease {
  available: boolean;
  version_code?: number;
  version_name?: string;
  size?: number;
  sha256?: string;
  notes?: string;
}

export interface AppInfo {
  versionCode: number;
  versionName: string;
  canInstall: boolean;
}

type Listener = { remove: () => Promise<void> | void };
type Bridge = {
  isNativePlatform?: () => boolean;
  nativePromise?: <T>(plugin: string, method: string, opts?: object) => Promise<T>;
  Plugins?: Record<
    string,
    | {
        addListener?: (ev: string, fn: (d: unknown) => void) => Promise<Listener> | Listener;
      }
    | undefined
  >;
};

function bridge(): Bridge | undefined {
  const cap = (window as unknown as { Capacitor?: Bridge }).Capacitor;
  return cap?.isNativePlatform?.() && cap.nativePromise ? cap : undefined;
}

/** The installed app's version; null in a browser or an app without the plugin (≤ 1.8). */
export async function appInfo(): Promise<AppInfo | null> {
  const cap = bridge();
  if (!cap) return null;
  try {
    return await cap.nativePromise!<AppInfo>("SentinelaUpdate", "info");
  } catch {
    return null;
  }
}

/** Installed version and the server's release, refreshed every 30 min. */
export function useAppUpdate() {
  const info = useQuery({
    queryKey: ["app-info"],
    queryFn: appInfo,
    staleTime: Infinity,
  });
  const rel = useQuery({
    queryKey: ["app-release"],
    queryFn: () => get<AppRelease>("/api/app/latest"),
    refetchInterval: 30 * 60_000,
    staleTime: 10 * 60_000,
  });
  const newer = !!info.data && !!rel.data?.available && (rel.data.version_code ?? 0) > info.data.versionCode;
  return { info: info.data ?? null, release: rel.data, newer };
}

/**
 * Downloads and installs the release. Resolves "installing" when Android's
 * installer opened, or "permission" when the one-time "install unknown
 * apps" screen was shown instead (call again after allowing).
 */
export async function installUpdate(rel: AppRelease, onProgress: (pct: number) => void): Promise<"installing" | "permission"> {
  const cap = bridge();
  if (!cap || !rel.sha256 || !rel.size) throw new Error("Atualização indisponível.");
  let sub: Listener | undefined;
  try {
    sub = await cap.Plugins?.SentinelaUpdate?.addListener?.("progress", (d) => {
      const p = d as { sent: number; total: number };
      if (p.total > 0) onProgress(Math.round((100 * p.sent) / p.total));
    });
  } catch {
    // progress is optional
  }
  try {
    const r = await cap.nativePromise!<{ state: "installing" | "permission" }>("SentinelaUpdate", "install", { sha256: rel.sha256, size: rel.size });
    return r.state;
  } finally {
    void sub?.remove();
  }
}
