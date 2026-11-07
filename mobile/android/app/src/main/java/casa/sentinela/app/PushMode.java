package casa.sentinela.app;

import android.content.Context;
import android.content.SharedPreferences;
import android.os.Handler;
import android.os.Looper;

import com.google.android.gms.tasks.Tasks;
import com.google.firebase.FirebaseApp;
import com.google.firebase.FirebaseOptions;
import com.google.firebase.messaging.FirebaseMessaging;

import org.json.JSONObject;

import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.nio.charset.StandardCharsets;
import java.util.concurrent.TimeUnit;
import java.util.function.Consumer;

/** Firebase when it works, the existing foreground connection otherwise. */
final class PushMode {
    private static final long RETRY_AFTER_MS = 10 * 60_000L;
    private static final long RECHECK_AFTER_MS = 6 * 60 * 60_000L;

    private PushMode() {}

    /**
     * Firebase for the paired house's own project. The APK carries no
     * project: the server hands its public client values (GET
     * /api/notify/fcm), so every house uses its own Firebase.
     */
    private static FirebaseOptions options(JSONObject client) {
        if (client == null) return null;
        String project = client.optString("project_id"), app = client.optString("app_id");
        String key = client.optString("api_key"), sender = client.optString("sender_id");
        if (project.isEmpty() || app.isEmpty() || key.isEmpty() || sender.isEmpty()) return null;
        FirebaseOptions.Builder b = new FirebaseOptions.Builder()
                .setProjectId(project).setApplicationId(app).setApiKey(key).setGcmSenderId(sender);
        String bucket = client.optString("storage_bucket");
        if (!bucket.isEmpty()) b.setStorageBucket(bucket);
        return b.build();
    }

    /** Starts (or switches) the default Firebase app to these options; false when unusable. */
    private static synchronized boolean startFirebase(Context c, FirebaseOptions want) {
        if (want == null) return false;
        try {
            for (FirebaseApp a : FirebaseApp.getApps(c)) {
                if (!FirebaseApp.DEFAULT_APP_NAME.equals(a.getName())) continue;
                if (a.getOptions().equals(want)) return true;
                a.delete(); // paired with another house since: its project goes
            }
            FirebaseApp.initializeApp(c.getApplicationContext(), want);
            return true;
        } catch (RuntimeException e) {
            return false;
        }
    }

    /** Process start: Firebase with the house config saved at the last registration. */
    static void startSavedFirebase(Context c) {
        String saved = NotifyService.prefs(c).getString("fcm.client", "");
        if (saved.isEmpty()) return;
        try {
            startFirebase(c, options(new JSONObject(saved)));
        } catch (Exception ignored) {
            // corrupt preference: the next registration writes it again
        }
    }

    static boolean isFirebase(Context c) {
        return NotifyService.prefs(c).getBoolean("fcm.enabled", false);
    }

    static boolean enabled(Context c) {
        return isFirebase(c) || NotifyService.enabled(c);
    }

    /** Called after Android's notification permission was granted. */
    static void enable(Context c, Consumer<Boolean> done) {
        Context app = c.getApplicationContext();
        new Thread(() -> {
            boolean firebase = tryRegister(app, null);
            if (!firebase) fallback(app);
            new Handler(Looper.getMainLooper()).post(() -> done.accept(firebase));
        }, "notify-enable").start();
    }

    /** Check for token changes and switch a direct connection to Firebase. */
    static void reconcile(Context c) {
        if (!enabled(c) || !Notifier.allowed(c) || NotifyService.cookie(c) == null) return;
        SharedPreferences p = NotifyService.prefs(c);
        long gap = isFirebase(c) ? RECHECK_AFTER_MS : RETRY_AFTER_MS;
        if (System.currentTimeMillis() - p.getLong("fcm.last_attempt", 0) < gap) return;
        Context app = c.getApplicationContext();
        new Thread(() -> {
            if (!tryRegister(app, null)) fallback(app);
        }, "notify-reconcile").start();
    }

    static void refreshedToken(Context c, String token) {
        if (!enabled(c)) return;
        if (!tryRegister(c.getApplicationContext(), token)) fallback(c);
    }

    private static synchronized void fallback(Context c) {
        SharedPreferences p = NotifyService.prefs(c);
        String token = p.getString("fcm.token", "");
        String server = Connection.savedUrl(c);
        String cookie = NotifyService.cookie(c);
        p.edit().putBoolean("fcm.enabled", false).apply();
        try {
            FirebaseMessaging.getInstance().setAutoInitEnabled(false);
        } catch (RuntimeException ignored) {
            // Firebase may be unavailable on this phone.
        }
        NotifyService.setEnabled(c, true);
        if (!token.isEmpty() && unregister(server, cookie, token)) {
            p.edit().remove("fcm.token").apply();
        }
    }

