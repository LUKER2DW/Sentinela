package casa.sentinela.app;

import android.Manifest;
import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.content.Context;
import android.content.Intent;
import android.content.SharedPreferences;
import android.content.pm.PackageManager;
import android.graphics.Bitmap;
import android.graphics.BitmapFactory;
import android.graphics.Color;
import android.os.Build;

import androidx.core.app.NotificationCompat;
import androidx.core.app.NotificationManagerCompat;
import androidx.core.content.ContextCompat;

import org.json.JSONObject;

import java.io.InputStream;
import java.net.HttpURLConnection;
import java.security.SecureRandom;

/**
 * Shows the server's notifications (the same JSON a browser's service
 * worker gets: title, body, tag, url and an optional snapshot path) and
 * owns the app's notification channels.
 */
final class Notifier {
    static final String CHANNEL = "events";
    static final String CHANNEL_SERVICE = "service";
    static final String EXTRA_PATH = "casa.sentinela.app.PATH";
    /** HMAC of EXTRA_PATH under this install's key: only the app's own notifications carry a valid one. */
    static final String EXTRA_TOKEN = "casa.sentinela.app.TOKEN";
    /** The action of the app's own notification taps (never in the manifest's intent filters). */
    static final String ACTION_OPEN = "casa.sentinela.app.OPEN";
    private static final int MAX_SNAPSHOT = 2 << 20;

    private Notifier() {}

    static boolean allowed(Context ctx) {
        return Build.VERSION.SDK_INT < Build.VERSION_CODES.TIRAMISU
                || ContextCompat.checkSelfPermission(ctx, Manifest.permission.POST_NOTIFICATIONS) == PackageManager.PERMISSION_GRANTED;
    }

