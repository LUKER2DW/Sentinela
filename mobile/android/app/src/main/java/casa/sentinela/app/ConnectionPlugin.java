package casa.sentinela.app;

import android.app.Activity;

import com.getcapacitor.JSObject;
import com.getcapacitor.Plugin;
import com.getcapacitor.PluginCall;
import com.getcapacitor.PluginMethod;
import com.getcapacitor.annotation.CapacitorPlugin;

/**
 * The start screen's bridge ({@code Capacitor.Plugins.SentinelaConnection}):
 * read and set the house's address, retry after "no connection", and come
 * back to the start screen from the Sentinela pages.
 */
@CapacitorPlugin(name = "SentinelaConnection")
public class ConnectionPlugin extends Plugin {

    @PluginMethod
    public void get(PluginCall call) {
        android.content.Context c = getContext();
        JSObject r = new JSObject();
        r.put("mode", Connection.prefs(c).getString("mode", ""));
        r.put("url", Connection.url(c));
        r.put("version", BuildConfig.VERSION_NAME);
        r.put("offline", Connection.offline != null);
        r.put("offlineWhy", Connection.offline == null ? "" : Connection.offline);
        call.resolve(r);
    }

    /** "Tentar de novo" on the no-connection screen: reload on the same address. */
    @PluginMethod
    public void retry(PluginCall call) {
        Connection.offline = null;
        call.resolve();
        restart();
    }

    /** Saves {url} and reloads the app on it. */
    @PluginMethod
    public void choose(PluginCall call) {
        String url = call.getString("url", "");
        if (url == null || !url.matches("https://[A-Za-z0-9.-]+(:\\d{1,5})?/?")) {
            call.reject("Use um endereço https:// (só o IP ou o nome e a porta, sem caminho).");
            return;
        }
        Connection.offline = null;
        Connection.prefs(getContext()).edit()
                .putString("mode", Connection.MODE)
                .putString("url." + Connection.MODE, url.replaceAll("/+$", ""))
                .apply();
        call.resolve();
        restart();
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
}
