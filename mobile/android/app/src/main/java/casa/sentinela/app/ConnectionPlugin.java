package casa.sentinela.app;

import android.app.Activity;
import android.content.SharedPreferences;

import com.getcapacitor.JSObject;
import com.getcapacitor.Plugin;
import com.getcapacitor.PluginCall;
import com.getcapacitor.PluginMethod;
import com.getcapacitor.annotation.CapacitorPlugin;

import java.io.File;

import casa.sentinela.tailnet.PairResult;
import casa.sentinela.tailnet.Tailnet;

/**
 * The start screen's bridge ({@code Capacitor.Plugins.SentinelaConnection},
 * ADR-0019): read and pick the connection path, pair the embedded tailnet
 * node, and come back to the start screen from the Sentinela pages.
 */
@CapacitorPlugin(name = "SentinelaConnection")
public class ConnectionPlugin extends Plugin {

    @PluginMethod
    public void get(PluginCall call) {
        android.content.Context c = getContext();
        JSObject r = new JSObject();
        r.put("mode", Connection.mode(c));
        JSObject urls = new JSObject();
        for (String m : new String[]{"tailscale", "domain", "funnel"}) urls.put(m, Connection.url(c, m));
        r.put("urls", urls);
        r.put("tailnetHost", Connection.tailnetHost());
        r.put("lanUrl", Connection.prefs(c).getString("lanUrl", BuildConfig.LAN_URL));
        r.put("tailnetEnrolled", Connection.tailnetEnrolled(c));
        // Touching Tailnet loads the Go runtime (memory): only once paired.
        r.put("tailnetStatus", Connection.tailnetEnrolled(c) ? Tailnet.status() : "");
        r.put("tailnetHostname", Connection.prefs(c).getString("tailnet.hostname", ""));
        r.put("version", BuildConfig.VERSION_NAME);
        r.put("offline", Connection.offline != null);
        r.put("offlineWhy", Connection.offline == null ? "" : Connection.offline);
        call.resolve(r);
    }

    /** "Tentar de novo" on the no-connection screen: reload on the same path. */
    @PluginMethod
    public void retry(PluginCall call) {
        Connection.offline = null;
        call.resolve();
        restart();
    }

    /** Saves {mode, url?} and reloads the app on it. */
    @PluginMethod
    public void choose(PluginCall call) {
        String mode = call.getString("mode", "");
        String url = call.getString("url", "");
        switch (mode) {
            case "tailscale":
            case "domain":
            case "funnel":
                if (url == null || !url.matches("https://[A-Za-z0-9.-]+(:\\d{1,5})?/?")) {
                    call.reject("Use um endereço https:// (só o nome ou o IP, sem caminho).");
                    return;
                }
                break;
            case "tailnet":
                if (!Connection.tailnetEnrolled(getContext())) {
                    call.reject("Pareie este celular primeiro.");
                    return;
                }
                break;
            default:
                call.reject("Modo desconhecido.");
                return;
        }
        Connection.offline = null;
        SharedPreferences.Editor e = Connection.prefs(getContext()).edit().putString("mode", mode);
        if (!mode.equals("tailnet")) e.putString("url." + mode, url.replaceAll("/+$", ""));
        e.apply();
        call.resolve();
        restart();
    }

    /**
     * Pairs the embedded node: {lanUrl, username, password} asks the server
     * at home for a single-use key; {key} uses one pasted by hand.
     */
    @PluginMethod
    public void pair(PluginCall call) {
        String lanUrl = call.getString("lanUrl", "");
        String user = call.getString("username", "");
        String pass = call.getString("password", "");
        String key = call.getString("key", "");
        new Thread(() -> {
            try {
                SharedPreferences p = Connection.prefs(getContext());
                String hostname = "sentinela-app";
                if (key == null || key.isEmpty()) {
                    PairResult pr = Tailnet.pair(lanUrl, user, pass);
                    p.edit().putString("lanUrl", lanUrl).apply();
                    hostname = pr.getHostname();
                    p.edit().putString("tailnet.hostname", hostname).apply();
                    Connection.startTailnet(getContext(), pr.getKey());
                } else {
                    if (!key.startsWith("tskey-")) throw new Exception("a chave começa com tskey-");
                    p.edit().putString("tailnet.hostname", hostname).apply();
                    Connection.startTailnet(getContext(), key.trim());
                }
                Tailnet.waitUp(90_000);
                p.edit().putBoolean("tailnet.enrolled", true).putString("mode", "tailnet").apply();
                Connection.offline = null;
                call.resolve();
                restart();
            } catch (Exception ex) {
                String msg = ex.getMessage() == null ? "falhou" : ex.getMessage();
                call.reject(msg);
            }
        }, "sentinela-pair").start();
    }

    /** Stops the embedded node and deletes its identity from this phone. */
    @PluginMethod
    public void forget(PluginCall call) {
        Tailnet.stop();
        deleteTree(Connection.tailnetDir(getContext()));
        Connection.prefs(getContext()).edit()
                .remove("tailnet.enrolled").remove("tailnet.hostname").putString("mode", "").apply();
        call.resolve();
    }

    /** Back to the start screen (from the Sentinela pages or the offline page). */
    @PluginMethod
    public void change(PluginCall call) {
        Connection.offline = null;
        Connection.prefs(getContext()).edit().putString("mode", "").apply();
        call.resolve();
        restart();
    }

    private void restart() {
        Activity a = getActivity();
        if (a != null) a.runOnUiThread(a::recreate);
    }

    private static void deleteTree(File f) {
        File[] kids = f.listFiles();
        if (kids != null) for (File k : kids) deleteTree(k);
        //noinspection ResultOfMethodCallIgnored
        f.delete();
    }
}
