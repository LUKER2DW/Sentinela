package casa.sentinela.app;

import android.app.Notification;
import android.app.Service;
import android.content.Context;
import android.content.Intent;
import android.content.SharedPreferences;
import android.content.pm.ServiceInfo;
import android.net.ConnectivityManager;
import android.net.Network;
import android.os.Build;
import android.os.IBinder;
import android.os.SystemClock;
import android.webkit.CookieManager;

import androidx.annotation.NonNull;
import androidx.core.app.NotificationCompat;
import androidx.core.app.NotificationManagerCompat;
import androidx.core.app.ServiceCompat;
import androidx.core.content.ContextCompat;

import org.json.JSONObject;

import java.io.BufferedReader;
import java.io.InputStreamReader;
import java.net.HttpURLConnection;
import java.nio.charset.StandardCharsets;

/**
 * Fallback notification transport on phones without working Firebase: a
 * foreground service keeps one Server-Sent Events connection to the house
 * ({@code GET /api/notify/stream}) and shows every message it gets. This is
 * how self-hosted ntfy delivers "instantly" too; Android requires the small
 * permanent notification of the {@link Notifier#CHANNEL_SERVICE} channel.
 * The session cookie is copied from the WebView (MainActivity refreshes it
 * on every resume): the service never loads a WebView itself.
 */
public class NotifyService extends Service {
    private static final String PREFS = "notify";
    private static final int ONGOING_ID = 7;
    private static final int RELOGIN_ID = 8;
    private static final long MIN_BACKOFF = 2_000, MAX_BACKOFF = 60_000;

    /** True while the worker is connected to the server. */
    static volatile boolean connected = false;
    static volatile boolean alive = false;

    private volatile boolean running = false;
    private volatile HttpURLConnection conn;
    private Thread worker;
    private final Object wake = new Object();
    private ConnectivityManager.NetworkCallback netCb;

    // ---------- control ----------

    static SharedPreferences prefs(Context c) {
        return c.getSharedPreferences(PREFS, Context.MODE_PRIVATE);
    }

    static boolean enabled(Context c) {
        return prefs(c).getBoolean("enabled", false);
    }

    static void setEnabled(Context c, boolean on) {
        SharedPreferences.Editor e = prefs(c).edit().putBoolean("enabled", on);
        if (!on) e.remove("cookie");
        e.apply();
        if (on) start(c);
        else c.stopService(new Intent(c, NotifyService.class));
    }

    /** Firebase is ready: stop the foreground service, retain the session
     * cookie so Firebase's short-lived worker can fetch event details. */
    static void stopForFCM(Context c) {
        prefs(c).edit().putBoolean("enabled", false).apply();
        c.stopService(new Intent(c, NotifyService.class));
    }

    /** Starts (or pokes) the service when notifications are on. */
    static void start(Context c) {
        if (!enabled(c) || !Notifier.allowed(c) || Connection.savedUrl(c) == null || cookie(c) == null) return;
        try {
            ContextCompat.startForegroundService(c, new Intent(c, NotifyService.class));
        } catch (RuntimeException e) {
            // Android 12+ refuses starts from the background; the next app
            // launch or boot starts it again.
        }
    }

    /** Copies the WebView's session cookie for the server (UI thread). */
    static void refreshCookie(Context c) {
        String server = Connection.savedUrl(c);
        if (server == null) return;
        String ck;
        try {
            ck = CookieManager.getInstance().getCookie(server);
        } catch (RuntimeException e) {
            return; // WebView being updated
        }
        if (ck != null && ck.contains("sentinela_session=")) {
            SharedPreferences p = prefs(c);
            SharedPreferences.Editor edit = p.edit().putString("cookie", ck);
            // A fresh login has a new session. Register its FCM token now;
            // otherwise the old session's token is correctly gone from DB.
            if (!ck.equals(p.getString("cookie", null))) edit.putLong("fcm.last_attempt", 0);
            edit.apply();
            NotificationManagerCompat.from(c).cancel(RELOGIN_ID);
        }
    }

    static String cookie(Context c) {
        return prefs(c).getString("cookie", null);
    }

    // ---------- service ----------

    @Override
    public IBinder onBind(Intent intent) {
        return null;
    }

    @Override
    public void onCreate() {
        super.onCreate();
        Pin.init(this);
        Notifier.createChannels(this);
    }

    @Override
    public int onStartCommand(Intent intent, int flags, int startId) {
        // Within 5 s of startForegroundService, whatever happens next.
        try {
            ServiceCompat.startForeground(this, ONGOING_ID, ongoing(),
                    Build.VERSION.SDK_INT >= 34 ? ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE : 0);
        } catch (RuntimeException e) {
            stopSelf();
            return START_NOT_STICKY;
        }
        if (!enabled(this)) {
            stopSelf();
            return START_NOT_STICKY;
        }
        alive = true;
        if (worker == null || !worker.isAlive()) {
            running = true;
            worker = new Thread(this::loop, "sentinela-notify");
            worker.start();
            watchNetwork();
        } else {
            poke();
        }
        return START_STICKY;
    }

