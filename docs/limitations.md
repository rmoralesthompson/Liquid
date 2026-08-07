# Limitations & scope — what Liquid v0.1 does *not* do yet

Liquid v0.1 has deliberate, honest boundaries. This page collects them in one place so you can decide, in a single read, whether v0.1 fits your deployment before you build on it. Each limitation names the driving decision (Dxx) in [`design-decisions.md`](design-decisions.md).

These are constraints of the current version, not permanent design positions — several have a planned path forward, noted below. Where a "later" story is not yet built, this page says so plainly rather than implying it exists.

## Single-node, in-memory sessions

Interactive component instances live in an **in-memory** registry, keyed by an opaque random session token and evicted by idle-timeout GC. There is no external session store in v0.1. (D2, D15)

**Concrete implication:** Liquid v0.1 runs as a **single node**. You cannot horizontally scale it across multiple instances behind a plain round-robin load balancer, because a live session's state exists only in the process that created it — a request routed to a different instance will not find that session and its interactivity breaks. If you must run more than one instance, you need **sticky sessions** (session affinity, so each browser session keeps hitting the same node).

The registry entry type (`HydroState`) is designed so a serialization backend — `Snapshot()`/`Restore()` over Redis or similar — can be added later, at which point a "resume anywhere" / multi-node story becomes possible. **That seam exists but is unbuilt.** Redis / external session persistence is explicitly deferred (D4), and the "resume anywhere" capability is not claimed until it ships. Single-node is a deliberate v0.1 choice, not a v1.0 blocker: the Redis-backed backend is on the [v0.2 roadmap](roadmap.md), decided in [ADR-0002](adr/0002-single-node-sessions-v0.1.md).

## SSE-only transport

Server-to-client push (live updates, deferred-render completions) is delivered over **Server-Sent Events (SSE)**; client-to-server events are a `fetch` POST to `/hydro-event`. Both are stdlib-only, with no WebSocket dependency. (D3)

**WebSocket is a v0.2 item**, added via a maintained library only if event latency demands it. It is out of scope for v0.1 (D4). If your use case requires low-latency bidirectional streaming today, v0.1's transport is one-way push over SSE plus request/response events — plan accordingly.

SSE reconnect triggers a full re-render of current state; there is **no missed-patch replay** in v0.1 (D20).

## Out of scope for v0.1 (deferred capabilities)

The following are deliberately **not** in v0.1 (D4, and the Out-of-Scope list of the v0.1 spec, [#1](https://github.com/rmoralesthompson/Liquid/issues/1)). Each is deferred, not rejected, unless noted:

- **Forms / validation framework** — `(submit)` + CSRF work, but there is no form-model, binding, or validation layer.
- **i18n / translation** (`*goTranslate`) — no internationalization directive or message catalog.
- **Auth / session persistence** — no authentication, authorization, or login flow; no external/durable session store (see single-node above). There is no auth/privilege-escalation flow to rotate CSRF tokens against yet (D15).
- **DOM morphing / diffing** — a hydro event re-renders the whole component subtree at its `[hydroId]` boundary and swaps `innerHTML`; there is no morphdom-style DOM merging (D14, D21). Full DOM reconciliation is the v0.2+ answer.
- **`(input)` / `(change)` events** — v0.1 handles `(click)` and `(submit)`; keystroke- and change-level bindings are not wired.
- **Interceptor chains** — plain `net/http` middleware (`http.Handler`) suffices for v0.1; there is no framework interceptor pipeline.
- **Other deferred items:** scoped per-component assets, blueprint catalog, attribute-directive registry, hierarchical DI, URL/history patching, and state-preserving hot reload (`liquid dev` does a full refresh on rebuild, D16).

Also, per D9, **there are no performance claims** in v0.1 — no "faster than X", no superlatives. There is now a measured, reproducible [benchmark baseline](benchmarks.md) (informational, single-machine, not a guarantee and not a comparison), but no comparative or superlative performance claim is made anywhere.

## Payload contracts: closed domains need a guard

The D30 value axis lets a payload field typed as a Go const-set (an enum) be a
**closed domain** the dispatch seam enforces — an out-of-set value is refused
before any handler runs. In v0.1 this enforcement is **coupled to declaring a
boundary guard**: because a handler takes the untyped `liquid.Event`, the only
place a payload type is named to the compiler is a
`func (c *T) <Action>Guard(p <Payload>) bool` method's parameter. So a
closed-domain field on an **unguarded** action is invisible to the compiler,
never enumerated, and **not enforced** — writing the enum field is not, by
itself, sufficient.

**Concrete implication:** to get closed-domain enforcement, declare a guard for
the action (even a `return true` guard that adds no extra check). An unguarded
payload action instead earns the `LSX018` build warning, whose suggestion names
this coupling. This is intended v0.1 behavior, not a bug — see
[ADR-0003](adr/0003-closed-domain-guard-coupling.md) (D30). It will be revisited
when typed-payload handlers make the payload type discoverable without a guard.

## API stability: `v0.x`, no backward-compat promise

Liquid follows semver, and while it is in the `v0.x` range it makes **no backward-compatibility promises**. APIs, template directives, the JSON manifest schema, and wire formats may change between minor versions without a compatibility shim. (D24; the `liquid manifest --json` schema carries a version field and explicitly makes no backward-compat promise while `v0.x` — D26.)

Backward-compatibility guarantees begin at **1.0**. Until then, pin the version you build against and read release notes before upgrading.
