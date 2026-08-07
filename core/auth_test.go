package liquid

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

var authTestSecret = []byte("auth-secret-32-bytes-000000000000")

func TestAuthCookieRoundTrips(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	val := mintAuthCookie(authTestSecret, "sess-1", "user-42", time.Hour, now)

	got, ok := verifyAuthCookie(authTestSecret, val, "sess-1", now.Add(time.Minute))
	if !ok || got != "user-42" {
		t.Fatalf("round trip = (%q, %v), want (\"user-42\", true)", got, ok)
	}
}

// TestAuthCookieRejectsTampering is the security core: the signed identity must
// be bound to its session and secret and unforgeable.
func TestAuthCookieRejectsTampering(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	val := mintAuthCookie(authTestSecret, "sess-1", "user-42", time.Hour, now)

	if _, ok := verifyAuthCookie(authTestSecret, val, "other-session", now); ok {
		t.Error("verified for a different session — identity is not session-bound")
	}
	if _, ok := verifyAuthCookie([]byte("different-secret-0000000000000000"), val, "sess-1", now); ok {
		t.Error("verified under a different secret — forgeable")
	}
	if _, ok := verifyAuthCookie(authTestSecret, val, "sess-1", now.Add(2*time.Hour)); ok {
		t.Error("expired cookie verified")
	}

	// Swap the principal segment, keep the old signature: must fail (the
	// signature covers the principal).
	parts := strings.Split(val, ":")
	parts[1] = base64.RawURLEncoding.EncodeToString([]byte("admin"))
	if _, ok := verifyAuthCookie(authTestSecret, strings.Join(parts, ":"), "sess-1", now); ok {
		t.Error("verified a swapped principal — signature does not cover identity (privilege escalation)")
	}

	for _, bad := range []string{"", "not-a-cookie", "a:b", "a:b:c:d", "notanumber:cA:sig"} {
		if _, ok := verifyAuthCookie(authTestSecret, bad, "sess-1", now); ok {
			t.Errorf("malformed value %q verified", bad)
		}
	}
}

// TestRotateSessionReKeysLiveState proves a session's live entry survives an id
// rotation (login/logout), so a logged-in session keeps its component state.
func TestRotateSessionReKeysLiveState(t *testing.T) {
	feed := NewBehaviorSubject(0)
	app := newPushApp(t, feed)
	sess := renderWB(t, app)

	app.hydro.rotateSession(sess.id, "rotated-id")

	app.hydro.mu.Lock()
	_, oldExists := app.hydro.sessions[sess.id]
	_, newExists := app.hydro.sessions["rotated-id"]
	app.hydro.mu.Unlock()
	if oldExists {
		t.Error("old session id still keyed after rotation")
	}
	if !newExists {
		t.Error("session not re-keyed to the new id — live state lost on rotation")
	}

	// A CSRF token minted for the old id must no longer validate — the fixation
	// defense (D15): rotating the id voids pre-rotation tokens.
	oldToken := mintCSRF(app.csrfSecret, sess.id, time.Hour, app.now())
	if validCSRF(app.csrfSecret, oldToken, "rotated-id", app.now()) {
		t.Error("a pre-rotation CSRF token still validates against the new session id")
	}
}
