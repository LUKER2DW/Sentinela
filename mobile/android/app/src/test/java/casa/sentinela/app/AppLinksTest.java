package casa.sentinela.app;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertNull;
import static org.junit.Assert.assertTrue;

import java.nio.charset.StandardCharsets;
import java.util.Arrays;
import java.util.Collections;
import java.util.List;

import org.junit.Test;

/** Plain JVM tests (./gradlew testDebugUnitTest): AppLinks has no Android types. */
public class AppLinksTest {
    private static final byte[] KEY = "0123456789abcdef0123456789abcdef".getBytes(StandardCharsets.US_ASCII);
    private static final byte[] OTHER_KEY = "fedcba9876543210fedcba9876543210".getBytes(StandardCharsets.US_ASCII);

    /** A 64-character pin made of one repeated pair. */
    private static String twice(String pair) {
        StringBuilder sb = new StringBuilder();
        for (int i = 0; i < 32; i++) sb.append(pair);
        return sb.toString();
    }

    @Test
    public void appRoutes() {
        Object[][] cases = {
                {"/#/", true},
                {"/#/events", true},
                {"/#/system", true},
                {"/#/recordings?cam=cam1&ev=12&t=2026-09-28T13%3A00%3A00Z", true},
                {"/#/invite?c=ABC&u=ana%40example.com", true},
                // API paths, other sites and tricks: never from an intent
                {"/api/cameras/cam1/export?from=2026-01-01T00:00:00Z&to=2026-01-01T00:30:00Z", false},
                {"/api/nas/file?path=/x.mp4&download=1", false},
                {"/stream/ws?ticket=x", false},
                {"/", false},
                {"//evil.example/#/events", false},
                {"https://evil.example/#/events", false},
                {"/%23/events", false},
                {"/#/../api/me", false},
                {"/#/Events", false},
                {"/#/events x", false},
                {"/#/events\n", false},
                {"/#/events\\", false},
                {"/#/events\"", false},
                {"/#/?x=1", false},
                {"", false},
                {null, false},
        };
        for (Object[] c : cases) {
            assertEquals(String.valueOf(c[0]), c[1], AppLinks.isAppRoute((String) c[0]));
        }
        char[] big = new char[3000];
        Arrays.fill(big, 'a');
        assertFalse(AppLinks.isAppRoute("/#/events?" + new String(big)));
    }

    @Test
    public void handoffRoutesAreInvitesOnly() {
        assertTrue(AppLinks.isHandoffRoute("/#/invite?c=ABC&u=ana%40example.com"));
        assertTrue(AppLinks.isHandoffRoute("/#/convite?c=ABC"));
        assertFalse(AppLinks.isHandoffRoute("/#/events"));
        assertFalse(AppLinks.isHandoffRoute("/#/account"));
        assertFalse(AppLinks.isHandoffRoute("/#/"));
        assertFalse(AppLinks.isHandoffRoute("/api/me"));
        assertFalse(AppLinks.isHandoffRoute(null));
    }

    @Test
    public void notificationTokens() {
        String path = "/#/recordings?cam=cam1&ev=12";
        String token = AppLinks.sign(KEY, path);
        assertEquals(64, token.length());
        assertTrue(AppLinks.verify(KEY, path, token));
        // bound to the path and to this install's key
        assertFalse(AppLinks.verify(KEY, "/#/events", token));
        assertFalse(AppLinks.verify(OTHER_KEY, path, token));
        assertFalse(AppLinks.verify(KEY, path, null));
        assertFalse(AppLinks.verify(KEY, path, ""));
        assertFalse(AppLinks.verify(KEY, path, token.substring(0, 63) + (token.charAt(63) == '0' ? '1' : '0')));
        // a valid token never makes an API path openable
        String api = "/api/cameras/cam1/export?from=a&to=b";
        assertFalse(AppLinks.verify(KEY, api, AppLinks.sign(KEY, api)));
        // no usable key: nothing is signed or accepted
        assertEquals("", AppLinks.sign(new byte[4], path));
        assertFalse(AppLinks.verify(new byte[4], path, AppLinks.sign(new byte[4], path)));
        assertFalse(AppLinks.verify(null, path, token));
    }

