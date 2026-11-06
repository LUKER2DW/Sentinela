import { describe, expect, it } from "vitest";
import { appIntentURL, isAndroidBrowser } from "./openInApp";

describe("openInApp", () => {
  it("detects the phone browser but not the app's WebView", () => {
    expect(isAndroidBrowser("Mozilla/5.0 (Linux; Android 14; SM-A546E) AppleWebKit/537.36 Chrome/129 Mobile Safari/537.36")).toBe(true);
    expect(isAndroidBrowser("Mozilla/5.0 (Linux; Android 14; wv) AppleWebKit/537.36 Chrome/129 Mobile Safari/537.36 SentinelaApp/1")).toBe(false);
    expect(isAndroidBrowser("Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/129")).toBe(false);
  });

  it("builds an intent URL for the app with this page as fallback", () => {
    const url = appIntentURL("/#/convite?c=ABC&u=ana%40x.com", "https://h:18443/#/convite?c=ABC");
    expect(url.startsWith("intent://open?path=%2F%23%2Fconvite%3Fc%3DABC%26u%3Dana%2540x.com#Intent;")).toBe(true);
    expect(url).toContain(";scheme=sentinela;package=casa.sentinela.app;");
    expect(url).toContain(";S.browser_fallback_url=https%3A%2F%2Fh%3A18443%2F%23%2Fconvite%3Fc%3DABC;end");
  });
});
