// Hands an invite / password-reset link from the phone's browser over to
// the Android app (MainActivity handles sentinela://open?path=/#/...). If
// the app is not installed, Chrome stays on this page (fallback URL).

const PACKAGE = "casa.sentinela.app";
const TRIED = "sentinela.openInApp";

/** An Android phone's browser, not the app's own WebView. */
export function isAndroidBrowser(ua = navigator.userAgent): boolean {
  return /Android/i.test(ua) && !/SentinelaApp\//.test(ua) && !/; wv\)/.test(ua);
}

/** intent:// URL that opens path ("/#/convite?...") in the app, falling back to this page. */
export function appIntentURL(path: string, fallback: string): string {
  return (
    "intent://open?path=" +
    encodeURIComponent(path) +
    "#Intent;scheme=sentinela;package=" +
    PACKAGE +
    ";S.browser_fallback_url=" +
    encodeURIComponent(fallback) +
    ";end"
  );
}

/** The current page as an in-app path. */
export function currentAppPath(): string {
  return "/" + window.location.hash;
}

/** Tries once per link to jump to the app; the page keeps a button for a manual tap. */
export function tryOpenInAppOnce(): void {
  if (!isAndroidBrowser()) return;
  const key = TRIED + ":" + window.location.hash;
  try {
    if (sessionStorage.getItem(key)) return;
    sessionStorage.setItem(key, "1");
  } catch {
    return;
  }
  window.location.href = appIntentURL(currentAppPath(), window.location.href);
}
