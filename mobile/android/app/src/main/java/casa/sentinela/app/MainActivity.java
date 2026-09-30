package casa.sentinela.app;

import android.app.DownloadManager;
import android.app.KeyguardManager;
import android.content.ContentValues;
import android.content.Context;
import android.content.Intent;
import android.content.SharedPreferences;
import android.graphics.Color;
import android.net.Uri;
import android.net.http.SslError;
import android.os.Build;
import android.os.Bundle;
import android.os.Environment;
import android.os.SystemClock;
import android.provider.MediaStore;
import android.view.Gravity;
import android.view.View;
import android.view.ViewGroup;
import android.webkit.CookieManager;
import android.webkit.RenderProcessGoneDetail;
import android.webkit.SslErrorHandler;
import android.webkit.URLUtil;
import android.webkit.WebResourceError;
import android.webkit.WebResourceRequest;
import android.webkit.WebSettings;
import android.webkit.WebView;
import android.widget.FrameLayout;
import android.widget.TextView;
import android.widget.Toast;

import androidx.activity.OnBackPressedCallback;
import androidx.annotation.NonNull;
import androidx.biometric.BiometricManager;
import androidx.biometric.BiometricPrompt;
import androidx.core.content.ContextCompat;

import com.getcapacitor.BridgeActivity;
import com.getcapacitor.BridgeWebViewClient;

import java.io.File;
import java.io.FileOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.HttpURLConnection;

/**
 * Sentinela Android app (ADR-0011): a Capacitor WebView on the server's
 * origin — the home's public address, set on the start screen
 * (Connection) — plus the native pieces the WebView lacks:
 * - an app lock (fingerprint/face or the phone's screen lock) after 5 min in
 *   the background, so a borrowed unlocked phone does not open the cameras;
 * - downloads (Nuvem files, exported clips) through Android's DownloadManager,
 *   carrying the session cookie only to the Sentinela server itself.
 * Notifications come from NotifyService (a foreground service holding the
 * server's event stream, NotifyPlugin); tapping one opens its page here.
 * It also makes the WebView behave like an app: the back button walks the
 * pages (twice to leave), no page zoom or scrollbars, a server that does not
 * answer brings back the start screen, and a killed renderer reloads
 * instead of taking the app down.
 */
public class MainActivity extends BridgeActivity {
    private static final long LOCK_AFTER_MS = 5 * 60 * 1000;

    /** Two renderer deaths this close together: stop reloading (crash loop). */
    private static final long RENDERER_LOOP_MS = 30_000;
    private static long lastRendererGone = 0;

    private View cover;
    private boolean prompting = false;
    private boolean lockPending = false;
    private boolean rendererGone = false;
    private long lastBack = 0;
    private BiometricPrompt biometric;

    @Override
    public void onCreate(Bundle savedInstanceState) {
        registerPlugin(NotifyPlugin.class);
        registerPlugin(ConnectionPlugin.class);
        registerPlugin(UpdatePlugin.class);
        Connection.migrate(this);
        // This launch's server (or the bundled start screen), before the bridge loads.
        config = Connection.capConfig(this);
        super.onCreate(savedInstanceState);
        tuneWebView();
        installBackButton();
        installDownloads();
        Notifier.createChannels(this);
        // Built here, not when locking: a BiometricPrompt made or shown
        // around onSaveInstanceState can throw IllegalStateException.
        biometric = new BiometricPrompt(this, ContextCompat.getMainExecutor(this), new BiometricPrompt.AuthenticationCallback() {
            @Override
            public void onAuthenticationSucceeded(@NonNull BiometricPrompt.AuthenticationResult result) {
                prompting = false;
                if (cover != null) cover.setVisibility(View.GONE);
            }

            @Override
            public void onAuthenticationError(int code, @NonNull CharSequence msg) {
                prompting = false; // cover stays; a tap asks again
            }
        });
        // Also after the process was killed in the background (the time is
        // kept on disk): a cold start past the limit asks too.
        lockPending = savedInstanceState == null ? lockDue() : savedInstanceState.getBoolean("locked", false) || lockDue();
        openFromNotification(getIntent());
    }

