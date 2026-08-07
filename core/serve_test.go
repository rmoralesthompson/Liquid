package liquid

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"
)

// White-box: the drain closes internal SSE streams and empties the unexported
// registry, and the graceful-shutdown proof drives the unexported serve so a
// test can bind an ephemeral port and read its address. The wire-visible serve
// behavior has no seam otherwise — an idle SSE handler blocking shutdown is
// exactly the invisible fact this workstream fixes (#101).

// TestDrainAllClosesEverySessionStream is the unit proof of the graceful-
// shutdown drain: closing a live session's SSE stream so its serve loop — which
// returns only on the stream closing, its request context ending, or a write
// error — unblocks, and the registry is left empty.
func TestDrainAllClosesEverySessionStream(t *testing.T) {
	idle := time.Hour
	feed := NewBehaviorSubject(0)
	app := newPushApp(t, feed, WithLimits(Limits{SessionIdleTimeout: idle}))

	sess := renderWB(t, app)
	stream := newSSEStream()
	if !app.hydro.attachStream(sess.id, stream, app.now(), idle, app.limits.MaxStreamsPerSession) {
		t.Fatal("attachStream on a live session returned false")
	}

	app.drainSessions()

	select {
	case <-stream.done:
	case <-time.After(time.Second):
		t.Fatal("drainSessions did not close the live SSE stream")
	}

	app.hydro.mu.Lock()
	remaining := app.hydro.lru.Len()
	app.hydro.mu.Unlock()
	if remaining != 0 {
		t.Errorf("registry left %d sessions after drain, want 0", remaining)
	}
}

// TestServeShutsDownGracefullyWithLiveSSE drives the real Serve path: with an
// idle SSE connection open, cancelling the context must shut the server down
// promptly. http.Server.Shutdown alone would hang here — it does not cancel the
// SSE handler's request context — so completing well within ShutdownTimeout
// proves the OnShutdown drain unblocked it.
func TestServeShutsDownGracefullyWithLiveSSE(t *testing.T) {
	feed := NewBehaviorSubject(0)
	app := newPushApp(t, feed)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	base := "http://" + ln.Addr().String()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.serve(ctx, ln, ServeConfig{}.withDefaults()) }()

	// Establish a live interactive session, then open a long-lived SSE stream on
	// it and leave it idle. The session cookie is Secure, which Go's cookiejar
	// would refuse to resend over plain HTTP, so carry it by hand — as the real
	// browser runtime does over a TLS/localhost origin.
	resp, err := http.Get(base + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	_ = resp.Body.Close()
	var session *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookieName {
			session = c
		}
	}
	if session == nil {
		t.Fatal("GET / did not set a session cookie; no live session to stream")
	}

	sseReq, _ := http.NewRequest(http.MethodGet, base+"/hydro-sse", nil)
	sseReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session.Value})
	sseResp, err := http.DefaultClient.Do(sseReq)
	if err != nil {
		t.Fatalf("GET /hydro-sse: %v", err)
	}
	defer func() { _ = sseResp.Body.Close() }()
	if sseResp.StatusCode != http.StatusOK {
		t.Fatalf("SSE status = %d, want 200 (no live session established?)", sseResp.StatusCode)
	}

	// Trigger graceful shutdown; it must finish well within ShutdownTimeout.
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve returned error on graceful shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not shut down within 5s; the idle SSE connection hung shutdown")
	}
}
