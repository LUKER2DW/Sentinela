package casa.sentinela.app;

import android.app.Activity;

import com.getcapacitor.JSObject;
import com.getcapacitor.Plugin;
import com.getcapacitor.PluginCall;
import com.getcapacitor.PluginMethod;
import com.getcapacitor.annotation.CapacitorPlugin;
import com.google.mlkit.vision.barcode.common.Barcode;
import com.google.mlkit.vision.codescanner.GmsBarcodeScannerOptions;
import com.google.mlkit.vision.codescanner.GmsBarcodeScanning;

/**
 * The start screen's bridge ({@code Capacitor.Plugins.SentinelaConnection}):
 * read and set the house's address, retry after "no connection", and come
 * back to the start screen from the Sentinela pages.
 */
@CapacitorPlugin(name = "SentinelaConnection")
public class ConnectionPlugin extends Plugin {

    /**
     * Reads the home's pairing QR code (System -> App) with Google's code
     * scanner and returns its text ({code}, "" when cancelled). The page puts
     * it in the address field, so the person sees the address and presses
     * Connect, as with a pasted code.
     */
    @PluginMethod
    public void scan(PluginCall call) {
        GmsBarcodeScannerOptions opts = new GmsBarcodeScannerOptions.Builder()
                .setBarcodeFormats(Barcode.FORMAT_QR_CODE)
                .build();
        GmsBarcodeScanning.getClient(getActivity(), opts).startScan()
                .addOnSuccessListener(b -> {
                    JSObject r = new JSObject();
                    r.put("code", b.getRawValue() == null ? "" : b.getRawValue());
                    call.resolve(r);
                })
                .addOnCanceledListener(() -> {
                    JSObject r = new JSObject();
                    r.put("code", "");
                    call.resolve(r);
                })
                .addOnFailureListener(e -> call.reject(getContext().getString(R.string.scan_unavailable)));
    }

    @PluginMethod
    public void get(PluginCall call) {
        android.content.Context c = getContext();
        JSObject r = new JSObject();
        r.put("mode", Connection.prefs(c).getString("mode", ""));
        r.put("url", Connection.url(c));
        r.put("pinned", !Connection.pin(c).isEmpty());
        com.getcapacitor.JSArray alts = new com.getcapacitor.JSArray();
        org.json.JSONArray saved = Connection.alternatives(c);
        String current = Connection.url(c);
        for (int i = 0; i < saved.length(); i++) {
            org.json.JSONObject o = saved.optJSONObject(i);
            if (o == null || o.optString("url").equals(current)) continue;
            JSObject a = new JSObject();
            a.put("kind", o.optString("kind"));
            a.put("url", o.optString("url"));
            alts.put(a);
        }
        r.put("alternatives", alts);
        r.put("version", BuildConfig.VERSION_NAME);
        r.put("offline", Connection.offline != null);
        r.put("offlineWhy", Connection.offline == null ? "" : Connection.offline);
        call.resolve(r);
    }

    /** "Try again" on the no-connection screen: reload on the same address. */
    @PluginMethod
    public void retry(PluginCall call) {
        Connection.offline = null;
        call.resolve();
        restart();
    }

    /**
     * Saves {url, pin} and reloads the app on it. The pin (certificate
     * SHA-256) comes from the house's pairing code; without one the server
     * must have a publicly trusted certificate (a domain, Tailscale HTTPS).
     */
    @PluginMethod
    public void choose(PluginCall call) {
        String err = Connection.save(getContext(), call.getString("url", ""), call.getString("pin", ""));
        if (err != null) {
            call.reject(err);
            return;
        }
        call.resolve();
        restart();
    }

    /** Uses another of the house's remembered addresses (offline screen). */
    @PluginMethod
    public void useAlternative(PluginCall call) {
        String err = Connection.useAlternative(getContext(), call.getString("url", ""));
        if (err != null) {
            call.reject(err);
            return;
        }
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
