package casa.sentinela.app;

import android.Manifest;
import android.content.Context;
import android.content.SharedPreferences;
import android.os.Build;
import android.os.Handler;
import android.os.Looper;

import com.getcapacitor.JSObject;
import com.getcapacitor.PermissionState;
import com.getcapacitor.Plugin;
import com.getcapacitor.PluginCall;
import com.getcapacitor.PluginMethod;
import com.getcapacitor.annotation.CapacitorPlugin;
import com.getcapacitor.annotation.Permission;
import com.getcapacitor.annotation.PermissionCallback;

import org.unifiedpush.android.connector.UnifiedPush;

import kotlin.Unit;

/**
 * Notifications in the app (ADR-0013): the WebView has no Push API, so the
 * page asks this plugin ({@code Capacitor.Plugins.SentinelaPush}) for a
 * UnifiedPush endpoint through the ntfy app (the distributor), and then
 * subscribes it on the server exactly like a browser subscription. The
 * endpoint's keys make the server encrypt with RFC 8291, as for Web Push.
 */
@CapacitorPlugin(
        name = "SentinelaPush",
        permissions = @Permission(alias = "notifications", strings = {Manifest.permission.POST_NOTIFICATIONS}))
public class PushBridge extends Plugin {
    static final String INSTANCE = "default";
    private static final String PREFS = "push";
    private static final long REGISTER_TIMEOUT_MS = 30_000;

    private static final Handler main = new Handler(Looper.getMainLooper());
    private static PluginCall pending;

    // ---------- endpoint storage (written by SentinelaPushService) ----------

    static void saveEndpoint(Context ctx, String url, String p256dh, String auth) {
        prefs(ctx).edit().putString("url", url).putString("p256dh", p256dh).putString("auth", auth).apply();
        main.post(() -> {
            if (pending != null) {
                pending.resolve(endpointJSON(ctx));
                pending = null;
            }
        });
    }

    static void clearEndpoint(Context ctx) {
        prefs(ctx).edit().clear().apply();
    }

    static void failed(String reason) {
        main.post(() -> {
            if (pending != null) {
                pending.reject("Registro recusado pelo ntfy (" + reason + ")", reason);
                pending = null;
            }
        });
    }

    private static SharedPreferences prefs(Context ctx) {
        return ctx.getSharedPreferences(PREFS, Context.MODE_PRIVATE);
    }

    private static JSObject endpointJSON(Context ctx) {
        SharedPreferences p = prefs(ctx);
        JSObject o = new JSObject();
        String url = p.getString("url", null);
        if (url != null) {
            o.put("endpoint", url);
            JSObject keys = new JSObject();
            keys.put("p256dh", p.getString("p256dh", ""));
            keys.put("auth", p.getString("auth", ""));
            o.put("keys", keys);
        }
        return o;
    }

    // ---------- JS API ----------

    /** {distributor, permission, endpoint?, keys?} */
    @PluginMethod
    public void status(PluginCall call) {
        JSObject o = endpointJSON(getContext());
        o.put("distributor", !UnifiedPush.getDistributors(getContext()).isEmpty());
        o.put("permission", notificationsAllowed() ? "granted" : getPermissionState("notifications") == PermissionState.DENIED ? "denied" : "prompt");
        call.resolve(o);
    }

    /** Asks for the notification permission, then registers with ntfy. Resolves {endpoint, keys}. */
    @PluginMethod
    public void register(PluginCall call) {
        if (!notificationsAllowed()) {
            requestPermissionForAlias("notifications", call, "afterPermission");
            return;
        }
        doRegister(call);
    }

    @PermissionCallback
    private void afterPermission(PluginCall call) {
        if (!notificationsAllowed()) {
            call.reject("Permissão de notificação negada", "denied");
            return;
        }
        doRegister(call);
    }

    private void doRegister(PluginCall call) {
        Context ctx = getContext();
        if (UnifiedPush.getDistributors(ctx).isEmpty()) {
            call.reject("Instale o app ntfy para receber avisos", "no-distributor");
            return;
        }
        String vapid = call.getString("vapid");
        UnifiedPush.tryUseCurrentOrDefaultDistributor(getActivity(), ok -> {
            if (!ok) {
                call.reject("Escolha o ntfy como distribuidor de avisos", "no-distributor");
                return Unit.INSTANCE;
            }
            if (pending != null) pending.reject("Substituído por um novo pedido", "replaced");
            pending = call;
            main.postDelayed(() -> {
                if (pending == call) {
                    pending = null;
                    call.reject("O ntfy não respondeu. Ele está conectado ao servidor da casa?", "timeout");
                }
            }, REGISTER_TIMEOUT_MS);
            try {
                UnifiedPush.register(ctx, INSTANCE, "Sentinela", vapid);
            } catch (RuntimeException e) {
                pending = null;
                call.reject("Falha ao registrar no ntfy", "error", e);
            }
            return Unit.INSTANCE;
        });
    }

    @PluginMethod
    public void unregister(PluginCall call) {
        UnifiedPush.unregister(getContext(), INSTANCE);
        clearEndpoint(getContext());
        call.resolve();
    }

    private boolean notificationsAllowed() {
        return Build.VERSION.SDK_INT < Build.VERSION_CODES.TIRAMISU
                || getPermissionState("notifications") == PermissionState.GRANTED;
    }
}
