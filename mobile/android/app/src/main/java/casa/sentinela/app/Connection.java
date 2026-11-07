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

    /**
     * The certificate pin (SHA-256, hex) saved with the address, or the
     * build's default pin while the address is the build's default; ""
     * means the server's certificate must be publicly trusted.
     */
    static String pin(Context c) {
        SharedPreferences p = prefs(c);
        String saved = p.getString("url." + MODE, "").replaceAll("/+$", "");
        String savedPin = p.getString("pin." + MODE, "");
        if (!savedPin.isEmpty()) return savedPin;
        // Installs from before pairing codes saved the address without a
        // pin: the build's pin still applies to the build's own address.
        if (saved.isEmpty() || saved.equals(BuildConfig.DOMAIN_URL.replaceAll("/+$", ""))) {
            return BuildConfig.PUBLIC_CERT_SHA256;
        }
        return "";
    }

    static final java.util.regex.Pattern URL = java.util.regex.Pattern.compile("https://[A-Za-z0-9.-]+(:\\d{1,5})?/?");
    static final java.util.regex.Pattern PIN = java.util.regex.Pattern.compile("[0-9a-fA-F]{64}");

    /**
     * The house's other ways in (Tailscale, public address...), as the house
     * itself listed them over the current, verified connection
     * (/api/app/pairing). JSON array of {kind, url, pin}.
     */
    static org.json.JSONArray alternatives(Context c) {
        try {
            return new org.json.JSONArray(prefs(c).getString("alts", "[]"));
        } catch (org.json.JSONException e) {
            return new org.json.JSONArray();
        }
    }

    /** Remembers the house's current ways in (only well-formed entries). */
    static void saveAlternatives(Context c, org.json.JSONArray options) {
        org.json.JSONArray keep = new org.json.JSONArray();
        for (int i = 0; i < options.length(); i++) {
            org.json.JSONObject o = options.optJSONObject(i);
            if (o == null) continue;
            String u = o.optString("url", ""), p = o.optString("pin", "");
            if (!URL.matcher(u).matches() || (!p.isEmpty() && !PIN.matcher(p).matches())) continue;
            try {
                keep.put(new org.json.JSONObject().put("kind", o.optString("kind", "")).put("url", u).put("pin", p.toLowerCase(java.util.Locale.ROOT)));
            } catch (org.json.JSONException ignored) {
            }
        }
        prefs(c).edit().putString("alts", keep.toString()).apply();
    }

    /** Switches to one of the house's remembered ways in; error or null. */
    static String useAlternative(Context c, String url) {
        org.json.JSONArray alts = alternatives(c);
        for (int i = 0; i < alts.length(); i++) {
            org.json.JSONObject o = alts.optJSONObject(i);
            if (o != null && o.optString("url").equals(url)) return save(c, url, o.optString("pin", ""));
        }
        return c.getString(R.string.err_unknown_address);
    }

    /**
     * Saves a server and its pin ("" = publicly trusted certificate).
     * Returns an error message, or null when saved.
     */
    static String save(Context c, String url, String pin) {
        if (url == null || !URL.matcher(url).matches()) {
            return c.getString(R.string.err_https_address);
        }
        pin = pin == null ? "" : pin.replace(":", "").trim();
        if (!pin.isEmpty() && !PIN.matcher(pin).matches()) {
            return c.getString(R.string.err_bad_pin);
        }
        offline = null;
        boolean known = false;
        org.json.JSONArray alts = alternatives(c);
        for (int i = 0; i < alts.length(); i++) {
            org.json.JSONObject o = alts.optJSONObject(i);
            if (o != null && o.optString("url").equals(url.replaceAll("/+$", ""))) known = true;
        }
        SharedPreferences.Editor ed = prefs(c).edit();
        if (!known) ed.remove("alts");
        ed.apply();
        prefs(c).edit()
                .putString("mode", MODE)
                .putString("url." + MODE, url.replaceAll("/+$", ""))
                .putString("pin." + MODE, pin.toLowerCase(java.util.Locale.ROOT))
                .apply();
        return null;
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
        // Pairing codes (v18): a phone that uses the build's default server
        // keeps it, with its pin, so a later generic APK (no default) still
        // knows the house.
        if (!p.getBoolean("migrated.v18", false)) {
            SharedPreferences.Editor e = p.edit().putBoolean("migrated.v18", true);
            String url = p.getString("url." + MODE, "");
            if (MODE.equals(p.getString("mode", "")) && url.isEmpty() && !BuildConfig.DOMAIN_URL.isEmpty()) {
                e.putString("url." + MODE, BuildConfig.DOMAIN_URL.replaceAll("/+$", ""))
                        .putString("pin." + MODE, BuildConfig.PUBLIC_CERT_SHA256);
            } else if (!url.isEmpty() && p.getString("pin." + MODE, "").isEmpty()
                    && url.equals(BuildConfig.DOMAIN_URL.replaceAll("/+$", ""))) {
                e.putString("pin." + MODE, BuildConfig.PUBLIC_CERT_SHA256);
            }
            e.apply();
        }
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
