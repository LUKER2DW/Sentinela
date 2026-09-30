package casa.sentinela.app;

import android.content.Context;
import android.content.SharedPreferences;

import com.getcapacitor.CapConfig;

import org.json.JSONObject;

import java.io.ByteArrayOutputStream;
import java.io.File;
import java.io.InputStream;
import java.nio.charset.StandardCharsets;

/**
 * How the app reaches the house. After the ADR-0019 trial only the home's
 * public address (port-forward, self-signed certificate pinned by Pin) is
 * left: the Tailscale VPN, the embedded tailnet and Funnel paths are gone.
 * The start screen (www/index.html, ConnectionPlugin) edits the address;
 * it becomes Capacitor's server.url for this launch, so cookies, the
 * plugins and the offline state behave as with a fixed server.
 */
final class Connection {
    static final String PREFS = "connection";
    /** The only mode left; older installs may still say tailscale/tailnet/funnel. */
    static final String MODE = "domain";

    /**
     * Set when the chosen server did not answer (MainActivity): the next
     * launch shows the bundled start screen in its "no connection" state,
     * which has the app's look and a working bridge. Cleared by "Tentar de
     * novo" or a new address.
     */
    static volatile String offline = null;

    private Connection() {}

    static SharedPreferences prefs(Context c) {
        return c.getSharedPreferences(PREFS, Context.MODE_PRIVATE);
    }

    /** The saved address (or the build default), "" when none. */
    static String url(Context c) {
        String saved = prefs(c).getString("url." + MODE, "");
        return saved.isEmpty() ? BuildConfig.DOMAIN_URL : saved;
    }

    /** The server to talk to in the background (notifications), or null. */
    static String savedUrl(Context c) {
        if (!MODE.equals(prefs(c).getString("mode", ""))) return null;
        String u = url(c);
        return u.startsWith("https://") ? u.replaceAll("/+$", "") : null;
    }

    /** The server URL for this launch, or null for the start screen. */
    static String serverUrl(Context c) {
        return offline != null ? null : savedUrl(c);
    }

    /**
     * One-time clean-up of the removed paths: a phone that used the
     * Tailscale VPN, the embedded tailnet or Funnel goes back to the start
     * screen, and the embedded node's state (keys, logs) is deleted.
     */
    static void migrate(Context c) {
        SharedPreferences p = prefs(c);
        if (p.getBoolean("migrated.v17", false)) return;
        String mode = p.getString("mode", "");
        SharedPreferences.Editor e = p.edit()
                .remove("tailnet.enrolled").remove("tailnet.hostname").remove("lanUrl")
                .remove("url.tailscale").remove("url.funnel")
                .putBoolean("migrated.v17", true);
        if (!mode.isEmpty() && !MODE.equals(mode)) e.putString("mode", "");
        e.apply();
        deleteTree(new File(c.getFilesDir(), "tailnet"));
    }

    private static void deleteTree(File f) {
        File[] kids = f.listFiles();
        if (kids != null) for (File k : kids) deleteTree(k);
        //noinspection ResultOfMethodCallIgnored
        f.delete();
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
            // Failures are handled natively (MainActivity), not by an error page.
            server.remove("errorPath");
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
}
