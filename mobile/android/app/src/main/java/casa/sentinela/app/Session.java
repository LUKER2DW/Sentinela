package casa.sentinela.app;

import android.content.Context;
import android.content.SharedPreferences;
import android.webkit.CookieManager;

import androidx.core.app.NotificationManagerCompat;

import com.google.android.gms.tasks.Tasks;
import com.google.firebase.messaging.FirebaseMessaging;

import java.io.IOException;
import java.net.HttpURLConnection;
import java.util.concurrent.TimeUnit;

/**
 * The session the native code uses (notifications, the wake-up fetch,
 * snapshots, pairing refresh, downloads, updates), bound to the server and
 * the certificate pin it belongs to.
 * - The notification code keeps a copy of the WebView's cookie (it runs
 *   without a WebView). The copy records the server and pin it was copied
 *   for and is only returned while the app is still paired with exactly
 *   those, so a session never goes to another house.
 * - Every request that carries a session goes through {@link Bound#open}:
 *   same origin as that server, TLS checked against that pin, no redirects.
 * - Pairing another house ({@link Connection#save}) drops the copy, the
 *   house's push registration and, before the next page loads, the
 *   WebView's cookies (they are per host, not per port or certificate).
 */
final class Session {
    private static final String COOKIE = "cookie", ORIGIN = "cookie.origin", PIN = "cookie.pin";
    /** Set by a house switch; MainActivity clears the WebView's cookies before the bridge loads. */
    private static final String WIPE_WEB = "session.wipe_web_cookies";
    static final int RELOGIN_ID = 8;

    private Session() {}

    /** A session cookie together with the one server (and pin) it may be sent to. */
    static final class Bound {
        final String server;
        final String pin;
        final String cookie;

        Bound(String server, String pin, String cookie) {
            this.server = Connection.trimSlashes(server);
            this.pin = pin == null ? "" : pin;
            this.cookie = cookie;
        }

        /** A connection to path on this server only, carrying the session. */
        HttpURLConnection open(String path) throws Exception {
            String url = AppLinks.onServer(server, path);
            if (url == null) throw new IOException("not a path on the paired server");
            return connect(url);
        }

        /** Same as open(), for an absolute URL that must be on this server (downloads). */
        HttpURLConnection openUrl(String url) throws Exception {
            if (!AppLinks.sameOrigin(server, url)) throw new IOException("not the paired server");
            return connect(url);
        }

        private HttpURLConnection connect(String url) throws Exception {
            HttpURLConnection c = Pin.open(url, pin);
            // A redirect would carry the Cookie header to wherever it points.
            c.setInstanceFollowRedirects(false);
            if (cookie != null) c.setRequestProperty("Cookie", cookie);
            return c;
        }
    }

    // ---------- the native copy ----------

    /** The WebView's session for the current server, bound to it (null when signed out there). */
    static Bound web(Context c, String server) {
        if (server == null) return null;
        String ck;
        try {
            ck = CookieManager.getInstance().getCookie(server);
        } catch (RuntimeException e) {
            return null; // WebView being updated
        }
        return new Bound(server, Connection.pin(c), ck);
    }

    /** Copies the WebView's session for the current server (UI thread). */
    static void refresh(Context c) {
        String server = Connection.savedUrl(c);
        if (server == null) return;
        String ck;
        try {
            ck = CookieManager.getInstance().getCookie(server);
        } catch (RuntimeException e) {
            return; // WebView being updated
        }
        if (AppLinks.sessionValue(ck) == null) return;
        String pin = Connection.pin(c);
        synchronized (Session.class) {
            SharedPreferences p = NotifyService.prefs(c);
            boolean same = ck.equals(p.getString(COOKIE, null))
                    && server.equals(p.getString(ORIGIN, null)) && pin.equals(p.getString(PIN, null));
            SharedPreferences.Editor e = p.edit().putString(COOKIE, ck).putString(ORIGIN, server).putString(PIN, pin);
            // A fresh login has a new session. Register its FCM token now;
            // otherwise the old session's token is correctly gone from DB.
            if (!same) e.putLong("fcm.last_attempt", 0);
            e.apply();
        }
        NotificationManagerCompat.from(c).cancel(RELOGIN_ID);
    }

