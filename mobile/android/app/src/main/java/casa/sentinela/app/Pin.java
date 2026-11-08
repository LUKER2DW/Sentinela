package casa.sentinela.app;

import android.net.http.SslCertificate;
import android.os.Bundle;

import java.net.HttpURLConnection;
import java.net.URL;
import java.security.KeyStore;
import java.security.MessageDigest;
import java.security.cert.CertificateException;
import java.security.cert.X509Certificate;
import java.util.Locale;

import javax.net.ssl.HttpsURLConnection;
import javax.net.ssl.SSLContext;
import javax.net.ssl.SSLSocketFactory;
import javax.net.ssl.TrustManager;
import javax.net.ssl.TrustManagerFactory;
import javax.net.ssl.X509TrustManager;

/**
 * A house reached by a public address may use a self-signed certificate
 * (the ISP blocks the ports Let's Encrypt needs, ADR-0019 P1). Besides the
 * normal system trust, the app accepts exactly that one certificate: its
 * SHA-256 comes with the server address in the pairing link or code the
 * house shows (Sistema -> Aplicativo), and is saved with it (Connection). A build
 * may also carry a default pair (SENTINELA_DOMAIN_URL / _PUBLIC_CERT_SHA256).
 * Any other untrusted certificate still fails, so a man in the middle gets
 * the same error as without a pin.
 */
final class Pin {
    private Pin() {}

    private static volatile android.content.Context app;

    /** Remembers the app context so every entry point reads the saved pin. */
    static void init(android.content.Context c) {
        if (app == null && c != null) app = c.getApplicationContext();
    }

    /** The pin of the chosen server, "" when it uses a publicly trusted certificate. */
    static String expected() {
        android.content.Context c = app;
        return c == null ? "" : Connection.pin(c);
    }

    static boolean active() {
        return !expected().isEmpty();
    }

    static String sha256(byte[] der) {
        try {
            byte[] d = MessageDigest.getInstance("SHA-256").digest(der);
            StringBuilder sb = new StringBuilder(d.length * 2);
            for (byte b : d) sb.append(String.format(Locale.ROOT, "%02x", b));
            return sb.toString();
        } catch (Exception e) {
            return "";
        }
    }

    static boolean matches(byte[] der) {
        String want = expected();
        return !want.isEmpty() && der != null && sha256(der).equals(want.toLowerCase(Locale.ROOT));
    }

    /** The WebView's view of a certificate (SslError.getCertificate()). */
    static boolean matches(SslCertificate cert) {
        if (cert == null) return false;
        Bundle b = SslCertificate.saveState(cert);
        return b != null && matches(b.getByteArray("x509-certificate"));
    }

    /** Opens a connection that also trusts the pinned certificate. */
    static HttpURLConnection open(String url) throws Exception {
        HttpURLConnection c = (HttpURLConnection) new URL(url).openConnection();
        if (c instanceof HttpsURLConnection && active()) {
            ((HttpsURLConnection) c).setSSLSocketFactory(factory());
        }
        return c;
    }

    private static SSLSocketFactory factory;

    private static X509TrustManager systemTrust() throws Exception {
        TrustManagerFactory tmf = TrustManagerFactory.getInstance(TrustManagerFactory.getDefaultAlgorithm());
        tmf.init((KeyStore) null);
        X509TrustManager system = null;
        for (TrustManager t : tmf.getTrustManagers()) {
            if (t instanceof X509TrustManager) system = (X509TrustManager) t;
        }
        return system;
    }

    /** What a server presented: its certificate's SHA-256 and whether the system trusts it. */
    static final class Probe {
        final String pin;
        final boolean trusted;

        Probe(String pin, boolean trusted) {
            this.pin = pin;
            this.trusted = trusted;
        }
    }

    /**
     * Reads the certificate a server presents (first connection by address
     * only). The connection is still checked by the system's trust: an
     * untrusted certificate fails the handshake as always, and only its
     * fingerprint is kept from the failure, to show the person. Nothing is
     * sent to the server, and nothing is trusted by this call; the person
     * compares the code with System -> App and then saves the pin.
     */
    static Probe probe(String url) throws Exception {
        URL u = new URL(url);
        String host = u.getHost();
        int port = u.getPort() > 0 ? u.getPort() : 443;
        final X509TrustManager sys = systemTrust();
        final byte[][] leaf = new byte[1][];
        TrustManager tm = new X509TrustManager() {
            @Override
            public void checkClientTrusted(X509Certificate[] chain, String authType) throws CertificateException {
                throw new CertificateException("client certificates are not used");
            }

            @Override
            public void checkServerTrusted(X509Certificate[] chain, String authType) throws CertificateException {
                if (chain != null && chain.length > 0) leaf[0] = chain[0].getEncoded();
                if (sys == null) throw new CertificateException("no system trust");
                sys.checkServerTrusted(chain, authType);
            }

            @Override
            public X509Certificate[] getAcceptedIssuers() {
                return sys == null ? new X509Certificate[0] : sys.getAcceptedIssuers();
            }
        };
        SSLContext ctx = SSLContext.getInstance("TLS");
        ctx.init(null, new TrustManager[] {tm}, null);
        try (java.net.Socket raw = new java.net.Socket()) {
            raw.connect(new java.net.InetSocketAddress(host, port), 8000);
            raw.setSoTimeout(8000);
            try (javax.net.ssl.SSLSocket s = (javax.net.ssl.SSLSocket) ctx.getSocketFactory().createSocket(raw, host, port, true)) {
                s.startHandshake();
                return new Probe(leaf[0] == null ? "" : sha256(leaf[0]), true);
            } catch (javax.net.ssl.SSLException e) {
                if (leaf[0] == null) throw e;
                return new Probe(sha256(leaf[0]), false);
            }
        }
    }

    private static synchronized SSLSocketFactory factory() throws Exception {
        // matches() reads the current pin on every handshake: one factory serves all.
        if (factory != null) return factory;
        final X509TrustManager sys = systemTrust();
        TrustManager tm = new X509TrustManager() {
            @Override
            public void checkClientTrusted(X509Certificate[] chain, String authType) throws CertificateException {
                throw new CertificateException("client certificates are not used");
            }

            @Override
            public void checkServerTrusted(X509Certificate[] chain, String authType) throws CertificateException {
                if (chain != null && chain.length > 0 && matches(chain[0].getEncoded())) return;
                if (sys == null) throw new CertificateException("no system trust");
                sys.checkServerTrusted(chain, authType);
            }

            @Override
            public X509Certificate[] getAcceptedIssuers() {
                return sys == null ? new X509Certificate[0] : sys.getAcceptedIssuers();
            }
        };
        SSLContext ctx = SSLContext.getInstance("TLS");
        ctx.init(null, new TrustManager[] {tm}, null);
        factory = ctx.getSocketFactory();
        return factory;
    }
}
