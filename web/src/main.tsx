import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App";
import { initTheme } from "./lib/theme";
import { installBackHandler } from "./lib/back";
import "./styles.css";

initTheme(); // before first render: no flash of the wrong theme
installBackHandler(); // Android app back button

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>,
);

// App-shell service worker (installable PWA). Needs HTTPS or localhost.
if ("serviceWorker" in navigator && window.isSecureContext && import.meta.env.PROD) {
  void navigator.serviceWorker.register("/sw.js").catch(() => {});
}
