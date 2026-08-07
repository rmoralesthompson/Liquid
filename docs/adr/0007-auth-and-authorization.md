# ADR-0007: Session-bound auth and authorization

- **Status:** Accepted
- **Date:** 2026-08-07
- **Tracking issue:** [#108](https://github.com/rmoralesthompson/Liquid/issues/108)
- **Relates to:** decisions D15 (session & CSRF), D18 (Ctx), D19 (guards); ADR-0002 (durable store, deferred); the v1.0 milestone (#2)

## Context

Liquid has route guards (`WithGuard`, D19), an opaque session cookie, and
session-bound CSRF (D15), but **no notion of identity**: a guard cannot ask "who
is this?", and D15 noted the missing piece explicitly — "there is no
auth/privilege-escalation flow to rotate CSRF tokens against yet." The v1.0
program adds a bounded auth layer (#108).

Two boundaries shape the scope:

- **Authentication (verifying credentials) is the app's job.** How a user proves
  who they are — password, OAuth, SSO — is application-specific, and an app can
  already wrap Liquid's `http.Handler` with any middleware. The framework should
  own only what it is uniquely positioned to: attaching an identity to the hydro
  session, exposing it to guards and lifecycle hooks, and rotating session/CSRF
  state on a privilege change.
- **The durable session store stays deferred** (ADR-0002, the topology call): auth
  must not require it. Identity therefore lives in a **stateless signed cookie**,
  not a server-side store — which also means it works unchanged when the durable
  store lands.

## Decision

Add a session-bound principal, authorization integration, and login/logout with
session rotation.

**Identity as a signed cookie.** `Login(principal)` issues a `liquid_auth`
cookie carrying `expiry:principal:HMAC(authSecret, sessionID:principal:expiry)` —
signed exactly as CSRF is (D15), **bound to the session id** so it cannot be
replayed on another session, and `HttpOnly`+`Secure`+`SameSite=Lax`. A new
per-process `authSecret` signs it. No server-side store — consistent with the
deferred durable store.

**Reading identity.** `ctx.Principal() (string, bool)` returns the verified
principal, resolved once per request from the `liquid_auth` cookie against the
current session id. Available in **guards, `OnInit`, and event handlers** (a
shared per-request carrier behind `Ctx`, so a value-copied `Ctx` still sees it).

**Authorization.** Guards read `ctx.Principal()`; `liquid.RequireAuthenticated()`
(deny) and `liquid.RequireAuthenticatedElse(path)` (redirect to a login route)
are provided; apps write role/permission guards over the principal. Guards run
before instantiation (D19), so an unauthorized request never constructs the
component.

**Login rotates the session (fixation defense, D15).** `Login`/`Logout` mint a
fresh session id, migrate the live session to it in the registry, set the new
`liquid_session` cookie, and set/clear the `liquid_auth` cookie. Because CSRF
signs over the session id, **rotation invalidates every pre-login CSRF token for
free** — no separate CSRF-revocation path needed.

**Login/Logout are event-path operations.** They are called from an event
handler (`func(e liquid.Event)` or `func(p Form, e liquid.Event)`) — the real
login flow is a form submit — where the session concretely exists and the
response carries the rotated cookies and re-minted CSRF. Calling them from a
render/`OnInit` or a background load returns an error (the render path's session
is minted lazily; rotating it there is out of scope for v1.0). Reading
`Principal()` works everywhere.

## Consequences

- **Positive.** Identity flows into guards and components; authorization composes
  with the existing guard system; login closes D15's session-fixation / CSRF-
  rotation gap. All stateless — no dependency on the deferred durable store, and
  it keeps working when that store lands.
- **Positive.** No new dependency; the signed cookie mirrors the CSRF primitive.
- **Negative / accepted.** `Login`/`Logout` are event-path only in v1.0; a
  render-time login must issue the event itself. The principal is an opaque
  app-chosen string (an id/username), not a framework user model — the app maps
  it to a richer identity. Authentication (credential checking) remains the
  app's responsibility.
- **Follow-up.** When the durable store lands (v1.x), the principal can
  optionally be resolved through it; render-path login can be revisited.
