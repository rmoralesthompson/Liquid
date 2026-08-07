package liquid

// This file is the v1.0 auth layer (#108, ADR-0007): a session-bound principal
// carried in a signed cookie, authorization guards over it, and login/logout
// that rotate the session id (fixation defense, D15). It owns no credential
// checking — that stays the app's job — and no server-side store, so it does
// not depend on the deferred durable session store (ADR-0002).

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// authCookieName carries the signed principal (identity) for a session. Like the
// session cookie it is HttpOnly + Secure + SameSite=Lax.
const authCookieName = "liquid_auth"

// DefaultAuthTTL is how long a liquid_auth cookie stays valid after login.
const DefaultAuthTTL = 24 * time.Hour

// errNoAuthResponse is returned by Login/Logout when called outside an event
// handler — there is no response to set cookies on, and no concrete session to
// rotate (ADR-0007).
var errNoAuthResponse = errors.New("liquid: Login/Logout must be called from an event handler")

// authSignature signs a principal to a session with the app's auth secret,
// mirroring the CSRF construction (D15): HMAC-SHA256 over sessionID:principal:expiry.
func authSignature(secret []byte, sessionID, principal string, expiry int64) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(sessionID + ":" + principal + ":" + strconv.FormatInt(expiry, 10)))
	return hex.EncodeToString(mac.Sum(nil))
}

// mintAuthCookie encodes a signed identity value expiry:base64(principal):signature.
// The principal is base64url-encoded so it cannot collide with the ':' delimiter;
// the session ID is signed over but never encoded (the cookie is HttpOnly).
func mintAuthCookie(secret []byte, sessionID, principal string, ttl time.Duration, now time.Time) string {
	expiry := now.Add(ttl).Unix()
	enc := base64.RawURLEncoding.EncodeToString([]byte(principal))
	return strconv.FormatInt(expiry, 10) + ":" + enc + ":" + authSignature(secret, sessionID, principal, expiry)
}

// verifyAuthCookie recovers the principal from a signed value, checking the
// expiry and the signature against the request's session ID. Returns ok=false on
// any tampering, expiry, or session mismatch.
func verifyAuthCookie(secret []byte, value, sessionID string, now time.Time) (string, bool) {
	parts := strings.Split(value, ":")
	if len(parts) != 3 {
		return "", false
	}
	expiry, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || !now.Before(time.Unix(expiry, 0)) {
		return "", false
	}
	principalBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", false
	}
	principal := string(principalBytes)
	want := authSignature(secret, sessionID, principal, expiry)
	if !hmac.Equal([]byte(parts[2]), []byte(want)) {
		return "", false
	}
	return principal, true
}

// resolvePrincipal reads and verifies the identity cookie for the request's
// session, returning "" for an anonymous or unverifiable request.
func (a *App) resolvePrincipal(r *http.Request, sessionID string) string {
	if sessionID == "" {
		return ""
	}
	ck, err := r.Cookie(authCookieName)
	if err != nil || ck.Value == "" {
		return ""
	}
	principal, ok := verifyAuthCookie(a.authSecret, ck.Value, sessionID, a.now())
	if !ok {
		return ""
	}
	return principal
}

// setSessionCookie writes the liquid_session cookie (HttpOnly, Secure, Lax) — the
// single place the session cookie is stamped, shared by first-render session
// establishment and login rotation.
func (a *App) setSessionCookie(w http.ResponseWriter, r *http.Request, id string) {
	a.warnInsecureCookie(r)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    id,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

// setAuthCookie stamps the signed identity cookie for a session.
func (a *App) setAuthCookie(w http.ResponseWriter, r *http.Request, sessionID, principal string) {
	a.warnInsecureCookie(r)
	http.SetCookie(w, &http.Cookie{
		Name:     authCookieName,
		Value:    mintAuthCookie(a.authSecret, sessionID, principal, DefaultAuthTTL, a.now()),
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

// clearAuthCookie deletes the identity cookie (logout).
func (a *App) clearAuthCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     authCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

// sessionFromRequest returns the request's liquid_session cookie value, or "".
func sessionFromRequest(r *http.Request) string {
	if ck, err := r.Cookie(sessionCookieName); err == nil {
		return ck.Value
	}
	return ""
}

// authScope is the per-request auth carrier shared behind Ctx by pointer, so a
// value-copied Ctx still observes a login. It resolves the principal once and,
// in the event path, backs Login/Logout — which rotate the session and set the
// cookies.
type authScope struct {
	app       *App
	w         http.ResponseWriter // nil outside the event path (no response to write)
	r         *http.Request
	sessionID string // current session id; Login/Logout rotate it
	principal string // resolved identity, "" when anonymous
	mutable   bool   // Login/Logout allowed (event path only, ADR-0007)
}

// login rotates the session (fixation defense, D15) and issues the identity.
func (s *authScope) login(principal string) error {
	if !s.mutable || s.w == nil {
		return errNoAuthResponse
	}
	if principal == "" {
		return errors.New("liquid: Login requires a non-empty principal")
	}
	if err := s.rotate(); err != nil {
		return err
	}
	s.principal = principal
	s.app.setAuthCookie(s.w, s.r, s.sessionID, principal)
	return nil
}

// logout clears the identity and rotates the session.
func (s *authScope) logout() error {
	if !s.mutable || s.w == nil {
		return errNoAuthResponse
	}
	if err := s.rotate(); err != nil {
		return err
	}
	s.principal = ""
	s.app.clearAuthCookie(s.w)
	return nil
}

// rotate mints a fresh session id, migrates the live session to it, and stamps
// the new session cookie — so every pre-rotation CSRF token (bound to the old
// id) is void (D15).
func (s *authScope) rotate() error {
	newID, err := s.app.newToken()
	if err != nil {
		return fmt.Errorf("liquid: rotating session: %w", err)
	}
	s.app.hydro.rotateSession(s.sessionID, newID)
	s.sessionID = newID
	s.app.setSessionCookie(s.w, s.r, newID)
	return nil
}

// RequireAuthenticated is a guard that denies (403) a request with no verified
// principal (#108). Compose it with WithGuard; write role/permission guards by
// reading ctx.Principal() directly.
func RequireAuthenticated() Guard {
	return func(ctx Ctx) GuardResult {
		if _, ok := ctx.Principal(); ok {
			return Allow()
		}
		return Deny()
	}
}

// RequireAuthenticatedElse is RequireAuthenticated that redirects an anonymous
// request to path (a login route) instead of denying (D19).
func RequireAuthenticatedElse(path string) Guard {
	return func(ctx Ctx) GuardResult {
		if _, ok := ctx.Principal(); ok {
			return Allow()
		}
		return Redirect(path)
	}
}
