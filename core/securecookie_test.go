package liquid

import (
	"bytes"
	"crypto/tls"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The Secure session cookie (D15) is minted unconditionally. A browser silently
// drops it when the request is plain HTTP on a non-localhost host, so the app
// re-mints a session every request and interactivity dies with no visible
// cause. ensureSession warns once about exactly that footgun (#47). These are
// white-box because the once-guard and the mint path are internal; the
// observable behavior is the log line and the cookie itself.

// warnApp builds an interactive app whose warnings land in the returned buffer.
func warnApp(t *testing.T) (*App, *bytes.Buffer) {
	t.Helper()
	var logs bytes.Buffer
	app := New(WithLogger(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))))
	if err := app.Route("/", &idleCounter{}); err != nil {
		t.Fatalf("Route: %v", err)
	}
	return app, &logs
}

// getFrom renders / with the given Host over HTTP or (secure) HTTPS.
func getFrom(app *App, host string, secure bool) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = host
	if secure {
		req.TLS = &tls.ConnectionState{}
	}
	app.ServeHTTP(httptest.NewRecorder(), req)
}

func countWarn(logs string) int {
	return strings.Count(logs, "level=WARN")
}

func TestInsecureCookieWarnsOnPlainHTTPNonLocalhost(t *testing.T) {
	app, logs := warnApp(t)

	getFrom(app, "internal.example:8080", false)

	if n := countWarn(logs.String()); n != 1 {
		t.Fatalf("warning count = %d, want 1\nlogs:\n%s", n, logs.String())
	}
	// The message must point the operator at the actual cause: a Secure cookie
	// dropped without TLS.
	got := logs.String()
	for _, want := range []string{"Secure", "TLS"} {
		if !strings.Contains(got, want) {
			t.Errorf("warning missing %q; got:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "internal.example:8080") {
		t.Errorf("warning omits the offending host; got:\n%s", got)
	}
}

func TestInsecureCookieWarnsAtMostOnce(t *testing.T) {
	app, logs := warnApp(t)

	// Every request re-mints because the cookie never sticks; the warning must
	// not repeat.
	for i := 0; i < 5; i++ {
		getFrom(app, "10.0.0.5", false)
	}
	if n := countWarn(logs.String()); n != 1 {
		t.Fatalf("warning count = %d after 5 mints, want 1\nlogs:\n%s", n, logs.String())
	}
}

func TestInsecureCookieLocalhostExempt(t *testing.T) {
	// Browsers treat http://localhost as trustworthy, so the Secure cookie
	// sticks and liquid dev's default serving must stay silent.
	for _, host := range []string{"localhost", "localhost:3000", "127.0.0.1", "127.0.0.1:8080", "[::1]", "[::1]:8080", "::1"} {
		app, logs := warnApp(t)
		getFrom(app, host, false)
		if n := countWarn(logs.String()); n != 0 {
			t.Errorf("host %q warned %d times, want 0 (localhost is exempt)\nlogs:\n%s", host, n, logs.String())
		}
	}
}

func TestInsecureCookieTLSExempt(t *testing.T) {
	app, logs := warnApp(t)

	getFrom(app, "app.example.com", true)

	if n := countWarn(logs.String()); n != 0 {
		t.Fatalf("TLS request warned %d times, want 0\nlogs:\n%s", n, logs.String())
	}
}