    @Override
    public void onSaveInstanceState(@NonNull Bundle out) {
        super.onSaveInstanceState(out);
        out.putBoolean("locked", cover != null && cover.getVisibility() == View.VISIBLE);
    }

    private SharedPreferences lockPrefs() {
        return getSharedPreferences("lock", MODE_PRIVATE);
    }

    private boolean lockDue() {
        long at = lockPrefs().getLong("background_at", 0);
        return at > 0 && System.currentTimeMillis() - at > LOCK_AFTER_MS;
    }

    @Override
    protected void onNewIntent(Intent intent) {
        super.onNewIntent(intent);
        openFromNotification(intent);
    }

    /** A notification carries an in-app path such as "/#/events". */
    private void openFromNotification(Intent intent) {
        String path = intent == null ? null : intent.getStringExtra(Notifier.EXTRA_PATH);
        if (path == null || !path.startsWith("/") || path.startsWith("//")) return;
        intent.removeExtra(Notifier.EXTRA_PATH);
        if (rendererGone) return;
        String server = getBridge().getServerUrl();
        if (server == null) return; // start screen: no server chosen yet
        getBridge().getWebView().loadUrl(server.replaceAll("/+$", "") + path);
    }

    @Override
    public void onResume() {
        super.onResume();
        if (lockPending) {
            lockPending = false;
            lock();
        }
        // Keep the notification service's copy of the session current and
        // bring it back after a sign-in (it stops when the session ends).
        if (getBridge() != null && getBridge().getServerUrl() != null) {
            NotifyService.refreshCookie(this);
            NotifyService.start(this);
        }
    }

    @Override
    public void onStop() {
        super.onStop();
        if (!isChangingConfigurations()) {
            lockPrefs().edit().putLong("background_at", System.currentTimeMillis()).apply();
        }
    }

    @Override
    public void onStart() {
        super.onStart();
        // Shown from onResume, once the activity can host the prompt.
        if (lockDue()) lockPending = true;
        lockPrefs().edit().remove("background_at").apply();
    }

    // ---------- WebView as an app ----------

    private void tuneWebView() {
        WebView web = getBridge().getWebView();
        web.setVerticalScrollBarEnabled(false);
        web.setHorizontalScrollBarEnabled(false);
        web.setOverScrollMode(View.OVER_SCROLL_NEVER);
        WebSettings ws = web.getSettings();
        ws.setSupportZoom(false);
        ws.setBuiltInZoomControls(false);
        ws.setDisplayZoomControls(false);
        // The phone's font size setting would break the layout; the pages
        // are sized for touch already.
        ws.setTextZoom(100);
        getBridge().setWebViewClient(new BridgeWebViewClient(getBridge()) {
            @Override
            public void onReceivedError(WebView view, WebResourceRequest request, WebResourceError error) {
                super.onReceivedError(view, request, error);
                if (request.isForMainFrame()) offline(String.valueOf(error.getDescription()));
            }

            @Override
            public void onReceivedSslError(WebView view, SslErrorHandler handler, SslError error) {
                String server = getBridge().getServerUrl();
                // The home's own self-signed certificate (pinned, Pin) on the
                // chosen server: go on. Anything else: never.
                if (server != null && error.getUrl() != null && error.getUrl().startsWith(server) && Pin.matches(error.getCertificate())) {
                    handler.proceed();
                    return;
                }
                handler.cancel();
                if (server != null && error.getUrl() != null && error.getUrl().startsWith(server)) {
                    offline("certificado HTTPS inválido");
                }
            }

            @Override
            public boolean onRenderProcessGone(WebView view, RenderProcessGoneDetail detail) {
                // Android killed the page's process (usually memory, with
                // video). This WebView is dead: never touch it again; the new
                // activity makes a fresh one (the old is destroyed on detach).
                rendererGone = true;
                long now = SystemClock.elapsedRealtime();
                boolean loop = lastRendererGone > 0 && now - lastRendererGone < RENDERER_LOOP_MS;
                lastRendererGone = now;
                if (loop) {
                    // Dying again right away: show the start screen instead
                    // of reloading forever.
                    Connection.offline = "a página fechou por falta de memória no celular";
                }
                runOnUiThread(MainActivity.this::recreate);
                return true;
            }
        });
    }

