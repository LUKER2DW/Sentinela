// Android password manager bridge (Credential Manager, mobile app only).
// The WebView never shows Google's "Save password?" sheet for a sign-in
// made with fetch(), so the app asks for it natively after a successful
// login and offers saved passwords on the login screen. Outside the app, or
// on an older APK without the plugin, every call is a quiet no-op.

type Bridge = { isNativePlatform?: () => boolean; nativePromise?: <T>(plugin: string, method: string, opts?: object) => Promise<T> };

const PLUGIN = "SentinelaCredentials";

function bridge(): Bridge | undefined {
  const cap = (window as unknown as { Capacitor?: Bridge }).Capacitor;
  return cap?.isNativePlatform?.() && cap.nativePromise ? cap : undefined;
}

export interface SavedLogin {
  username: string;
  password: string;
}

/** Asks Android to save the password (Google Password Manager sheet). Never throws. */
export async function offerToSavePassword(username: string, password: string): Promise<void> {
  const b = bridge();
  if (!b?.nativePromise || !username || !password) return;
  try {
    // The sheet waits for the user; do not hold the sign-in hostage to it.
    await Promise.race([b.nativePromise(PLUGIN, "save", { username, password }), new Promise((r) => setTimeout(r, 1500))]);
  } catch {
    // dismissed, no password manager, or an APK without the plugin
  }
}

/** Shows Android's saved-password picker; null when dismissed or unavailable. */
export async function pickSavedPassword(): Promise<SavedLogin | null> {
  const b = bridge();
  if (!b?.nativePromise) return null;
  try {
    const r = await b.nativePromise<Partial<SavedLogin>>(PLUGIN, "get");
    return r?.username && r.password ? { username: r.username, password: r.password } : null;
  } catch {
    return null;
  }
}
