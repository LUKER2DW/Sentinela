package casa.sentinela.app;

import android.app.Activity;
import android.os.CancellationSignal;

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

/**
 * Android password manager for the login screen
 * ({@code Capacitor.Plugins.SentinelaCredentials}). The WebView never offers
 * "Save password?" for a sign-in made with fetch(), so the page calls
 * {@code save} after a successful login (Google Password Manager's sheet)
 * and {@code get} to fill the form from a saved password. The password goes
 * only to Android's Credential Manager; nothing here stores or logs it.
 */
@CapacitorPlugin(name = "SentinelaCredentials")
public class CredentialsPlugin extends Plugin {

    @PluginMethod
    public void save(PluginCall call) {
        String user = call.getString("username", "");
        String pw = call.getString("password", "");
        Activity activity = getActivity();
        if (user == null || user.isEmpty() || pw == null || pw.isEmpty() || activity == null) {
            call.reject("missing username or password");
            return;
        }
        CredentialManager cm = CredentialManager.create(activity);
        cm.createCredentialAsync(activity, new CreatePasswordRequest(user, pw), new CancellationSignal(),
            ContextCompat.getMainExecutor(activity),
            new CredentialManagerCallback<CreateCredentialResponse, CreateCredentialException>() {
                @Override
                public void onResult(CreateCredentialResponse r) {
                    call.resolve();
                }

                @Override
                public void onError(@NonNull CreateCredentialException e) {
                    call.reject(e.getType());
                }
            });
    }

    @PluginMethod
    public void get(PluginCall call) {
        Activity activity = getActivity();
        if (activity == null) {
            call.reject("no activity");
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
    }
}
