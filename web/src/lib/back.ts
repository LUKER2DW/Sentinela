// The Android app's back button (MainActivity) asks the page first:
// close what is on top (fullscreen, a sheet, a dialog), else go back a page,
// else return to Live. false = nothing left; the app then handles it
// (second press leaves).
import { t } from "./i18n";
import { EXIT_PLAYER_FULLSCREEN } from "./playerFullscreen";

declare global {
  interface Window {
    sentinelaBack?: (canGoBack: boolean) => boolean;
  }
}

function closeTopmost(): boolean {
  if (document.fullscreenElement) {
    void document.exitFullscreen().catch(() => {});
    return true;
  }
  const player = document.querySelector<HTMLElement>(".player.viewport-fullscreen");
  if (player) {
    player.dispatchEvent(new Event(EXIT_PLAYER_FULLSCREEN));
    return true;
  }
  const sheet = [...document.querySelectorAll<HTMLDialogElement>("dialog[open]")].pop();
  if (sheet) {
    sheet.close(); // fires "close": the React owner updates its state
    return true;
  }
  const dialog = [...document.querySelectorAll<HTMLElement>(".dialog-backdrop")].pop();
  if (dialog) {
    const close =
      dialog.querySelector<HTMLElement>(`[aria-label="${t("Close")}"]`) ??
      [...dialog.querySelectorAll<HTMLElement>("button")].find((b) => /^(Cancelar|Fechar)$/.test(b.textContent?.trim() ?? ""));
    (close ?? dialog).click(); // a click on the backdrop itself closes the others
    return true;
  }
  return false;
}

export function installBackHandler() {
  window.sentinelaBack = (canGoBack) => {
    if (closeTopmost()) return true;
    if (canGoBack) {
      history.back();
      return true;
    }
    const page = window.location.hash.replace(/^#\/?/, "").split("?")[0];
    if (page && page !== "live") {
      window.location.replace("#/live");
      return true;
    }
    return false;
  };
}
