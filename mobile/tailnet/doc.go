// Package tailnet is the Tailscale node embedded in the Android app
// (ADR-0019, path P2). It is bound with gomobile into an AAR and called from
// MainActivity/ConnectionPlugin.
//
// The node runs in userspace inside the app (tsnet): no Android VpnService,
// no VPN icon, and only the app's own traffic uses it. The WebView loads
// http://127.0.0.1:<port>, a reverse proxy to the server's HTTPS name over
// the tailnet; 127.0.0.1:8555 (TCP and UDP) is forwarded to the server's
// WebRTC port so live video keeps its direct low-latency path.
//
// Other apps on the phone can reach 127.0.0.1 too, so the proxy answers only
// requests carrying the per-launch random token cookie the app sets in the
// WebView's cookie store.
//
// The exported API uses only gomobile-friendly types.
package tailnet
