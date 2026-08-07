# Roadmap

Forward-looking capabilities that are **deliberately deferred** out of the
current release, with the decision that deferred them. Nothing here is a promise
of a date; items graduate when a concrete need appears (the D8/D4 "add it when a
real case appears" stance). For what the *current* release does not do yet, see
[`limitations.md`](limitations.md).

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
