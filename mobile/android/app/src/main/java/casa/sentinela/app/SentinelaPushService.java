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
import android.webkit.CookieManager;

import androidx.annotation.NonNull;
import androidx.core.app.NotificationCompat;
import androidx.core.app.NotificationManagerCompat;
import androidx.core.content.ContextCompat;

import com.getcapacitor.CapConfig;

import org.json.JSONObject;
import org.unifiedpush.android.connector.FailedReason;
import org.unifiedpush.android.connector.PushService;
import org.unifiedpush.android.connector.data.PushEndpoint;
import org.unifiedpush.android.connector.data.PushMessage;

import java.io.InputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.nio.charset.StandardCharsets;

/**
 * Receives UnifiedPush messages from the ntfy app (ADR-0013). The server
 * encrypts every message (RFC 8291) with this endpoint's keys, and the
 * connector decrypts it here. The payload is the same JSON the PWA's service
 * worker gets: title, body, tag, url and an optional snapshot path, which is
 * fetched from the Sentinela server itself over the tailnet.
 */
public class SentinelaPushService extends PushService {
    static final String CHANNEL = "events";
    static final String EXTRA_PATH = "casa.sentinela.app.PATH";

    @Override
    public void onNewEndpoint(@NonNull PushEndpoint endpoint, @NonNull String instance) {
        if (endpoint.getPubKeySet() == null) {
            PushBridge.failed("NO_KEYS"); // the server only sends encrypted messages
            return;
        }
        PushBridge.saveEndpoint(this, endpoint.getUrl(), endpoint.getPubKeySet().getPubKey(), endpoint.getPubKeySet().getAuth());
    }

    @Override
    public void onRegistrationFailed(@NonNull FailedReason reason, @NonNull String instance) {
        PushBridge.failed(reason.name());
    }

    @Override
    public void onUnregistered(@NonNull String instance) {
        PushBridge.clearEndpoint(this);
    }

    @Override
    public void onMessage(@NonNull PushMessage message, @NonNull String instance) {
        if (!message.getDecrypted()) return; // plaintext: not from our server
        try {
            JSONObject m = new JSONObject(new String(message.getContent(), StandardCharsets.UTF_8));
            show(getApplicationContext(), m);
        } catch (Exception ignored) {
            // malformed payload: nothing to show
        }
    }

    private static void show(Context ctx, JSONObject m) {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU
                && ContextCompat.checkSelfPermission(ctx, Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) {
            return;
        }
        createChannel(ctx);
        String tag = m.optString("tag", "sentinela");
        String path = m.optString("url", "/#/events");
        Intent open = new Intent(ctx, MainActivity.class)
                .setAction(Intent.ACTION_VIEW)
                .putExtra(EXTRA_PATH, path)
                .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK | Intent.FLAG_ACTIVITY_SINGLE_TOP);
        PendingIntent pi = PendingIntent.getActivity(ctx, tag.hashCode(), open,
                PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE);
        NotificationCompat.Builder b = new NotificationCompat.Builder(ctx, CHANNEL)
                .setSmallIcon(R.drawable.ic_stat_sentinela)
                .setColor(Color.parseColor("#3E7C88"))
                .setContentTitle(m.optString("title", "Sentinela"))
                .setContentText(m.optString("body", ""))
                .setPriority(NotificationCompat.PRIORITY_HIGH)
                .setCategory(NotificationCompat.CATEGORY_EVENT)
                .setAutoCancel(true)
                .setContentIntent(pi);
        NotificationManagerCompat nm = NotificationManagerCompat.from(ctx);
        // Text first (fast), then the same notification again with the picture.
        nm.notify(tag, 1, b.build());
        String image = m.optString("image", "");
        if (image.startsWith("/") && !image.startsWith("//")) {
            new Thread(() -> {
                Bitmap bmp = fetchSnapshot(ctx, image);
                if (bmp == null) return;
                b.setLargeIcon(bmp)
                        .setStyle(new NotificationCompat.BigPictureStyle().bigPicture(bmp).bigLargeIcon((Bitmap) null))
                        .setOnlyAlertOnce(true);
                nm.notify(tag, 1, b.build());
            }, "snapshot").start();
        }
    }

    /** GET the snapshot from the Sentinela server with the app's session cookie. */
    private static Bitmap fetchSnapshot(Context ctx, String path) {
        HttpURLConnection c = null;
        try {
            String server = CapConfig.loadDefault(ctx).getServerUrl();
            if (server == null || !server.startsWith("https://")) return null;
            URL u = new URL(Uri.parse(server).buildUpon().encodedPath(path).build().toString());
            if (!u.getHost().equals(Uri.parse(server).getHost())) return null;
            c = Pin.open(u.toString()); // the public address's certificate is pinned
            c.setConnectTimeout(4000);
            c.setReadTimeout(4000);
            String cookie = CookieManager.getInstance().getCookie(server);
            if (cookie != null) c.setRequestProperty("Cookie", cookie);
            if (c.getResponseCode() != 200) return null;
            try (InputStream in = c.getInputStream()) {
                return BitmapFactory.decodeStream(in);
            }
        } catch (Exception e) {
            return null;
        } finally {
            if (c != null) c.disconnect();
        }
    }

    static void createChannel(Context ctx) {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) return;
        NotificationChannel ch = new NotificationChannel(CHANNEL, "Eventos das câmeras", NotificationManager.IMPORTANCE_HIGH);
        ch.setDescription("Pessoa, movimento e câmera mexida, conforme o modo Casa / Noite / Fora");
        ch.enableVibration(true);
        ctx.getSystemService(NotificationManager.class).createNotificationChannel(ch);
    }
}
