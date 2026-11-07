package casa.sentinela.app;

import android.content.Context;
import android.content.Intent;
import android.net.Uri;
import android.os.Build;
import android.provider.Settings;
import android.webkit.CookieManager;

import androidx.core.content.FileProvider;

import com.getcapacitor.JSObject;
import com.getcapacitor.Plugin;
import com.getcapacitor.PluginCall;
import com.getcapacitor.PluginMethod;
import com.getcapacitor.annotation.CapacitorPlugin;

import java.io.File;
import java.io.FileInputStream;
import java.io.FileOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.security.MessageDigest;
import java.util.Locale;

/**
 * In-app updates ({@code Capacitor.Plugins.SentinelaUpdate}). The APK is not
 * on the Play Store: the server publishes the newest one
 * ({@code GET /api/app/latest}, scripts/publish-apk.sh) and this plugin
 * downloads it over the same pinned HTTPS connection, checks its SHA-256
 * and hands it to Android's installer. Android itself refuses an APK
 * signed with another key, so a tampered file cannot replace the app.
 */
@CapacitorPlugin(name = "SentinelaUpdate")
public class UpdatePlugin extends Plugin {
    private volatile boolean busy = false;

    private File apk() {
        File dir = new File(getContext().getCacheDir(), "update");
        //noinspection ResultOfMethodCallIgnored
        dir.mkdirs();
        return new File(dir, "sentinela.apk");
    }

    private boolean canInstall() {
        return Build.VERSION.SDK_INT < Build.VERSION_CODES.O || getContext().getPackageManager().canRequestPackageInstalls();
    }

    /** {versionCode, versionName, canInstall} */
    @PluginMethod
    public void info(PluginCall call) {
        JSObject o = new JSObject();
        o.put("versionCode", BuildConfig.VERSION_CODE);
        o.put("versionName", BuildConfig.VERSION_NAME);
        o.put("canInstall", canInstall());
        call.resolve(o);
    }

    /**
     * Downloads {sha256, size} from the server's /api/app/sentinela.apk
     * (emitting "progress" {sent, total}) and opens the installer. Resolves
     * {state: "installing" | "permission"}: "permission" means Android's
     * "install unknown apps" screen was opened; call again after allowing.
     */
    @PluginMethod
    public void install(PluginCall call) {
        final String sha = call.getString("sha256", "").toLowerCase(Locale.ROOT);
        // Not call.getLong: Capacitor returns the default for JSON numbers
        // that fit an int (every APK size), which made 1.14/1.15 refuse.
        final long size = call.getData().optLong("size", 0L);
        String server = getBridge().getServerUrl();
        if (!sha.matches("[0-9a-f]{64}") || size <= 0 || server == null || !server.startsWith("https://")) {
            call.reject(getContext().getString(R.string.update_unavailable));
            return;
        }
        if (busy) {
            call.reject(getContext().getString(R.string.update_busy));
            return;
        }
        busy = true;
        final String url = server.replaceAll("/+$", "") + "/api/app/sentinela.apk";
        new Thread(() -> {
            try {
                File f = apk();
                if (!(f.length() == size && sha.equals(digest(f)))) download(url, f, size);
                if (!sha.equals(digest(f))) {
                    //noinspection ResultOfMethodCallIgnored
                    f.delete();
                    throw new IOException("checksum");
                }
                JSObject r = new JSObject();
                if (!canInstall()) {
                    // One-time switch: "Permitir desta fonte" for Sentinela.
                    Intent i = new Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES,
                            Uri.parse("package:" + getContext().getPackageName()));
                    i.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK);
                    getContext().startActivity(i);
                    r.put("state", "permission");
                } else {
                    openInstaller(f);
                    r.put("state", "installing");
                }
                call.resolve(r);
            } catch (Exception e) {
                call.reject(getContext().getString("checksum".equals(e.getMessage())
                        ? R.string.update_mismatch
                        : R.string.update_failed));
            } finally {
                busy = false;
            }
        }, "app-update").start();
    }

    private void download(String url, File f, long size) throws Exception {
        String cookie = CookieManager.getInstance().getCookie(url);
        HttpURLConnection c = Pin.open(url);
        try {
            c.setConnectTimeout(10000);
            c.setReadTimeout(60000);
            if (cookie != null) c.setRequestProperty("Cookie", cookie);
            if (c.getResponseCode() != 200) throw new IOException("HTTP " + c.getResponseCode());
            File tmp = new File(f.getPath() + ".part");
            long sent = 0;
            long last = 0;
            try (InputStream in = c.getInputStream(); OutputStream out = new FileOutputStream(tmp)) {
                byte[] buf = new byte[64 * 1024];
                for (int n; (n = in.read(buf)) > 0; ) {
                    out.write(buf, 0, n);
                    sent += n;
                    if (sent > size) throw new IOException("too large");
                    long now = System.currentTimeMillis();
                    if (now - last > 250 || sent == size) {
                        last = now;
                        JSObject p = new JSObject();
                        p.put("sent", sent);
                        p.put("total", size);
                        notifyListeners("progress", p);
                    }
                }
            }
            if (!tmp.renameTo(f)) throw new IOException("rename");
        } finally {
            c.disconnect();
        }
    }

    private void openInstaller(File f) {
        Context ctx = getContext();
        Uri uri = FileProvider.getUriForFile(ctx, ctx.getPackageName() + ".fileprovider", f);
        Intent i = new Intent(Intent.ACTION_VIEW);
        i.setDataAndType(uri, "application/vnd.android.package-archive");
        i.addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION | Intent.FLAG_ACTIVITY_NEW_TASK);
        ctx.startActivity(i);
    }

    private static String digest(File f) {
        if (!f.isFile()) return "";
        try (InputStream in = new FileInputStream(f)) {
            MessageDigest md = MessageDigest.getInstance("SHA-256");
            byte[] buf = new byte[64 * 1024];
            for (int n; (n = in.read(buf)) > 0; ) md.update(buf, 0, n);
            StringBuilder sb = new StringBuilder();
            for (byte b : md.digest()) sb.append(String.format(Locale.ROOT, "%02x", b));
            return sb.toString();
        } catch (Exception e) {
            return "";
        }
    }
}
