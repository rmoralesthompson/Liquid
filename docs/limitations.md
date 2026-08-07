# Limitations & scope — what Liquid v0.1 does *not* do yet

Liquid v0.1 has deliberate, honest boundaries. This page collects them in one place so you can decide, in a single read, whether v0.1 fits your deployment before you build on it. Each limitation names the driving decision (Dxx) in [`design-decisions.md`](design-decisions.md).

These are constraints of the current version, not permanent design positions — several have a planned path forward, noted below. Where a "later" story is not yet built, this page says so plainly rather than implying it exists.

## Single-node, in-memory sessions

Interactive component instances live in an **in-memory** registry, keyed by an opaque random session token and evicted by idle-timeout GC. There is no external session store in v0.1. (D2, D15)

**Concrete implication:** Liquid v0.1 runs as a **single node**. You cannot horizontally scale it across multiple instances behind a plain round-robin load balancer, because a live session's state exists only in the process that created it — a request routed to a different instance will not find that session and its interactivity breaks. If you must run more than one instance, you need **sticky sessions** (session affinity, so each browser session keeps hitting the same node). The [deployment guide](deployment.md) shows how to configure this.

The registry entry type (`HydroState`) is designed so a serialization backend — `Snapshot()`/`Restore()` over Redis or similar — can be added later, at which point a "resume anywhere" / multi-node story becomes possible. **That seam exists but is unbuilt.** Redis / external session persistence is explicitly deferred (D4), and the "resume anywhere" capability is not claimed until it ships. Single-node is a deliberate v0.1 choice, not a v1.0 blocker: the Redis-backed backend is on the [v0.2 roadmap](roadmap.md), decided in [ADR-0002](adr/0002-single-node-sessions-v0.1.md).

## SSE-only transport

Server-to-client push (live updates, deferred-render completions) is delivered over **Server-Sent Events (SSE)**; client-to-server events are a `fetch` POST to `/hydro-event`. Both are stdlib-only, with no WebSocket dependency. (D3)

**WebSocket is a v0.2 item**, added via a maintained library only if event latency demands it. It is out of scope for v0.1 (D4). If your use case requires low-latency bidirectional streaming today, v0.1's transport is one-way push over SSE plus request/response events — plan accordingly.

SSE reconnect triggers a full re-render of current state; there is **no missed-patch replay** in v0.1 (D20).

## Out of scope for v0.1 (deferred capabilities)

The following are deliberately **not** in v0.1 (D4, and the Out-of-Scope list of the v0.1 spec, [#1](https://github.com/rmoralesthompson/Liquid/issues/1)). Each is deferred, not rejected, unless noted:

- **Forms / validation framework** — `(submit)` + CSRF work, but there is no form-model, binding, or validation layer.
- **i18n / translation** (`*goTranslate`) — no internationalization directive or message catalog.
- **Durable / external session store** — sessions remain in-memory and single-node (see above); no external store yet ([ADR-0002](adr/0002-single-node-sessions-v0.1.md), roadmap). *Authentication and authorization now exist* — a session-bound principal, `RequireAuthenticated`-style guards, and login/logout that rotate the session and CSRF ([ADR-0007](adr/0007-auth-and-authorization.md), #108) — but **credential checking (the login mechanism) stays the app's job**, and `Login`/`Logout` are event-path operations.
- **DOM morphing / diffing** — a hydro event re-renders the whole component subtree at its `[hydroId]` boundary; the client **morphs** that re-render into the live DOM in place (Idiomorph, [ADR-0005](adr/0005-dom-morphing.md)), preserving element identity, scroll, focus, media, and CSS-transition state, and reordering keyed lists (by `id`) rather than rebuilding them (D14, D21). A patch still does not update attributes on the `[hydroId]` element itself.
- **`(input)` / `(change)` events** — v0.1 handles `(click)` and `(submit)`; keystroke- and change-level bindings are not wired.
- **Interceptor chains** — plain `net/http` middleware (`http.Handler`) suffices for v0.1; there is no framework interceptor pipeline.
- **Other deferred items:** scoped per-component assets, blueprint catalog, attribute-directive registry, hierarchical DI, URL/history patching, and state-preserving hot reload (`liquid dev` does a full refresh on rebuild, D16).

Also, per D9, **there are no performance claims** in v0.1 — no "faster than X", no superlatives. There is now a measured, reproducible [benchmark baseline](benchmarks.md) (informational, single-machine, not a guarantee and not a comparison), but no comparative or superlative performance claim is made anywhere.

## Payload contracts: untyped-payload closed domains need a guard

The D30 value axis lets a payload field typed as a Go const-set (an enum) be a
**closed domain** the dispatch seam enforces — an out-of-set value is refused
before any handler runs. The compiler must be able to *see* the payload type to
enumerate it. There are two ways to name it:

- **A typed payload parameter** (`func (c *T) Submit(f Form)`, #105) — the
  recommended shape. The compiler sees `Form` directly, so its closed-domain
  fields enforce **without a guard**, and an optional `Validate() liquid.Errors`
  runs at the seam. See [ADR-0004](adr/0004-typed-payload-handlers-and-validation.md).
- **A boundary guard** (`func (c *T) SubmitGuard(p Form) bool`, D30) — names the
  payload through the guard's parameter.

**Residual limitation:** a handler that takes the **untyped** `liquid.Event`
(`func (c *T) Submit(e liquid.Event)`) names no payload type, so a closed-domain
value it reads by string key is **not** enforced unless the action also declares
a guard. Such an action earns the `LSX018` build warning. To constrain values,
prefer a typed payload; a guard is the alternative. This residual coupling is
recorded in [ADR-0003](adr/0003-closed-domain-guard-coupling.md), amended by
ADR-0004 (D30).

## API stability: `v0.x`, no backward-compat promise

Liquid follows semver, and while it is in the `v0.x` range it makes **no backward-compatibility promises**. APIs, template directives, the JSON manifest schema, and wire formats may change between minor versions without a compatibility shim. (D24; the `liquid manifest --json` schema carries a version field and explicitly makes no backward-compat promise while `v0.x` — D26.)

Backward-compatibility guarantees begin at **1.0**. Until then, pin the version you build against and read release notes before upgrading.
