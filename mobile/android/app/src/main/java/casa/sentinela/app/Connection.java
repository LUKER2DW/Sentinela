package casa.sentinela.app;

import android.content.Context;
import android.content.SharedPreferences;
import android.net.ConnectivityManager;
import android.net.LinkProperties;
import android.net.Network;
import android.webkit.CookieManager;

import androidx.annotation.NonNull;

import com.getcapacitor.CapConfig;

import org.json.JSONObject;

import java.io.ByteArrayOutputStream;
import java.io.File;
import java.io.InputStream;
import java.net.InetAddress;
import java.net.InterfaceAddress;
import java.net.NetworkInterface;
import java.nio.charset.StandardCharsets;
import java.security.SecureRandom;
import java.util.Collections;

import casa.sentinela.tailnet.Tailnet;

/**
 * How the app reaches the house (ADR-0019 trial). One build tests every
 * path; the start screen (www/index.html, ConnectionPlugin) picks one:
 * <ul>
 *   <li>{@code tailscale}: the Tailscale app's VPN, tailnet HTTPS name (as before);</li>
 *   <li>{@code tailnet}: Tailscale embedded in this app (P2) — the WebView
 *       loads a local proxy on 127.0.0.1 into the app's own tailnet node;</li>
 *   <li>{@code domain}: the home's own domain, port-forwarded (P1);</li>
 *   <li>{@code funnel}: Tailscale Funnel, {@code https://<name>:10000} (P3).</li>
 * </ul>
 * The chosen URL becomes Capacitor's server.url for this launch, so cookies,
 * the plugins and the offline page behave as with a fixed server.
 */
final class Connection {
    static final String PREFS = "connection";
    static final String LOCAL_ORIGIN = "http://127.0.0.1:47080";
    private static final String LOCAL_ADDR = "127.0.0.1:47080";

    // One random token per process: the local proxy answers only the
    // WebView, which gets it as a cookie (other apps cannot read it).
    private static final String TOKEN = randomHex(32);

    private Connection() {}

    static SharedPreferences prefs(Context c) {
        return c.getSharedPreferences(PREFS, Context.MODE_PRIVATE);
    }

    static String mode(Context c) {
        return prefs(c).getString("mode", "");
    }

    static String tailnetHost() {
        return BuildConfig.TAILNET_HOST;
    }

    static String defaultUrl(String mode) {
        switch (mode) {
            case "tailscale": return "https://" + BuildConfig.TAILNET_HOST;
            case "funnel": return "https://" + BuildConfig.TAILNET_HOST + ":10000";
            case "domain": return BuildConfig.DOMAIN_URL;
            default: return "";
        }
    }

    static String url(Context c, String mode) {
        String saved = prefs(c).getString("url." + mode, "");
        return saved.isEmpty() ? defaultUrl(mode) : saved;
    }

    static File tailnetDir(Context c) {
        return new File(c.getFilesDir(), "tailnet");
    }

    static boolean tailnetEnrolled(Context c) {
        return prefs(c).getBoolean("tailnet.enrolled", false);
    }

    /** The server URL for this launch, or null for the start screen. */
    static String serverUrl(Context c) {
        String mode = mode(c);
        if (mode.equals("tailnet")) {
            if (!tailnetEnrolled(c)) return null;
            try {
                startTailnet(c, "");
            } catch (Exception e) {
                return null; // the start screen shows Tailnet.status()
            }
            return LOCAL_ORIGIN;
        }
        String u = url(c, mode);
        return u.startsWith("https://") ? u.replaceAll("/+$", "") : null;
    }

    private static boolean watching = false;

    /** Starts the embedded node (idempotent) and hands the WebView its token. */
    static void startTailnet(Context c, String authKey) throws Exception {
        Tailnet.setInterfaces(describeInterfaces());
        watchNetwork(c.getApplicationContext());
        String host = prefs(c).getString("tailnet.hostname", "sentinela-app");
        File dir = tailnetDir(c);
        //noinspection ResultOfMethodCallIgnored
        dir.mkdirs();
        Tailnet.start(dir.getAbsolutePath(), host, authKey, tailnetHost(), LOCAL_ADDR, TOKEN);
        CookieManager cm = CookieManager.getInstance();
        cm.setCookie(LOCAL_ORIGIN, "sentinela_local=" + TOKEN + "; Path=/; HttpOnly; SameSite=Strict");
        cm.flush();
    }