    static void createChannels(Context ctx) {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) return;
        NotificationManager nm = ctx.getSystemService(NotificationManager.class);
        if (nm == null) return;
        NotificationChannel ch = new NotificationChannel(CHANNEL, ctx.getString(R.string.channel_events), NotificationManager.IMPORTANCE_HIGH);
        ch.setDescription(ctx.getString(R.string.channel_events_desc));
        ch.enableVibration(true);
        nm.createNotificationChannel(ch);
        NotificationChannel svc = new NotificationChannel(CHANNEL_SERVICE, ctx.getString(R.string.channel_service), NotificationManager.IMPORTANCE_MIN);
        svc.setDescription(ctx.getString(R.string.channel_service_desc));
        svc.setShowBadge(false);
        svc.setSound(null, null);
        svc.enableVibration(false);
        svc.setLockscreenVisibility(Notification.VISIBILITY_SECRET);
        nm.createNotificationChannel(svc);
    }

    /** Android's settings for the fixed "Connection to the home" notice (one switch hides it). */
    static void openServiceChannelSettings(Context ctx) {
        Intent i = Build.VERSION.SDK_INT >= Build.VERSION_CODES.O
                ? new Intent(android.provider.Settings.ACTION_CHANNEL_NOTIFICATION_SETTINGS)
                        .putExtra(android.provider.Settings.EXTRA_APP_PACKAGE, ctx.getPackageName())
                        .putExtra(android.provider.Settings.EXTRA_CHANNEL_ID, CHANNEL_SERVICE)
                : new Intent(android.provider.Settings.ACTION_APPLICATION_DETAILS_SETTINGS, android.net.Uri.parse("package:" + ctx.getPackageName()));
        i.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK);
        try {
            ctx.startActivity(i);
        } catch (RuntimeException ignored) {
            // no such screen on this phone
        }
    }

    /** Pending intent for the fixed notice: straight to its switch. */
    static PendingIntent serviceChannelSettings(Context ctx, int request) {
        Intent i = Build.VERSION.SDK_INT >= Build.VERSION_CODES.O
                ? new Intent(android.provider.Settings.ACTION_CHANNEL_NOTIFICATION_SETTINGS)
                        .putExtra(android.provider.Settings.EXTRA_APP_PACKAGE, ctx.getPackageName())
                        .putExtra(android.provider.Settings.EXTRA_CHANNEL_ID, CHANNEL_SERVICE)
                : new Intent(android.provider.Settings.ACTION_APPLICATION_DETAILS_SETTINGS, android.net.Uri.parse("package:" + ctx.getPackageName()));
        return PendingIntent.getActivity(ctx, request, i, PendingIntent.FLAG_IMMUTABLE | PendingIntent.FLAG_UPDATE_CURRENT);
    }

    /**
     * Opens the app on an in-app page such as "/#/events". MainActivity is
     * exported, so the path carries a token only this install can make; an
     * intent from any other app or a web page cannot steer the WebView.
     */
    static PendingIntent openApp(Context ctx, String path, int request) {
        Intent open = new Intent(ctx, MainActivity.class)
                .setAction(ACTION_OPEN)
                .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK | Intent.FLAG_ACTIVITY_SINGLE_TOP);
        if (path != null && AppLinks.isAppRoute(path)) {
            open.putExtra(EXTRA_PATH, path).putExtra(EXTRA_TOKEN, AppLinks.sign(linkKey(ctx), path));
        }
        return PendingIntent.getActivity(ctx, request, open, PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE);
    }

    /** True when path came from one of this app's own notifications (openApp). */
    static boolean fromOwnNotification(Context ctx, String action, String path, String token) {
        return ACTION_OPEN.equals(action) && AppLinks.verify(linkKey(ctx), path, token);
    }

    /** This install's random key for notification tokens: app-private, never backed up (allowBackup=false). */
    private static synchronized byte[] linkKey(Context ctx) {
        SharedPreferences p = ctx.getSharedPreferences("links", Context.MODE_PRIVATE);
        String saved = p.getString("key", "");
        if (!saved.isEmpty()) {
            try {
                byte[] k = android.util.Base64.decode(saved, android.util.Base64.NO_WRAP);
                if (k.length == 32) return k;
            } catch (IllegalArgumentException ignored) {
                // corrupt: make a new one (old notifications then open the home page)
            }
        }
        byte[] k = new byte[32];
        new SecureRandom().nextBytes(k);
        p.edit().putString("key", android.util.Base64.encodeToString(k, android.util.Base64.NO_WRAP)).commit();
        return k;
    }

    /** Shows one notification; the snapshot follows in a second update. s: the session it came with. */
    static void show(Context ctx, JSONObject m, Session.Bound s) {
        if (!allowed(ctx)) return;
        createChannels(ctx);
        String tag = m.optString("tag", "sentinela");
        String path = m.optString("url", "/#/events");
        if (!AppLinks.isAppRoute(path)) path = "/#/events";
        NotificationCompat.Builder b = new NotificationCompat.Builder(ctx, CHANNEL)
                .setSmallIcon(R.drawable.ic_stat_sentinela)
                .setColor(Color.parseColor("#3E7C88"))
                .setContentTitle(m.optString("title", "Sentinela"))
                .setContentText(m.optString("body", ""))
                .setPriority(NotificationCompat.PRIORITY_HIGH)
                .setCategory(NotificationCompat.CATEGORY_EVENT)
                .setVisibility(NotificationCompat.VISIBILITY_PRIVATE)
                .setWhen(System.currentTimeMillis())
                .setShowWhen(true)
                .setAutoCancel(true)
                .setContentIntent(openApp(ctx, path, tag.hashCode()));
        NotificationManagerCompat nm = NotificationManagerCompat.from(ctx);
        try {
            nm.notify(tag, 1, b.build());
        } catch (SecurityException e) {
            return; // permission revoked meanwhile
        }
        String image = m.optString("image", "");
        if (s != null && image.startsWith("/") && !image.startsWith("//")) {
            Bitmap bmp = fetchSnapshot(s, image);
            if (bmp == null) return;
            b.setLargeIcon(bmp)
                    .setStyle(new NotificationCompat.BigPictureStyle().bigPicture(bmp).bigLargeIcon((Bitmap) null))
                    .setOnlyAlertOnce(true);
            try {
                nm.notify(tag, 1, b.build());
            } catch (SecurityException ignored) {
                // permission revoked meanwhile
            }
        }
    }

    /**
     * GET the snapshot from the Sentinela server the session belongs to
     * (Session.Bound: same origin, its pin, no redirects), scaled down: a
     * full camera frame as a Bitmap would cost ~8 MB.
     */
    static Bitmap fetchSnapshot(Session.Bound s, String path) {
        HttpURLConnection c = null;
        try {
            int q = path.indexOf('?');
            c = s.open(q < 0 ? path : path.substring(0, q));
            c.setConnectTimeout(5000);
            c.setReadTimeout(8000);
            if (c.getResponseCode() != 200 || c.getContentLength() > MAX_SNAPSHOT) return null;
            byte[] data;
            try (InputStream in = c.getInputStream()) {
                java.io.ByteArrayOutputStream out = new java.io.ByteArrayOutputStream();
                byte[] buf = new byte[16 * 1024];
                for (int n; (n = in.read(buf)) > 0; ) {
                    out.write(buf, 0, n);
                    if (out.size() > MAX_SNAPSHOT) return null;
                }
                data = out.toByteArray();
            }
            BitmapFactory.Options o = new BitmapFactory.Options();
            o.inJustDecodeBounds = true;
            BitmapFactory.decodeByteArray(data, 0, data.length, o);
            int sample = 1;
            while (o.outWidth / (sample * 2) >= 720) sample *= 2;
            o = new BitmapFactory.Options();
            o.inSampleSize = sample;
            return BitmapFactory.decodeByteArray(data, 0, data.length, o);
        } catch (Exception | OutOfMemoryError e) {
            return null;
        } finally {
            if (c != null) c.disconnect();
        }
    }
}