    /** The chosen server did not answer: back to the start screen, offline state. */
    private void offline(String why) {
        if (getBridge().getServerUrl() == null || Connection.offline != null) return;
        Connection.offline = why == null || why.isEmpty() ? "sem resposta" : why;
        runOnUiThread(this::recreate);
    }

    /**
     * Back: the page closes what is open or goes back a page
     * (window.sentinelaBack in the web app); then WebView history; at the
     * start, a second press within 2 s leaves (the app stays in recents).
     */
    private void installBackButton() {
        getOnBackPressedDispatcher().addCallback(this, new OnBackPressedCallback(true) {
            @Override
            public void handleOnBackPressed() {
                if (cover != null && cover.getVisibility() == View.VISIBLE) {
                    moveTaskToBack(true);
                    return;
                }
                if (rendererGone) return;
                WebView web = getBridge().getWebView();
                boolean can = web.canGoBack();
                web.evaluateJavascript("(window.sentinelaBack ? window.sentinelaBack(" + can + ") : false) === true", v -> {
                    if ("true".equals(v)) return;
                    if (can) {
                        web.goBack();
                        return;
                    }
                    long now = SystemClock.elapsedRealtime();
                    if (now - lastBack < 2000) {
                        lastBack = 0;
                        moveTaskToBack(true);
                    } else {
                        lastBack = now;
                        Toast.makeText(MainActivity.this, "Toque em voltar de novo para sair", Toast.LENGTH_SHORT).show();
                    }
                });
            }
        });
    }

    // ---------- app lock ----------

    private int authenticators() {
        // Weak biometrics or the device PIN/pattern; before Android 11 the
        // credential fallback is enabled with setDeviceCredentialAllowed.
        return Build.VERSION.SDK_INT >= Build.VERSION_CODES.R
                ? BiometricManager.Authenticators.BIOMETRIC_WEAK | BiometricManager.Authenticators.DEVICE_CREDENTIAL
                : BiometricManager.Authenticators.BIOMETRIC_WEAK;
    }

