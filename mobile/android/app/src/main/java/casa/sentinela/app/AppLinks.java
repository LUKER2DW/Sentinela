package casa.sentinela.app;

import java.net.URI;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.util.Locale;
import java.util.regex.Pattern;

import javax.crypto.Mac;
import javax.crypto.spec.SecretKeySpec;

/**
 * Rules for what may steer the app from outside, with no Android types so
 * they run as plain JVM unit tests (src/test).
 * - In-app routes: what an intent may open in the WebView. Only "/#/"
 *   pages: the fragment never reaches the server, so an intent cannot make
 *   the signed-in WebView fetch an API path (a download, an export).
 * - Notification tokens: MainActivity is exported, so a path from an intent
 *   extra counts only with the token the app's own notifications carry.
 * - Origins: a session copied for one server is only ever sent to it.
 */
final class AppLinks {
    private AppLinks() {}

    private static final int MAX_PATH = 2048;
    /** "/#/" + an optional page name + an optional query of URL characters (no spaces, quotes or backslashes). */
    private static final Pattern ROUTE = Pattern.compile("/#/(?:([a-z][a-z0-9-]{0,31})(?:\\?[A-Za-z0-9._~%!$&'()*+,;=:@/?-]*)?)?");
    /** An API path on the server itself: no "//", fragment, spaces or backslashes. */
    private static final Pattern SERVER_PATH = Pattern.compile("/(?!/)[A-Za-z0-9._~%!$&'()*+,;=:@/-]*(?:\\?[A-Za-z0-9._~%!$&'()*+,;=:@/?-]*)?");
    /** Pages a browser may hand over (web/src/lib/openInApp.ts): invites, also under their pre-2026-11 name. */
    private static final java.util.Set<String> HANDOFF = new java.util.HashSet<>(java.util.Arrays.asList("invite", "convite"));

    /** The page of an in-app route ("" for the default page), or null when path is not one. */
    static String routePage(String path) {
        if (path == null || path.length() > MAX_PATH) return null;
        java.util.regex.Matcher m = ROUTE.matcher(path);
        if (!m.matches()) return null;
        return m.group(1) == null ? "" : m.group(1);
    }

    /** A page the app's own notifications may open. */
    static boolean isAppRoute(String path) {
        return routePage(path) != null;
    }

    /** A page a browser link (sentinela://open?path=...) may open: invites only. */
    static boolean isHandoffRoute(String path) {
        String page = routePage(path);
        return page != null && HANDOFF.contains(page);
    }

    // ---------- notification tokens ----------

    /** HMAC-SHA256 of the path under the install's own key, hex; "" when it cannot be made. */
    static String sign(byte[] key, String path) {
        if (key == null || key.length < 16 || path == null) return "";
        try {
            Mac mac = Mac.getInstance("HmacSHA256");
            mac.init(new SecretKeySpec(key, "HmacSHA256"));
            byte[] d = mac.doFinal(("open-path\n" + path).getBytes(StandardCharsets.UTF_8));
            StringBuilder sb = new StringBuilder(d.length * 2);
            for (byte b : d) sb.append(String.format(Locale.ROOT, "%02x", b));
            return sb.toString();
        } catch (Exception e) {
            return "";
        }
    }

    /** True only for an in-app route carrying the token sign() made for it (constant time). */
    static boolean verify(byte[] key, String path, String token) {
        if (token == null || !isAppRoute(path)) return false;
        String want = sign(key, path);
        return !want.isEmpty() && MessageDigest.isEqual(
                want.getBytes(StandardCharsets.US_ASCII), token.getBytes(StandardCharsets.US_ASCII));
    }

    // ---------- origins and sessions ----------

    /** "https://host:port" (lower-case host, explicit port) of an HTTPS URL without user info, else null. */
    static String origin(String url) {
        if (url == null) return null;
        try {
            URI u = new URI(url);
            if (!"https".equalsIgnoreCase(u.getScheme()) || u.getRawUserInfo() != null) return null;
            String host = u.getHost();
            if (host == null || host.isEmpty()) return null;
            int port = u.getPort() == -1 ? 443 : u.getPort();
            return "https://" + host.toLowerCase(Locale.ROOT) + ":" + port;
        } catch (Exception e) {
            return null;
        }
    }

    static boolean sameOrigin(String a, String b) {
        String oa = origin(a);
        return oa != null && oa.equals(origin(b));
    }

    /**
     * server + path when path is a plain path on that server (it cannot point
     * at another host, carry a fragment or escape with "//" or "\"), else null.
     */
    static String onServer(String server, String path) {
        if (server == null || path == null || path.length() > MAX_PATH || !SERVER_PATH.matcher(path).matches()) return null;
        String base = server;
        while (base.endsWith("/")) base = base.substring(0, base.length() - 1);
        String url = base + path;
        return sameOrigin(server, url) ? url : null;
    }

    /**
     * A session copied for boundOrigin with boundPin may go to server, which
     * the app now trusts with pin: same origin and the same certificate pin
     * ("" = publicly trusted). A copy with no recorded origin never matches.
     */
    static boolean boundTo(String boundOrigin, String boundPin, String server, String pin) {
        return boundOrigin != null && boundPin != null && pin != null
                && sameOrigin(boundOrigin, server) && boundPin.equalsIgnoreCase(pin);
    }

    /**
     * True when url with pin is the house the app is paired with now: its
     * current address and certificate, or one of the ways in the house itself
     * listed (alts: {url, pin} pairs) with that pin. Anything else, also the
     * same address with another certificate, is another house.
     */
    static boolean sameHouse(String currentUrl, String currentPin, java.util.List<String[]> alts, String url, String pin) {
        if (url == null || pin == null) return false;
        String u = trim(url);
        if (u.isEmpty()) return false;
        if (currentUrl != null && u.equals(trim(currentUrl)) && pin.equalsIgnoreCase(currentPin == null ? "" : currentPin)) return true;
        if (alts != null) {
            for (String[] a : alts) {
                if (a != null && a.length == 2 && a[0] != null && u.equals(trim(a[0])) && pin.equalsIgnoreCase(a[1] == null ? "" : a[1])) return true;
            }
        }
        return false;
    }

    private static String trim(String s) {
        int end = s.length();
        while (end > 0 && s.charAt(end - 1) == '/') end--;
        return s.substring(0, end);
    }

    /** The sentinela_session value in a Cookie header, or null. */
    static String sessionValue(String cookieHeader) {
        if (cookieHeader == null) return null;
        for (String part : cookieHeader.split(";")) {
            String p = part.trim();
            if (p.startsWith("sentinela_session=")) {
                String v = p.substring("sentinela_session=".length());
                return v.isEmpty() ? null : v;
            }
        }
        return null;
    }
}
