package casa.sentinela.app;

import android.app.DownloadManager;
import android.app.KeyguardManager;
import android.content.Context;
import android.content.Intent;
import android.graphics.Color;
import android.net.Uri;
import android.os.Build;
import android.os.Bundle;
import android.os.Environment;
import android.os.SystemClock;
import android.view.Gravity;
import android.view.View;
import android.view.ViewGroup;
import android.webkit.CookieManager;
import android.webkit.URLUtil;
import android.webkit.WebView;
import android.widget.FrameLayout;
import android.widget.TextView;
import android.widget.Toast;

import androidx.annotation.NonNull;
import androidx.biometric.BiometricManager;
import androidx.biometric.BiometricPrompt;
import androidx.core.content.ContextCompat;

import com.getcapacitor.BridgeActivity;

/**
 * Sentinela Android app (ADR-0011): a Capacitor WebView on the tailnet
 * origin, plus two native pieces the WebView lacks:
 * - an app lock (fingerprint/face or the phone's screen lock) after 5 min in
 *   the background, so a borrowed unlocked phone does not open the cameras;
 * - downloads (Nuvem files, exported clips) through Android's DownloadManager,
 *   carrying the session cookie only to the Sentinela server itself.
 * Notifications come through UnifiedPush (PushBridge, SentinelaPushService);
 * tapping one opens its page here.
 */
public class MainActivity extends BridgeActivity {
    private static final long LOCK_AFTER_MS = 5 * 60 * 1000;

    private long backgroundAt = 0;
    private View cover;
    private boolean prompting = false;

    @Override
    public void onCreate(Bundle savedInstanceState) {
        registerPlugin(PushBridge.class);
        super.onCreate(savedInstanceState);
        installDownloads();
        SentinelaPushService.createChannel(this);
        openFromNotification(getIntent());
    }

    @Override
    protected void onNewIntent(Intent intent) {
        super.onNewIntent(intent);
        openFromNotification(intent);
    }

    /** A notification carries an in-app path such as "/#/events". */
    private void openFromNotification(Intent intent) {
        String path = intent == null ? null : intent.getStringExtra(SentinelaPushService.EXTRA_PATH);
        if (path == null || !path.startsWith("/") || path.startsWith("//")) return;
        intent.removeExtra(SentinelaPushService.EXTRA_PATH);
        String server = getBridge().getServerUrl();
        getBridge().getWebView().loadUrl(server.replaceAll("/+$", "") + path);
    }

    @Override
    public void onStop() {
        super.onStop();
        backgroundAt = SystemClock.elapsedRealtime();
    }

    @Override
    public void onStart() {
        super.onStart();
        if (backgroundAt > 0 && SystemClock.elapsedRealtime() - backgroundAt > LOCK_AFTER_MS) {
            lock();
        }
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
            f.setBackgroundColor(Color.parseColor("#3E7C88"));
            f.setClickable(true);
            TextView t = new TextView(this);
            t.setText("Sentinela bloqueado\nToque para desbloquear");
            t.setTextColor(Color.parseColor("#F5F2EC"));
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
        if (prompting) return;
        prompting = true;
        BiometricPrompt.PromptInfo.Builder info = new BiometricPrompt.PromptInfo.Builder()
                .setTitle("Desbloquear o Sentinela")
                .setSubtitle("Use a digital, o rosto ou o bloqueio de tela");
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
            info.setAllowedAuthenticators(authenticators());
        } else {
            info.setDeviceCredentialAllowed(true);
        }
        new BiometricPrompt(this, ContextCompat.getMainExecutor(this), new BiometricPrompt.AuthenticationCallback() {
            @Override
            public void onAuthenticationSucceeded(@NonNull BiometricPrompt.AuthenticationResult result) {
                prompting = false;
                backgroundAt = 0;
                if (cover != null) cover.setVisibility(View.GONE);
            }

            @Override
            public void onAuthenticationError(int code, @NonNull CharSequence msg) {
                prompting = false; // cover stays; a tap asks again
            }
        }).authenticate(info.build());
    }

    // ---------- downloads ----------

    private void installDownloads() {
        WebView web = getBridge().getWebView();
        final String serverHost = Uri.parse(getBridge().getServerUrl()).getHost();
        web.setDownloadListener((url, userAgent, disposition, mime, length) -> {
            Uri uri = Uri.parse(url);
            if (!"https".equals(uri.getScheme()) || serverHost == null || !serverHost.equals(uri.getHost())) {
                return; // only files from the Sentinela server itself
            }
            String name = URLUtil.guessFileName(url, disposition, mime);
            DownloadManager.Request req = new DownloadManager.Request(uri);
            String cookie = CookieManager.getInstance().getCookie(url);
            if (cookie != null) req.addRequestHeader("Cookie", cookie);
            req.addRequestHeader("User-Agent", userAgent);
            req.setTitle(name);
            req.setMimeType(mime);
            req.setNotificationVisibility(DownloadManager.Request.VISIBILITY_VISIBLE_NOTIFY_COMPLETED);
            req.setDestinationInExternalPublicDir(Environment.DIRECTORY_DOWNLOADS, "Sentinela/" + name);
            DownloadManager dm = (DownloadManager) getSystemService(Context.DOWNLOAD_SERVICE);
            dm.enqueue(req);
            Toast.makeText(this, "Baixando " + name, Toast.LENGTH_SHORT).show();
        });
    }
}
