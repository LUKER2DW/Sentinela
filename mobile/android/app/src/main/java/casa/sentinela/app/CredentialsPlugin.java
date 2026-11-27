package casa.sentinela.app;

import android.app.Activity;
import android.content.Context;
import android.content.SharedPreferences;
import android.os.CancellationSignal;
import android.security.keystore.KeyGenParameterSpec;
import android.security.keystore.KeyProperties;
import android.webkit.WebView;

import androidx.annotation.NonNull;
import androidx.core.content.ContextCompat;
import androidx.credentials.CreateCredentialResponse;
import androidx.credentials.CreatePasswordRequest;
import androidx.credentials.Credential;
import androidx.credentials.CredentialManager;
import androidx.credentials.CredentialManagerCallback;
import androidx.credentials.GetCredentialRequest;
import androidx.credentials.GetCredentialResponse;
import androidx.credentials.GetPasswordOption;
import androidx.credentials.PasswordCredential;
import androidx.credentials.exceptions.CreateCredentialException;
import androidx.credentials.exceptions.GetCredentialException;

import com.getcapacitor.JSObject;
import com.getcapacitor.Plugin;
import com.getcapacitor.PluginCall;
import com.getcapacitor.PluginMethod;
import com.getcapacitor.annotation.CapacitorPlugin;

import java.nio.charset.StandardCharsets;
import java.security.KeyStore;
import java.util.HashMap;
import java.util.Locale;
import java.util.Map;

import javax.crypto.KeyGenerator;
import javax.crypto.Mac;
import javax.crypto.SecretKey;

/**
 * Android password manager for the login screen
 * ({@code Capacitor.Plugins.SentinelaCredentials}). The WebView never offers
 * "Save password?" for a sign-in made with fetch(), so the page calls
 * {@code save} after a successful login (Google Password Manager's sheet)
 * and {@code get} to fill the form from a saved password. The password goes
 * only to Android's Credential Manager; nothing here stores or logs it.
 *
 * <p>The app is the same for every house, and Credential Manager keeps the
 * app's passwords together. So each password saved here is bound to the
 * house it was saved on: an HMAC, under a key that never leaves the Android
 * Keystore, of that server's origin, its certificate pin, the account and
 * the password ({@link AppLinks#credentialBinding}). {@code get} hands a
 * picked password only to the house it was bound to, while the app is
 * still paired with it and its page is the one showing; a password saved
 * before this binding existed, or for another house, is refused and the
 * person signs in by hand (the next save binds it).
 */
@CapacitorPlugin(name = "SentinelaCredentials")
public class CredentialsPlugin extends Plugin {
    private static final String PREFS = "credentials";
    private static final String KEY_ALIAS = "sentinela.credentials.binding";
    /** Bindings kept: the oldest go first. */
    private static final int MAX_BINDINGS = 32;

    @PluginMethod
    public void save(PluginCall call) {
        String user = call.getString("username", "");
        String pw = call.getString("password", "");
        Activity activity = getActivity();
        if (user == null || user.isEmpty() || pw == null || pw.isEmpty() || activity == null) {
            call.reject("missing username or password");
            return;
        }
        getBridge().executeOnMainThread(() -> {
            final House house = pairedPage();
            if (house == null) {
                call.reject("not the paired server");
                return;
            }
            CredentialManager cm = CredentialManager.create(activity);
            cm.createCredentialAsync(activity, new CreatePasswordRequest(user, pw), new CancellationSignal(),
                    ContextCompat.getMainExecutor(activity),
                    new CredentialManagerCallback<CreateCredentialResponse, CreateCredentialException>() {
                        @Override
                        public void onResult(CreateCredentialResponse r) {
                            // Bound only once the person accepted the sheet, and only
                            // while the app is still paired with the same house.
                            if (house.same(House.current(getContext()))) bind(house, user, pw);
                            call.resolve();
                        }

                        @Override
                        public void onError(@NonNull CreateCredentialException e) {
                            call.reject(e.getType());
                        }
                    });
        });
    }

