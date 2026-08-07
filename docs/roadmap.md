# Roadmap

Forward-looking capabilities that are **deliberately deferred** out of the
current release, with the decision that deferred them. Nothing here is a promise
of a date; items graduate when a concrete need appears (the D8/D4 "add it when a
real case appears" stance). For what the *current* release does not do yet, see
[`limitations.md`](limitations.md).

## v1.0 — production ready (active)

The target after v0.1.0: a **feature-complete, production-ready** release that
also **commits the public API** (backward-compatibility guarantees begin at 1.0,
D24). v1.0 ships **single-node** by default; the Redis/multi-node backend is
built but kept **additive and optional** (ADR-0002), so horizontal scaling can
be turned on without a rewrite when load demands it. Tracked in the
[v1.0 milestone](https://github.com/rmoralesthompson/Liquid/milestone/2).

Sequenced so each workstream lands as its own PR; the four feature workstreams
open with a design ADR before implementation.

**Operational hardening (no design fork — first):**
1. **Production HTTP serving** — graceful shutdown, SSE connection draining,
   server timeouts, TLS ([#101](https://github.com/rmoralesthompson/Liquid/issues/101)).
2. **Observability** — health/readiness endpoints + a pluggable, dependency-free
   metrics seam ([#102](https://github.com/rmoralesthompson/Liquid/issues/102)).
3. **Production deployment guide** ([#103](https://github.com/rmoralesthompson/Liquid/issues/103)).

**Features:**
4. **`(input)` / `(change)` events** — extends D12 through the same allowlist +
   payload-contract path ([#104](https://github.com/rmoralesthompson/Liquid/issues/104)).
5. **Forms & validation framework** — *design ADR first*
   ([#105](https://github.com/rmoralesthompson/Liquid/issues/105)).
6. **DOM diffing / morph** — *design ADR first*
   ([#106](https://github.com/rmoralesthompson/Liquid/issues/106)).
7. **WebSocket transport** alongside SSE — *design ADR first*; pulled forward
   from v0.2 ([#107](https://github.com/rmoralesthompson/Liquid/issues/107)).
8. **Auth & durable sessions** — auth/authz plus the external session store that
   implements the `HydroState` `Snapshot()`/`Restore()` seam (also brings
   multi-node online additively); *design ADR first*; pulled forward from v0.2
   ([#108](https://github.com/rmoralesthompson/Liquid/issues/108)).

**Cut:**
9. **API-stability review & `v1.0.0` tag** — commit the public surface, migration
   notes, backward-compat policy from 1.0
   ([#109](https://github.com/rmoralesthompson/Liquid/issues/109)).

> The Redis-backed sessions and WebSocket items below were originally scoped for
> v0.2; both are **pulled forward into v1.0** (#108, #107) so that horizontal
> scaling and a low-latency transport are available in the production release.

## v0.2

### Redis-backed session persistence (multi-node)

- **What:** A serialization backend for the interactive session registry —
  `Snapshot()`/`Restore()` over Redis (or a similar external store) plugging into
  the `HydroState` seam that D2 already shaped for it. Enables a "resume
  anywhere" story so live sessions survive being routed to a different instance,
  removing the sticky-session requirement for horizontal scaling.
- **Why deferred:** v0.1 ships single-node by design (D2, D4). The seam exists
  but the backend is unbuilt; deferring is cheap and adding it later is additive.
  See [ADR-0002](adr/0002-single-node-sessions-v0.1.md) ([#89](https://github.com/rmoralesthompson/Liquid/issues/89)) —
  single-node does not block a v1.0, and this backend is the path to multi-node
  when a real multi-instance adopter appears.
- **Status:** Not started. The extension seam (`HydroState`) is in place from
  v0.1.

### WebSocket transport

- **What:** A WebSocket upgrade path for interactivity transport, added via a
  maintained library, alongside the existing v0.1 fetch + SSE transport (D3).
- **Why deferred:** v0.1's fetch-POST-events + SSE-push transport is stdlib-only
  and sufficient; WebSocket is added only if event latency demands it (D3, D4).
- **Status:** Not started.