    /** Worker thread only; never emit the token or cookie in a log. */
    private static synchronized boolean tryRegister(Context c, String freshToken) {
        if (BuildConfig.DEBUG) return false; // .dev has no Firebase registration
        SharedPreferences p = NotifyService.prefs(c);
        p.edit().putLong("fcm.last_attempt", System.currentTimeMillis()).apply();
        String server = Connection.savedUrl(c);
        String cookie = NotifyService.cookie(c);
        if (server == null || cookie == null || !Notifier.allowed(c)) return false;
        try {
            JSONObject status = request(server, "/api/notify/fcm", "GET", cookie, null, null);
            if (!status.optBoolean("available", false)) return false;
            JSONObject client = status.optJSONObject("client");
            if (!startFirebase(c, options(client))) return false;
            p.edit().putString("fcm.client", client.toString()).apply();
            FirebaseMessaging firebase = FirebaseMessaging.getInstance();
            firebase.setAutoInitEnabled(true);
            String token = freshToken != null ? freshToken : Tasks.await(firebase.getToken(), 8, TimeUnit.SECONDS);
            if (token == null || token.isEmpty()) return false;
            JSONObject me = request(server, "/api/me", "GET", cookie, null, null);
            String csrf = me.optString("csrf", "");
            if (csrf.isEmpty()) return false;
            request(server, "/api/notify/fcm/token", "PUT", cookie, csrf, new JSONObject().put("token", token));
            p.edit().putString("fcm.token", token).putBoolean("fcm.enabled", true)
                    .putLong("fcm.last_attempt", System.currentTimeMillis()).apply();
            NotifyService.stopForFCM(c); // keeps the cookie for the wake-up fetch
            return true;
        } catch (Exception ignored) {
            return false;
        }
    }

    static void disable(Context c) {
        Context app = c.getApplicationContext();
        SharedPreferences p = NotifyService.prefs(c);
        String token = p.getString("fcm.token", "");
        String server = Connection.savedUrl(c);
        String cookie = NotifyService.cookie(c);
        p.edit().putBoolean("fcm.enabled", false).remove("fcm.token").apply();
        NotifyService.setEnabled(c, false);
        new Thread(() -> {
            unregister(server, cookie, token);
            try {
                FirebaseMessaging.getInstance().setAutoInitEnabled(false);
                Tasks.await(FirebaseMessaging.getInstance().deleteToken(), 5, TimeUnit.SECONDS);
            } catch (Exception ignored) {
                // The local flag already prevents showing any FCM message.
            }
        }, "notify-disable").start();
    }

    private static boolean unregister(String server, String cookie, String token) {
        if (server == null || cookie == null || token.isEmpty()) return false;
        try {
            String csrf = request(server, "/api/me", "GET", cookie, null, null).optString("csrf", "");
            if (csrf.isEmpty()) return false;
            request(server, "/api/notify/fcm/token", "DELETE", cookie, csrf, new JSONObject().put("token", token));
            return true;
        } catch (Exception ignored) {
            // Session deletion also removes the server token.
            return false;
        }
    }

    /** The Google message contains only id; fetch the actual text and image. */
    static void deliver(Context c, String id) {
        if (!isFirebase(c) || !Notifier.allowed(c) || !id.matches("[A-Za-z0-9_-]{24}")) return;
        String server = Connection.savedUrl(c);
        String cookie = NotifyService.cookie(c);
        if (server == null || cookie == null) return;
        JSONObject message;
        try {
            message = request(server, "/api/notify/pending/" + id, "GET", cookie, null, null);
        } catch (Exception ignored) {
            // A visible generic notice preserves the alert when the house is
            // briefly unreachable (and keeps FCM high-priority eligibility).
            message = new JSONObject();
            try {
                message.put("title", c.getString(R.string.pending_title));
                message.put("body", c.getString(R.string.pending_text));
                message.put("url", "/#/events");
                message.put("tag", "sentinela-" + id);
            } catch (Exception impossible) {
                return;
            }
        }
        Notifier.show(c, message, server, cookie);
    }

    /** All network calls use the app's pinned HTTPS connection and session. */
    private static JSONObject request(String server, String path, String method, String cookie, String csrf, JSONObject body) throws Exception {
        HttpURLConnection conn = Pin.open(server + path);
        try {
            conn.setConnectTimeout(5_000);
            conn.setReadTimeout(8_000);
            conn.setRequestMethod(method);
            conn.setUseCaches(false);
            conn.setRequestProperty("Cookie", cookie);
            conn.setRequestProperty("Accept", "application/json");
            if (csrf != null) conn.setRequestProperty("X-CSRF-Token", csrf);
            if (body != null) {
                byte[] bytes = body.toString().getBytes(StandardCharsets.UTF_8);
                conn.setDoOutput(true);
                conn.setRequestProperty("Content-Type", "application/json");
                conn.setFixedLengthStreamingMode(bytes.length);
                try (OutputStream out = conn.getOutputStream()) {
                    out.write(bytes);
                }
            }
            int status = conn.getResponseCode();
            if (status != 200) throw new IOException("HTTP " + status);
            try (InputStream in = conn.getInputStream(); ByteArrayOutputStream out = new ByteArrayOutputStream()) {
                byte[] buf = new byte[2048];
                for (int n; (n = in.read(buf)) > 0; ) {
                    out.write(buf, 0, n);
                    if (out.size() > 32 * 1024) throw new IOException("response too large");
                }
                return new JSONObject(out.toString(StandardCharsets.UTF_8.name()));
            }
        } finally {
            conn.disconnect();
        }
    }
}
