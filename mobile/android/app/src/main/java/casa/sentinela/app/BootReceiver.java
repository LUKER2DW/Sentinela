package casa.sentinela.app;

import android.content.BroadcastReceiver;
import android.content.Context;
import android.content.Intent;

/** Restarts the direct fallback after a reboot or an app update; Firebase
 * delivery needs no app-owned service. */
public class BootReceiver extends BroadcastReceiver {
    @Override
    public void onReceive(Context context, Intent intent) {
        String a = intent == null ? null : intent.getAction();
        if (Intent.ACTION_BOOT_COMPLETED.equals(a) || Intent.ACTION_MY_PACKAGE_REPLACED.equals(a)) {
            NotifyService.start(context);
        }
    }
}