    @Test
    public void origins() {
        assertEquals("https://house.example:443", AppLinks.origin("https://House.Example/"));
        assertEquals("https://house.example:443", AppLinks.origin("https://house.example:443"));
        assertEquals("https://192.168.1.10:18443", AppLinks.origin("https://192.168.1.10:18443/api/me?x=1"));
        assertNull(AppLinks.origin("http://house.example"));
        assertNull(AppLinks.origin("https://user@house.example"));
        assertNull(AppLinks.origin("sentinela://open?path=/%23/events"));
        assertNull(AppLinks.origin("not a url"));
        assertNull(AppLinks.origin(null));

        assertTrue(AppLinks.sameOrigin("https://house.example", "https://HOUSE.example:443/api/x"));
        assertFalse(AppLinks.sameOrigin("https://203.0.113.5:18443", "https://203.0.113.5:9443"));
        assertFalse(AppLinks.sameOrigin("https://house.example", "https://house.example.evil.example"));
        assertFalse(AppLinks.sameOrigin("https://house.example", "http://house.example"));
        assertFalse(AppLinks.sameOrigin(null, null));
    }

    @Test
    public void pathsStayOnTheServer() {
        String server = "https://203.0.113.5:18443";
        assertEquals(server + "/api/notify/stream", AppLinks.onServer(server, "/api/notify/stream"));
        assertEquals(server + "/api/me", AppLinks.onServer(server + "/", "/api/me"));
        assertEquals(server + "/api/events/12/snapshot", AppLinks.onServer(server, "/api/events/12/snapshot"));
        assertEquals(server + "/@evil.example/x", AppLinks.onServer(server, "/@evil.example/x"));
        String[] bad = {"//evil.example/x", "api/me", "/api/me#x", "/\\evil.example", " /api/me", "/api me", "https://evil.example/x", "", null};
        for (String p : bad) assertNull(String.valueOf(p), AppLinks.onServer(server, p));
        assertNull(AppLinks.onServer("http://203.0.113.5", "/api/me"));
        assertNull(AppLinks.onServer(null, "/api/me"));
    }

    @Test
    public void sessionBinding() {
        String a = "https://203.0.113.5:18443", b = "https://198.51.100.7:18443";
        String pinA = twice("aa"), pinB = twice("bb");
        assertTrue(AppLinks.boundTo(a, pinA, a + "/", pinA.toUpperCase()));
        assertTrue(AppLinks.boundTo("https://house.example", "", "https://house.example", ""));
        // another server, another port, another certificate, or no record: never
        assertFalse(AppLinks.boundTo(a, pinA, b, pinA));
        assertFalse(AppLinks.boundTo(a, pinA, "https://203.0.113.5:9443", pinA));
        assertFalse(AppLinks.boundTo(a, pinA, a, pinB));
        assertFalse(AppLinks.boundTo(a, pinA, a, ""));
        assertFalse(AppLinks.boundTo(null, null, a, pinA));
        assertFalse(AppLinks.boundTo(a, null, a, pinA));
    }

    @Test
    public void sameHouse() {
        String cur = "https://203.0.113.5:18443", pin = twice("aa");
        List<String[]> alts = Arrays.asList(
                new String[] {"https://192.168.1.10:18443/", pin},
                new String[] {"https://house.example.ts.net", ""});
        assertTrue(AppLinks.sameHouse(cur, pin, alts, cur + "/", pin));
        assertTrue(AppLinks.sameHouse(cur, pin, alts, "https://192.168.1.10:18443", pin.toUpperCase()));
        assertTrue(AppLinks.sameHouse(cur, pin, alts, "https://house.example.ts.net/", ""));
        // a code for another house, or a known address with another certificate
        assertFalse(AppLinks.sameHouse(cur, pin, alts, "https://198.51.100.7:18443", pin));
        assertFalse(AppLinks.sameHouse(cur, pin, alts, cur, twice("bb")));
        assertFalse(AppLinks.sameHouse(cur, pin, alts, "https://192.168.1.10:18443", ""));
        assertFalse(AppLinks.sameHouse(cur, pin, alts, "https://house.example.ts.net", pin));
        // first pairing: nothing to keep
        assertFalse(AppLinks.sameHouse("", "", Collections.emptyList(), cur, pin));
        assertFalse(AppLinks.sameHouse(cur, pin, null, null, pin));
    }

    @Test
    public void sessionValue() {
        assertEquals("abc", AppLinks.sessionValue("a=1; sentinela_session=abc; b=2"));
        assertEquals("abc", AppLinks.sessionValue("sentinela_session=abc"));
        assertNull(AppLinks.sessionValue("sentinela_session="));
        assertNull(AppLinks.sessionValue("xsentinela_session=abc"));
        assertNull(AppLinks.sessionValue("a=1"));
        assertNull(AppLinks.sessionValue(null));
    }
}
