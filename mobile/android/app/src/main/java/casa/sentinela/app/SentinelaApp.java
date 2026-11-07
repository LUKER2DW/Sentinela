package casa.sentinela.app;

import android.app.Application;

/**
 * Starts Firebase with the paired house's own project (saved from the
 * server) before any component runs: Android may start the process just
 * to deliver an FCM wake-up, before any screen opens.
 */
public final class SentinelaApp extends Application {
    @Override
    public void onCreate() {
        super.onCreate();
        PushMode.startSavedFirebase(this);
    }
}