    /** The native session for the server the app is paired with now, or null. */
    static Bound current(Context c) {
        String server = Connection.savedUrl(c);
        if (server == null) return null;
        String pin = Connection.pin(c);
        synchronized (Session.class) {
            SharedPreferences p = NotifyService.prefs(c);
            String ck = p.getString(COOKIE, null);
            if (ck == null) return null;
            String origin = p.getString(ORIGIN, null), boundPin = p.getString(PIN, null);
            if (origin == null || boundPin == null) return adoptLegacy(c, p, server, pin, ck);
            if (!AppLinks.boundTo(origin, boundPin, server, pin)) {
                forgetCopy(p); // copied for another server or certificate
                return null;
            }
            return new Bound(server, pin, ck);
        }
    }

    /**
     * A copy saved by app 1.17 or older has no server recorded. It is kept,
     * bound to the current server, only when the WebView holds the same
     * session for that server (so nothing new is disclosed); otherwise it may
     * be another house's and is dropped (the next app launch copies again).
     */
    private static Bound adoptLegacy(Context c, SharedPreferences p, String server, String pin, String ck) {
        String web;
        try {
            web = CookieManager.getInstance().getCookie(server);
        } catch (RuntimeException e) {
            return null; // WebView being updated: keep it for a later try
        }
        String mine = AppLinks.sessionValue(ck);
        if (mine != null && mine.equals(AppLinks.sessionValue(web))) {
            p.edit().putString(ORIGIN, server).putString(PIN, pin).apply();
            return new Bound(server, pin, ck);
        }
        forgetCopy(p);
        return null;
    }

    /** Drops the native copy (signed out, notifications off). */
    static void forget(Context c) {
        synchronized (Session.class) {
            forgetCopy(NotifyService.prefs(c));
        }
    }

    /** Drops the copy only if it is still s (a newer sign-in may have replaced it). */
    static void forget(Context c, Bound s) {
        synchronized (Session.class) {
            SharedPreferences p = NotifyService.prefs(c);
            if (s != null && s.cookie != null && s.cookie.equals(p.getString(COOKIE, null))) forgetCopy(p);
        }
    }

    private static void forgetCopy(SharedPreferences p) {
        p.edit().remove(COOKIE).remove(ORIGIN).remove(PIN).apply();
    }

    // ---------- switching houses ----------

    /**
     * The app now points at another house (Connection.save): nothing of the
     * previous one may reach it. Drops the native session copy and the push
     * registration (its token, its Firebase project, the transport choice:
     * the new house's pages turn alerts on again), and marks the WebView's
     * cookies for removal before the new server's page loads.
     */
    static void switchedHouse(Context c) {
        synchronized (Session.class) {
            NotifyService.prefs(c).edit()
                    .remove(COOKIE).remove(ORIGIN).remove(PIN)
                    .putBoolean("enabled", false).putBoolean("fcm.enabled", false)
                    .remove("fcm.token").remove("fcm.client").remove("fcm.last_attempt")
                    .commit();
            Connection.prefs(c).edit().putBoolean(WIPE_WEB, true).commit();
        }
        Context app = c.getApplicationContext();
        try {
            app.stopService(new android.content.Intent(app, NotifyService.class));
        } catch (RuntimeException ignored) {
            // not running
        }
        NotificationManagerCompat.from(app).cancel(RELOGIN_ID);
        // The old house's wake-ups stop: its next send finds the token gone.
        new Thread(() -> {
            try {
                FirebaseMessaging.getInstance().setAutoInitEnabled(false);
                Tasks.await(FirebaseMessaging.getInstance().deleteToken(), 5, TimeUnit.SECONDS);
            } catch (Exception ignored) {
                // Firebase never started on this phone, or no network: the
                // cleared flags already keep its messages from being shown.
            }
        }, "session-switch").start();
    }

    /** Before the bridge loads (MainActivity.onCreate): the WebView forgets the previous house. */
    @SuppressWarnings("deprecation")
    static void wipeWebCookiesIfPending(Context c) {
        SharedPreferences p = Connection.prefs(c);
        if (!p.getBoolean(WIPE_WEB, false)) return;
        try {
            CookieManager cm = CookieManager.getInstance();
            // The synchronous form: done before the first request of the new page.
            cm.removeAllCookie();
            cm.flush();
        } catch (RuntimeException e) {
            return; // WebView being updated: try again on the next launch
        }
        p.edit().remove(WIPE_WEB).commit();
    }
}
