package liquid

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// csrfTTL is how long a minted CSRF token stays valid. It tracks the session
// idle window (D15/D2); a configurable value is session hardening, tracked on
// ticket #9. Tokens are regenerated on every full-page render, so the window
// only matters for a page left open untouched.
const csrfTTL = time.Hour

// csrfTokenField returns the index of a component type's CSRFToken string
// field — the field the framework populates with the render's token (D15) —
// or -1 for a component without one.
func csrfTokenField(t reflect.Type) int {
	return stringFieldIndex(t, "CSRFToken")
}

// csrfSignature computes the token's HMAC-SHA256 signature over the
// sessionID:expiry pair (D15).
func csrfSignature(secret []byte, sessionID string, expiry int64) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(sessionID + ":" + strconv.FormatInt(expiry, 10)))
	return hex.EncodeToString(mac.Sum(nil))
}

// mintCSRF issues a CSRF token bound to sessionID, expiring csrfTTL from now,
// encoded sessionID:expiryUnix:signature (D15). Session IDs are base64url,
// so the colons unambiguously delimit the segments.
func mintCSRF(secret []byte, sessionID string, now time.Time) string {
	expiry := now.Add(csrfTTL).Unix()
	return sessionID + ":" + strconv.FormatInt(expiry, 10) + ":" + csrfSignature(secret, sessionID, expiry)
}

// validCSRF reports whether token is an unexpired token this server minted
// for sessionID: the embedded session must match the request's, the expiry
// must be ahead of now, and the signature must recompute (D15).
func validCSRF(secret []byte, token, sessionID string, now time.Time) bool {
	tokenSession, rest, ok := strings.Cut(token, ":")
	if !ok || tokenSession != sessionID {
		return false
	}
	expiryText, signature, ok := strings.Cut(rest, ":")
	if !ok {
		return false
	}
	expiry, err := strconv.ParseInt(expiryText, 10, 64)
	if err != nil || !now.Before(time.Unix(expiry, 0)) {
		return false
	}
	want := csrfSignature(secret, sessionID, expiry)
	return hmac.Equal([]byte(signature), []byte(want))
}
