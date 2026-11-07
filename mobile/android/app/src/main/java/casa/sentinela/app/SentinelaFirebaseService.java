package casa.sentinela.app;

import com.google.firebase.messaging.FirebaseMessagingService;
import com.google.firebase.messaging.RemoteMessage;

/** Android wakes this briefly for the data-only FCM signal. */
public final class SentinelaFirebaseService extends FirebaseMessagingService {
    @Override
    public void onMessageReceived(RemoteMessage message) {
        Pin.init(this);
        String id = message.getData().get("id");
        if (id != null) PushMode.deliver(this, id);
    }

    @Override
    public void onNewToken(String token) {
        PushMode.refreshedToken(this, token);
    }
}
