package casa.sentinela.app;

import android.Manifest;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.content.Context;
import android.content.Intent;
import android.content.pm.PackageManager;
import android.graphics.Bitmap;
import android.graphics.BitmapFactory;
import android.graphics.Color;
import android.net.Uri;
import android.os.Build;

import androidx.core.app.NotificationCompat;
import androidx.core.app.NotificationManagerCompat;
import androidx.core.content.ContextCompat;

import org.json.JSONObject;

import java.io.InputStream;
import java.net.HttpURLConnection;
import java.net.URL;

/**
 * Shows the server's notifications (the same JSON a browser's service
 * worker gets: title, body, tag, url and an optional snapshot path) and
 * owns the app's notification channels.
 */
final class Notifier {
    static final String CHANNEL = "events";
    static final String CHANNEL_SERVICE = "service";
    static final String EXTRA_PATH = "casa.sentinela.app.PATH";
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
        NotificationChannel ch = new NotificationChannel(CHANNEL, "Eventos das câmeras", NotificationManager.IMPORTANCE_HIGH);
        ch.setDescription("Pessoa, veículo, movimento e câmera mexida, como escolhido em Minha conta");
        ch.enableVibration(true);
        nm.createNotificationChannel(ch);
        NotificationChannel svc = new NotificationChannel(CHANNEL_SERVICE, "Conexão com a casa", NotificationManager.IMPORTANCE_MIN);
        svc.setDescription("Aviso fixo exigido pelo Android enquanto o app espera os eventos. Pode ser ocultado.");
        svc.setShowBadge(false);
        nm.createNotificationChannel(svc);
    }

    /** Opens the app on an in-app path such as "/#/events". */
    static PendingIntent openApp(Context ctx, String path, int request) {
        Intent open = new Intent(ctx, MainActivity.class)
                .setAction(Intent.ACTION_VIEW)
                .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK | Intent.FLAG_ACTIVITY_SINGLE_TOP);
        if (path != null) open.putExtra(EXTRA_PATH, path);
        return PendingIntent.getActivity(ctx, request, open, PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE);
    }

    /** Shows one notification; the snapshot follows in a second update. */
    static void show(Context ctx, JSONObject m, String server, String cookie) {
        if (!allowed(ctx)) return;
        createChannels(ctx);
        String tag = m.optString("tag", "sentinela");
        String path = m.optString("url", "/#/events");
        if (!path.startsWith("/") || path.startsWith("//")) path = "/#/events";
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
        if (server != null && image.startsWith("/") && !image.startsWith("//")) {
            Bitmap bmp = fetchSnapshot(server, cookie, image);
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
     * GET the snapshot from the Sentinela server itself (pinned certificate),
     * scaled down: a full camera frame as a Bitmap would cost ~8 MB.
     */
    static Bitmap fetchSnapshot(String server, String cookie, String path) {
        HttpURLConnection c = null;
        try {
            Uri base = Uri.parse(server);
            URL u = new URL(base.buildUpon().encodedPath(path).encodedQuery(null).build().toString());
            if (!u.getHost().equals(base.getHost())) return null;
            c = Pin.open(u.toString());
            c.setConnectTimeout(5000);
            c.setReadTimeout(8000);
            if (cookie != null) c.setRequestProperty("Cookie", cookie);
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