    /** Keeps the node's interface list current when Wi-Fi/4G changes. */
    private static synchronized void watchNetwork(Context app) {
        if (watching) return;
        ConnectivityManager cm = (ConnectivityManager) app.getSystemService(Context.CONNECTIVITY_SERVICE);
        if (cm == null) return;
        cm.registerDefaultNetworkCallback(new ConnectivityManager.NetworkCallback() {
            @Override
            public void onLinkPropertiesChanged(@NonNull Network network, @NonNull LinkProperties lp) {
                Tailnet.setInterfaces(describeInterfaces());
            }

            @Override
            public void onLost(@NonNull Network network) {
                Tailnet.setInterfaces(describeInterfaces());
            }
        });
        watching = true;
    }

    /**
     * Capacitor's config from the bundled capacitor.config.json with
     * server.url replaced by this launch's choice.
     */
    @SuppressWarnings("deprecation")
    static CapConfig capConfig(Context c) {
        JSONObject json;
        try (InputStream in = c.getAssets().open("capacitor.config.json")) {
            ByteArrayOutputStream out = new ByteArrayOutputStream();
            byte[] buf = new byte[4096];
            for (int n; (n = in.read(buf)) > 0; ) out.write(buf, 0, n);
            json = new JSONObject(out.toString(StandardCharsets.UTF_8.name()));
            JSONObject server = json.optJSONObject("server");
            if (server == null) {
                server = new JSONObject();
                json.put("server", server);
            }
            String url = serverUrl(c);
            if (url != null) server.put("url", url);
            else server.remove("url");
            JSONObject android = json.optJSONObject("android");
            if (android == null) {
                android = new JSONObject();
                json.put("android", android);
            }
            // The JSON constructor cannot tell a debug build: say it here.
            android.put("webContentsDebuggingEnabled", BuildConfig.DEBUG);
        } catch (Exception e) {
            return CapConfig.loadDefault(c);
        }
        return new CapConfig(c.getAssets(), json);
    }

    /**
     * Interfaces for the embedded node, in the format of Tailnet.setInterfaces:
     * Android 11+ blocks the netlink calls Go would use.
     */
    static String describeInterfaces() {
        StringBuilder sb = new StringBuilder();
        try {
            for (NetworkInterface ni : Collections.list(NetworkInterface.getNetworkInterfaces())) {
                StringBuilder flags = new StringBuilder();
                if (ni.isUp()) flags.append("up,");
                if (ni.isLoopback()) flags.append("loopback,");
                if (ni.isPointToPoint()) flags.append("pointtopoint,");
                if (ni.supportsMulticast()) flags.append("multicast,");
                boolean broadcast = false;
                StringBuilder addrs = new StringBuilder();
                for (InterfaceAddress ia : ni.getInterfaceAddresses()) {
                    InetAddress a = ia.getAddress();
                    if (a == null) continue;
                    if (ia.getBroadcast() != null) broadcast = true;
                    String host = a.getHostAddress();
                    int pct = host.indexOf('%');
                    if (pct >= 0) host = host.substring(0, pct);
                    addrs.append(' ').append(host).append('/').append(ia.getNetworkPrefixLength());
                }
                if (broadcast) flags.append("broadcast,");
                if (flags.length() == 0) flags.append("down,");
                flags.setLength(flags.length() - 1);
                sb.append(ni.getName()).append(' ').append(ni.getIndex()).append(' ')
                        .append(Math.max(ni.getMTU(), 0)).append(' ').append(flags).append(addrs).append('\n');
            }
        } catch (Exception ignored) {
            // An empty list makes Go fall back to its own lookup.
        }
        return sb.toString();
    }

    private static String randomHex(int bytes) {
        byte[] b = new byte[bytes];
        new SecureRandom().nextBytes(b);
        StringBuilder sb = new StringBuilder();
        for (byte x : b) sb.append(String.format("%02x", x));
        return sb.toString();
    }
}
