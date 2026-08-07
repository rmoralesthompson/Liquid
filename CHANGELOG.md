# Changelog

All notable changes to Liquid are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and Liquid follows
[semantic versioning](https://semver.org/spec/v2.0.0.html). **Backward-compatibility
guarantees begin at 1.0** (D24): from v1.0.0 the public Go API, `.lsx` template
directives, and the wire/manifest formats follow semver — breaking changes wait
for a new major version. (Through the `v0.x` range there was no such promise.)

## [1.0.0] — 2026-08-07

The **production-ready** release. Everything from v0.1.0 plus operational
hardening, a forms/validation layer, in-place DOM morphing, and auth — and the
public API is now **committed** (backward-compatibility guarantees begin here,
D24). Ships single-node by default; the durable/multi-node session store stays
additive on the roadmap ([ADR-0002](docs/adr/0002-single-node-sessions-v0.1.md)).

### Added — operational
- **`App.Serve(ctx, ServeConfig)`** — production HTTP server with graceful
  shutdown that **drains live SSE streams**, sane timeouts (`ReadHeaderTimeout`,
  `IdleTimeout`; no `WriteTimeout`, which would sever SSE), and optional TLS.
  Wire `signal.NotifyContext` for `SIGINT`/`SIGTERM`.
- **Health probes** `/liquid/health` (liveness) and `/liquid/ready` (readiness,
  `503` while draining) under the framework namespace.
- **`Metrics`** — a dependency-free observability seam (`WithMetrics`) reporting
  page renders, `/hydro-event` dispatches, and SSE connect/disconnect, plus
  `App.LiveSessions()` as a gauge.
- **[Deployment guide](docs/deployment.md)** — TLS/reverse-proxy, sticky
  sessions, health checks, graceful shutdown, SSE proxy-buffering, containers.

### Added — interactivity
- **`(input)` and `(change)` event bindings** (`(input)` debounced ~200ms),
  through the same allowlist + CSRF + payload-contract path as `(click)`/`(submit)`.
- **In-place DOM morphing** of patches (vendored [Idiomorph](https://github.com/bigskysoftware/idiomorph),
  [ADR-0005](docs/adr/0005-dom-morphing.md)) — preserves scroll, focus, media,
  CSS transitions, and keyed-list identity instead of a wholesale swap.

### Added — forms & validation ([ADR-0004](docs/adr/0004-typed-payload-handlers-and-validation.md))
- **Typed payload handlers** — `func (c *C) Submit(f Form)` (and
  `func(f Form, e liquid.Event)`): the framework binds the payload, enforces D30
  closed domains, and runs an optional `Validate() liquid.Errors`, calling the
  handler only when valid.
- **`liquid.Errors`** and the **`Validator`** interface for per-field validation
  rendered back into the component.
- A typed payload names its type to the compiler, so **closed-domain fields
  enforce without a guard** — closing the #85 gap (ADR-0003 amended).

### Added — auth ([ADR-0007](docs/adr/0007-auth-and-authorization.md))
- **Session-bound identity**: `ctx.Principal()`, and event-path `ctx.Login()` /
  `ctx.Logout()` backed by a signed `liquid_auth` cookie.
- **Authorization guards**: `RequireAuthenticated()` / `RequireAuthenticatedElse(path)`,
  composing with `WithGuard`.
- **Login rotates the session id**, which (because CSRF binds to it) voids
  pre-login CSRF tokens — the session-fixation defense D15 called for.

### Changed
- The document shell loads the morph library before the runtime script.
- CHANGELOG/README/limitations updated for the committed 1.0 API.

### Deferred (on the roadmap, not in 1.0)
- **WebSocket transport** — kept out to preserve the stdlib-only transport;
  added when a real latency-bound case appears ([ADR-0006](docs/adr/0006-defer-websocket-past-v1.md)).
- **Durable / multi-node session store** — the `Snapshot()/Restore()` seam is
  ready; the backend lands additively in a later release (ADR-0002).

## [0.1.0] — 2026-08-06

First tagged release. Liquid is a server-driven UI framework for Go: an
Angular-style component model, LiveView-style interactivity over `fetch` + SSE,
and ahead-of-time template compilation that doubles as a guardrail for
AI-generated code. This release ships the core runtime, the `.lsx` compiler, the
dev server, and a full example app.

### Components & templates
- **`.lsx` component templates**, compiled ahead of time by preprocessing into
  `html/template` so every interpolation flows through contextual escaping
  (D1, D6). Templates are transformed as a parsed HTML node tree, not by regex.
- **Component model**: plain Go structs implementing `Selector()` / `Template()`,
  with per-request (or per interactive session) instances — never shared mutable
  singletons across requests (D2).
- **Nesting**: parent templates embed child components; a child inherits the
  parent's interactive session (D14).
- **Dependency injection**: a minimal reflection-based injector wires services
  into components via struct tags (D8).
- **Request lifecycle**: `OnInit(ctx liquid.Ctx) error` for per-request setup
  with a first-class error contract (D18).

### Interactivity
- **Transport**: client→server events are a `fetch` POST to `/hydro-event`;
  server→client updates stream over Server-Sent Events. Stdlib-only, no
  WebSocket dependency in v0.1 (D3).
- **Events**: `(click)` and `(submit)` bindings, with an optional typed
  `liquid.Event` handler parameter (D11, D12).
- **Patch model**: a handled event re-renders the component's `[hydroId]`
  subtree and swaps it in; navigation uses a patch-or-redirect envelope (D14,
  D19).
- **Derived reactive state**: stream combinators over `BehaviorSubject[T]` for
  values computed from other values and pushed live (D25).

### Security & hardening
- **Compile-time action allowlist**: events dispatch only to methods the
  compiler derived from template bindings — never `MethodByName` on client input
  (D10).
- **Sessions & CSRF**: one opaque, random session cookie with per-session hydro
  tokens nested under it; CSRF tokens are stateless HMACs checked before any
  action lookup, re-minted per dispatch against a sliding idle deadline (D15).
- **Serialized dispatch**: events for one live instance are serialized by a
  per-session mutex — never dispatched concurrently against the same instance
  (D20).
- **Bounded session registry**: per-session and global caps with idle-timeout
  eviction, so unauthenticated traffic cannot grow it without limit (D20).
- **Bounded event payloads**: request bodies are capped before they are read
  (D20).
- **Payload contracts**: a value-constrained dispatch guard enforces closed
  domains and boundary checks at the dispatch seam, before the handler runs
  (D30).

### CLI & developer experience
- **`liquid` CLI**: ahead-of-time `.lsx` compiler, component scaffolder, and a
  dev server (D13).
- **Agent-first diagnostics**: structured compiler diagnostics designed to feed
  back to a generating agent (D13).
- **Dev server**: SSE-triggered full refresh on rebuild plus an error overlay
  (D16).
- **Machine-readable manifest**: `liquid manifest --json` emits the component and
  action surface, with a versioned schema (D26).
- **Deterministic render mode** for reproducible output in tests and snapshots
  (D28).
- **Reactivity leak check**: a `vet`-level analyzer flags leaked reactive
  subscriptions (D29).

### Testing
- **`liquidtest`**: an httptest-based harness to drive components — render, fire
  events, and read SSE pushes through the real handler stack (D23).
- **Render snapshot assertions** in `liquidtest` (D27).

### Accessibility
- Patch application preserves focus, with an accessibility checklist for
  interactive updates (D21).

### Documents & assets
- Document `<head>` management and a static-asset serving path (D22).

### Observability
- Logging through `log/slog` with a pluggable handler; no `fmt.Println` in core
  (D24). Minimum Go version 1.23 (D24).

### Performance
- A measured, reproducible [benchmark baseline](docs/benchmarks.md) at the
  render and `/hydro-event` seams. The numbers are informational and
  single-machine — **not a guarantee and not a comparison** (D9).

### Known limitations
This release is honest about its boundaries — see
[what Liquid v0.1 does not do yet](docs/limitations.md) before adopting. In
short: **single-node** (in-memory sessions; multi-node needs sticky sessions,
with a Redis-backed store on the [v0.2 roadmap](docs/roadmap.md), decided in
[ADR-0002](docs/adr/0002-single-node-sessions-v0.1.md)); **SSE-only** transport
(WebSocket is a v0.2 item); no forms/validation framework, i18n, auth, or
DOM-diffing yet; and **`v0.x`, no backward-compat promise**. Per D9, Liquid
makes no comparative or superlative performance claims.

[1.0.0]: https://github.com/rmoralesthompson/Liquid/releases/tag/v1.0.0
[0.1.0]: https://github.com/rmoralesthompson/Liquid/releases/tag/v0.1.0
