package casa.sentinela.app;

import android.Manifest;
import android.annotation.SuppressLint;
import android.content.Context;
import android.content.Intent;
import android.net.Uri;
import android.os.Build;
import android.os.PowerManager;
import android.provider.Settings;

import com.getcapacitor.JSObject;
import com.getcapacitor.PermissionState;
import com.getcapacitor.Plugin;
import com.getcapacitor.PluginCall;
import com.getcapacitor.PluginMethod;
import com.getcapacitor.annotation.CapacitorPlugin;
import com.getcapacitor.annotation.Permission;
import com.getcapacitor.annotation.PermissionCallback;

/**
 * Notifications in the app ({@code Capacitor.Plugins.SentinelaNotify}):
 * asks for the permission and chooses Firebase or the direct connection.
 * Which events notify is chosen on the server (Minha conta).
 */
@CapacitorPlugin(
        name = "SentinelaNotify",
        permissions = @Permission(alias = "notifications", strings = {Manifest.permission.POST_NOTIFICATIONS}))
public class NotifyPlugin extends Plugin {

    private JSObject status() {
        Context c = getContext();
        JSObject o = new JSObject();
        o.put("permission", Notifier.allowed(c) ? "granted"
                : getPermissionState("notifications") == PermissionState.DENIED ? "denied" : "prompt");
        o.put("running", PushMode.enabled(c) && Notifier.allowed(c));
        o.put("transport", PushMode.isFirebase(c) ? "firebase" : NotifyService.enabled(c) ? "direct" : "off");
        o.put("connected", NotifyService.connected);
        PowerManager pm = (PowerManager) c.getSystemService(Context.POWER_SERVICE);
        o.put("batteryOptimized", !PushMode.isFirebase(c) && pm != null && !pm.isIgnoringBatteryOptimizations(c.getPackageName()));
        return o;
    }

    /** {permission, running, connected, batteryOptimized} */
    @PluginMethod
    public void status(PluginCall call) {
        call.resolve(status());
    }

    /** Called when the signed-in web UI mounts (also after a new login). */
    @PluginMethod
    public void sync(PluginCall call) {
        getActivity().runOnUiThread(() -> {
            NotifyService.refreshCookie(getContext());
            NotifyService.start(getContext());
            PushMode.reconcile(getContext());
            call.resolve(status());
        });
    }

    /** Asks for the permission (Android 13+), then chooses the transport. */
    @PluginMethod
    public void enable(PluginCall call) {
        if (!Notifier.allowed(getContext())) {
            if (getPermissionState("notifications") == PermissionState.DENIED && Build.VERSION.SDK_INT >= 33) {
                // Refused twice: Android no longer asks; open the app's settings.
                openNotificationSettings();
                call.resolve(status());
                return;
            }
            requestPermissionForAlias("notifications", call, "afterPermission");
            return;
        }
        turnOn(call);
    }

    @PermissionCallback
    private void afterPermission(PluginCall call) {
        if (!Notifier.allowed(getContext())) {
            call.resolve(status());
            return;
        }
        turnOn(call);
    }

    private void turnOn(PluginCall call) {
        getActivity().runOnUiThread(() -> {
            NotifyService.refreshCookie(getContext());
            if (NotifyService.cookie(getContext()) == null) {
                call.reject("Entre no Sentinela neste celular primeiro.");
                return;
            }
            PushMode.enable(getContext(), firebase -> call.resolve(status()));
        });
    }

    @PluginMethod
    public void disable(PluginCall call) {
        PushMode.disable(getContext());
        call.resolve(status());
    }

    /** Asks Android to let the service run while the phone sleeps. */
    @SuppressLint("BatteryLife") // sideloaded app whose core job is instant alerts
    @PluginMethod
    public void allowBackground(PluginCall call) {
        Context c = getContext();
        try {
            Intent i = new Intent(Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS, Uri.parse("package:" + c.getPackageName()));
            getActivity().startActivity(i);
        } catch (RuntimeException e) {
            try {
                getActivity().startActivity(new Intent(Settings.ACTION_IGNORE_BATTERY_OPTIMIZATION_SETTINGS));
            } catch (RuntimeException ignored) {
                // no such screen on this phone
            }
        }
        call.resolve(status());
    }

    /**
     * Opens Android's switch for the "Conexão com a casa" channel only: turning
     * it off hides the fixed notice while event notifications keep coming (the
     * service keeps running; Android only hides its notice).
     */
    @PluginMethod
    public void hideOngoing(PluginCall call) {
        Notifier.openServiceChannelSettings(getContext());
        call.resolve();
    }

    private void openNotificationSettings() {
        try {
            Intent i = new Intent(Settings.ACTION_APP_NOTIFICATION_SETTINGS)
                    .putExtra(Settings.EXTRA_APP_PACKAGE, getContext().getPackageName());
            getActivity().startActivity(i);
        } catch (RuntimeException ignored) {
            // no such screen on this phone
        }
    }
}
