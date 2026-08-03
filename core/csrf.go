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

// mintCSRF issues a CSRF token bound to sessionID, expiring ttl — the
// session idle window (D15/D2) — from now, encoded expiryUnix:signature
// (D15 as amended by #45). The session ID is signed over but never encoded:
// the token is stamped into the DOM (meta tag, hidden inputs) while the
// session cookie is HttpOnly, so embedding the ID would hand DOM-reading
// script the cookie's value. Tokens are regenerated on every full-page
// render, so the window only matters for a page left open untouched.
func mintCSRF(secret []byte, sessionID string, ttl time.Duration, now time.Time) string {
	expiry := now.Add(ttl).Unix()
	return strconv.FormatInt(expiry, 10) + ":" + csrfSignature(secret, sessionID, expiry)
}

// validCSRF reports whether token is an unexpired token this server minted
// for sessionID — recovered from the request's cookie, never from the token
// (#45): the expiry must be ahead of now, and the signature must recompute
// over the cookie's session ID (D15).
func validCSRF(secret []byte, token, sessionID string, now time.Time) bool {
	expiryText, signature, ok := strings.Cut(token, ":")
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