    @PluginMethod
    public void get(PluginCall call) {
        Activity activity = getActivity();
        if (activity == null) {
            call.reject("no activity");
            return;
        }
        getBridge().executeOnMainThread(() -> {
            final House house = pairedPage();
            if (house == null) {
                call.reject("not the paired server");
                return;
            }
            GetCredentialRequest req = new GetCredentialRequest.Builder()
                    .addCredentialOption(new GetPasswordOption())
                    .build();
            CredentialManager cm = CredentialManager.create(activity);
            cm.getCredentialAsync(activity, req, new CancellationSignal(), ContextCompat.getMainExecutor(activity),
                    new CredentialManagerCallback<GetCredentialResponse, GetCredentialException>() {
                        @Override
                        public void onResult(GetCredentialResponse r) {
                            Credential c = r.getCredential();
                            if (!(c instanceof PasswordCredential)) {
                                call.reject("not a password");
                                return;
                            }
                            PasswordCredential p = (PasswordCredential) c;
                            // The picker may have stayed open while the app was
                            // paired with another house or left its page.
                            if (!house.same(pairedPage())) {
                                call.reject("the server changed");
                                return;
                            }
                            if (!bound(house, p.getId(), p.getPassword())) {
                                call.reject("saved for another server");
                                return;
                            }
                            JSObject o = new JSObject();
                            o.put("username", p.getId());
                            o.put("password", p.getPassword());
                            call.resolve(o);
                        }

                        @Override
                        public void onError(@NonNull GetCredentialException e) {
                            call.reject(e.getType());
                        }
                    });
        });
    }

    /** The paired house, when the WebView (UI thread) shows a page of it; else null. */
    private House pairedPage() {
        House h = House.current(getContext());
        if (h == null) return null;
        WebView web = getBridge().getWebView();
        String page = web == null ? null : web.getUrl();
        String bridge = getBridge().getServerUrl();
        return AppLinks.sameOrigin(h.server, page) && AppLinks.sameOrigin(h.server, bridge) ? h : null;
    }

    /** The server and pin the app is paired with. */
    private static final class House {
        final String server;
        final String pin;

        private House(String server, String pin) {
            this.server = server;
            this.pin = pin;
        }

        static House current(Context c) {
            String server = Connection.savedUrl(c);
            return server == null ? null : new House(server, Connection.pin(c));
        }

        /** The same origin and certificate pin. */
        boolean same(House h) {
            return h != null && AppLinks.boundTo(server, pin, h.server, h.pin);
        }
    }

    // ---------- bindings ----------

    private SharedPreferences prefs() {
        return getContext().getSharedPreferences(PREFS, Context.MODE_PRIVATE);
    }

    private void bind(House h, String user, String pw) {
        String tag = tag(h, user, pw);
        if (tag == null) return; // no Keystore key: the password is saved, just not offered back
        synchronized (CredentialsPlugin.class) {
            SharedPreferences p = prefs();
            SharedPreferences.Editor e = p.edit().putLong("b." + tag, System.currentTimeMillis());
            Map<String, ?> all = new HashMap<>(p.getAll());
            int n = all.containsKey("b." + tag) ? all.size() : all.size() + 1;
            while (n > MAX_BINDINGS) {
                String oldest = null;
                long when = Long.MAX_VALUE;
                for (Map.Entry<String, ?> en : all.entrySet()) {
                    if (en.getKey().equals("b." + tag) || !(en.getValue() instanceof Long)) continue;
                    if ((Long) en.getValue() < when) {
                        when = (Long) en.getValue();
                        oldest = en.getKey();
                    }
                }
                if (oldest == null) break;
                e.remove(oldest);
                all.remove(oldest);
                n--;
            }
            e.apply();
        }
    }

    private boolean bound(House h, String user, String pw) {
        String tag = tag(h, user, pw);
        if (tag == null) return false;
        synchronized (CredentialsPlugin.class) {
            return prefs().contains("b." + tag);
        }
    }

    /** HMAC of the binding under the Keystore key, hex; null when it cannot be made. */
    private static String tag(House h, String user, String pw) {
        String text = AppLinks.credentialBinding(h.server, h.pin, user, pw);
        if (text == null) return null;
        try {
            Mac mac = Mac.getInstance("HmacSHA256");
            mac.init(key());
            byte[] d = mac.doFinal(text.getBytes(StandardCharsets.UTF_8));
            StringBuilder sb = new StringBuilder(d.length * 2);
            for (byte b : d) sb.append(String.format(Locale.ROOT, "%02x", b));
            return sb.toString();
        } catch (Exception e) {
            return null;
        }
    }

    /** The install's binding key, made once inside the Android Keystore. */
    private static synchronized SecretKey key() throws Exception {
        KeyStore ks = KeyStore.getInstance("AndroidKeyStore");
        ks.load(null);
        java.security.Key k = ks.getKey(KEY_ALIAS, null);
        if (k instanceof SecretKey) return (SecretKey) k;
        KeyGenerator g = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_HMAC_SHA256, "AndroidKeyStore");
        g.init(new KeyGenParameterSpec.Builder(KEY_ALIAS, KeyProperties.PURPOSE_SIGN).build());
        return g.generateKey();
    }
}