    @Override
    public void onDestroy() {
        running = false;
        alive = false;
        connected = false;
        HttpURLConnection c = conn;
        if (c != null) c.disconnect(); // unblocks the reader
        if (worker != null) worker.interrupt();
        if (netCb != null) {
            ConnectivityManager cm = (ConnectivityManager) getSystemService(Context.CONNECTIVITY_SERVICE);
            try {
                if (cm != null) cm.unregisterNetworkCallback(netCb);
            } catch (RuntimeException ignored) {
                // already gone
            }
            netCb = null;
        }
        super.onDestroy();
    }

    private Notification ongoing() {
        return new NotificationCompat.Builder(this, Notifier.CHANNEL_SERVICE)
                .setSmallIcon(R.drawable.ic_stat_sentinela)
                .setContentTitle("Conectado à casa")
                .setContentText("Toque para ocultar este aviso; os alertas continuam")
                .setPriority(NotificationCompat.PRIORITY_MIN)
                .setSilent(true)
                .setShowWhen(false)
                .setVisibility(NotificationCompat.VISIBILITY_SECRET)
                .setContentIntent(Notifier.serviceChannelSettings(this, ONGOING_ID))
                .build();
    }

    /** A new network: reconnect now instead of waiting for the backoff. */
    private void watchNetwork() {
        ConnectivityManager cm = (ConnectivityManager) getSystemService(Context.CONNECTIVITY_SERVICE);
        if (cm == null || netCb != null) return;
        netCb = new ConnectivityManager.NetworkCallback() {
            @Override
            public void onAvailable(@NonNull Network network) {
                poke();
            }
        };
        try {
            cm.registerDefaultNetworkCallback(netCb);
        } catch (RuntimeException e) {
            netCb = null; // too many callbacks: the backoff still reconnects
        }
    }

    private void poke() {
        synchronized (wake) {
            wake.notifyAll();
        }
    }

    private void loop() {
        long backoff = MIN_BACKOFF;
        while (running) {
            String server = Connection.savedUrl(this);
            String ck = cookie(this);
            if (server == null || ck == null) {
                stopSelf();
                return;
            }
            long started = SystemClock.elapsedRealtime();
            int code = stream(server, ck);
            if (!running) return;
            // 401/403: signed out, password changed, account disabled. 404: the
            // public listener hides everything from a session it does not honour.
            if (code == 401 || code == 403 || code == 404) {
                askRelogin();
                prefs(this).edit().remove("cookie").apply();
                stopSelf();
                return;
            }
            if (SystemClock.elapsedRealtime() - started > 60_000) backoff = MIN_BACKOFF;
            synchronized (wake) {
                try {
                    wake.wait(backoff);
                } catch (InterruptedException e) {
                    return;
                }
            }
            backoff = Math.min(backoff * 2, MAX_BACKOFF);
        }
    }

    /** One connection; returns the HTTP status, or -1 when the network failed. */
    private int stream(String server, String ck) {
        HttpURLConnection c = null;
        try {
            c = Pin.open(server + "/api/notify/stream");
            conn = c;
            c.setConnectTimeout(15_000);
            c.setReadTimeout(120_000); // the server pings every 45 s
            c.setUseCaches(false);
            c.setRequestProperty("Cookie", ck);
            c.setRequestProperty("Accept", "text/event-stream");
            c.setRequestProperty("User-Agent", "SentinelaApp/1 (notifications; Android " + Build.VERSION.RELEASE + ")");
            int code = c.getResponseCode();
            if (code != 200) return code;
            connected = true;
            try (BufferedReader in = new BufferedReader(new InputStreamReader(c.getInputStream(), StandardCharsets.UTF_8))) {
                String event = "";
                StringBuilder data = new StringBuilder();
                for (String line; running && (line = in.readLine()) != null; ) {
                    if (line.isEmpty()) {
                        if ("notify".equals(event) && data.length() > 0) deliver(server, ck, data.toString());
                        event = "";
                        data.setLength(0);
                    } else if (line.startsWith("event:")) {
                        event = line.substring(6).trim();
                    } else if (line.startsWith("data:") && data.length() < 64 * 1024) {
                        data.append(line.substring(5).trim());
                    }
                }
            }
            return 200;
        } catch (Exception e) {
            return -1;
        } finally {
            connected = false;
            conn = null;
            if (c != null) c.disconnect();
        }
    }

    private void deliver(String server, String ck, String json) {
        try {
            JSONObject m = new JSONObject(json);
            // The snapshot download must not hold up the stream.
            new Thread(() -> Notifier.show(getApplicationContext(), m, server, ck), "notify-show").start();
        } catch (Exception ignored) {
            // malformed message: nothing to show
        }
    }

    private void askRelogin() {
        if (!Notifier.allowed(this)) return;
        NotificationCompat.Builder b = new NotificationCompat.Builder(this, Notifier.CHANNEL)
                .setSmallIcon(R.drawable.ic_stat_sentinela)
                .setContentTitle("Avisos do Sentinela pausados")
                .setContentText("A sessão deste celular terminou. Abra o app e entre de novo.")
                .setAutoCancel(true)
                .setContentIntent(Notifier.openApp(this, null, RELOGIN_ID));
        try {
            NotificationManagerCompat.from(this).notify(RELOGIN_ID, b.build());
        } catch (SecurityException ignored) {
            // permission revoked meanwhile
        }
    }
}