    private void lock() {
        KeyguardManager km = (KeyguardManager) getSystemService(Context.KEYGUARD_SERVICE);
        if (km == null || !km.isDeviceSecure()) {
            return; // no screen lock on this phone: nothing to ask for
        }
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R
                && BiometricManager.from(this).canAuthenticate(authenticators()) != BiometricManager.BIOMETRIC_SUCCESS) {
            return;
        }
        if (cover == null) {
            FrameLayout f = new FrameLayout(this);
            f.setBackgroundColor(Color.parseColor("#232322"));
            f.setClickable(true);
            TextView t = new TextView(this);
            t.setText("Sentinela bloqueado\nToque para desbloquear");
            t.setTextColor(Color.parseColor("#ECEBE6"));
            t.setTextSize(18);
            t.setGravity(Gravity.CENTER);
            f.addView(t, new FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT));
            f.setOnClickListener(v -> prompt());
            cover = f;
            addContentView(cover, new ViewGroup.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT));
        }
        cover.setVisibility(View.VISIBLE);
        prompt();
    }

    @SuppressWarnings("deprecation")
    private void prompt() {
        if (prompting || biometric == null) return;
        BiometricPrompt.PromptInfo.Builder info = new BiometricPrompt.PromptInfo.Builder()
                .setTitle("Desbloquear o Sentinela")
                .setSubtitle("Use a digital, o rosto ou o bloqueio de tela");
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
            info.setAllowedAuthenticators(authenticators());
        } else {
            info.setDeviceCredentialAllowed(true);
        }
        try {
            biometric.authenticate(info.build());
            prompting = true;
        } catch (RuntimeException e) {
            prompting = false; // the cover stays; a tap tries again
        }
    }

    // ---------- downloads ----------

    private void installDownloads() {
        WebView web = getBridge().getWebView();
        final String server = getBridge().getServerUrl();
        final Uri serverUri = server == null ? null : Uri.parse(server);
        web.setDownloadListener((url, userAgent, disposition, mime, length) -> {
            Uri uri = Uri.parse(url);
            // Only files from the Sentinela server itself, over HTTPS.
            if (serverUri == null || !"https".equals(uri.getScheme()) || !"https".equals(serverUri.getScheme())
                    || !String.valueOf(serverUri.getHost()).equals(uri.getHost())
                    || serverUri.getPort() != uri.getPort()) {
                return;
            }
            String name = URLUtil.guessFileName(url, disposition, mime);
            if (Pin.active()) {
                // DownloadManager trusts only the system CAs: on the public
                // address (self-signed, pinned) the app downloads by itself.
                pinnedDownload(url, name, mime, userAgent);
                return;
            }
            DownloadManager.Request req;
            try {
                req = new DownloadManager.Request(uri);
                req.setDestinationInExternalPublicDir(Environment.DIRECTORY_DOWNLOADS, "Sentinela/" + name);
            } catch (RuntimeException e) {
                pinnedDownload(url, name, mime, userAgent);
                return;
            }
            String cookie = CookieManager.getInstance().getCookie(url);
            if (cookie != null) req.addRequestHeader("Cookie", cookie);
            req.addRequestHeader("User-Agent", userAgent);
            req.setTitle(name);
            req.setMimeType(mime);
            req.setNotificationVisibility(DownloadManager.Request.VISIBILITY_VISIBLE_NOTIFY_COMPLETED);
            try {
                DownloadManager dm = (DownloadManager) getSystemService(Context.DOWNLOAD_SERVICE);
                dm.enqueue(req);
                Toast.makeText(this, "Baixando " + name, Toast.LENGTH_SHORT).show();
            } catch (RuntimeException e) {
                // Download manager switched off, or no Downloads folder.
                pinnedDownload(url, name, mime, userAgent);
            }
        });
    }

    /** Download over the pinned connection into Downloads/Sentinela. */
    private void pinnedDownload(String url, String name, String mime, String userAgent) {
        Toast.makeText(this, "Baixando " + name, Toast.LENGTH_SHORT).show();
        final String cookie = CookieManager.getInstance().getCookie(url);
        new Thread(() -> {
            String msg;
            HttpURLConnection c = null;
            try {
                c = Pin.open(url);
                c.setConnectTimeout(10000);
                c.setReadTimeout(60000);
                if (cookie != null) c.setRequestProperty("Cookie", cookie);
                c.setRequestProperty("User-Agent", userAgent);
                if (c.getResponseCode() != 200) throw new IOException("HTTP " + c.getResponseCode());
                try (InputStream in = c.getInputStream(); OutputStream out = openDownload(name, mime)) {
                    byte[] buf = new byte[64 * 1024];
                    for (int n; (n = in.read(buf)) > 0; ) out.write(buf, 0, n);
                }
                msg = "Salvo em Downloads/Sentinela: " + name;
            } catch (Exception e) {
                msg = "Não foi possível baixar " + name;
            } finally {
                if (c != null) c.disconnect();
            }
            final String m = msg;
            runOnUiThread(() -> Toast.makeText(this, m, Toast.LENGTH_LONG).show());
        }, "download").start();
    }

    private OutputStream openDownload(String name, String mime) throws IOException {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            ContentValues v = new ContentValues();
            v.put(MediaStore.Downloads.DISPLAY_NAME, name);
            if (mime != null) v.put(MediaStore.Downloads.MIME_TYPE, mime);
            v.put(MediaStore.Downloads.RELATIVE_PATH, Environment.DIRECTORY_DOWNLOADS + "/Sentinela");
            Uri item = getContentResolver().insert(MediaStore.Downloads.EXTERNAL_CONTENT_URI, v);
            if (item == null) throw new IOException("no downloads entry");
            OutputStream out = getContentResolver().openOutputStream(item);
            if (out == null) throw new IOException("cannot write");
            return out;
        }
        // Android 9 and older: the app's own Downloads folder (no storage permission).
        File dir = new File(getExternalFilesDir(Environment.DIRECTORY_DOWNLOADS), "Sentinela");
        if (!dir.isDirectory() && !dir.mkdirs()) throw new IOException("cannot create folder");
        return new FileOutputStream(new File(dir, name));
    }
}
