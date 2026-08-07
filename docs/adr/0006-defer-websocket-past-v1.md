# ADR-0006: Defer WebSocket transport past v1.0

- **Status:** Accepted
- **Date:** 2026-08-07
- **Tracking issue:** [#107](https://github.com/rmoralesthompson/Liquid/issues/107)
- **Relates to:** decisions D3 (transport), D4 (v0.1 scope / deferrals), D24 (prefer stdlib); the v1.0 milestone (#2)

## Context

D3 fixed Liquid's interactivity transport as **fetch-POST events + SSE push** —
deliberately stdlib-only, with no WebSocket dependency — and D4 listed WebSocket
as a deferred capability, "added only if event latency demands it." The v1.0
program initially pulled WebSocket forward into v1.0 as part of a
"feature-complete" release ([roadmap](../roadmap.md), #107).

Revisiting it before implementation, the decisive facts:

- **Go has no standard-library WebSocket.** Adding it means importing a
  third-party library (`coder/websocket`, `gorilla/websocket`, …) into `core` —
  which would be the framework's **first third-party runtime dependency** in
  core. Today core depends only on the standard library and `golang.org/x/net`
  (used by the compiler's HTML parsing), so this is a real departure from the
  stdlib-first posture (D24).
- **SSE + fetch POST already covers server-driven UI.** Server→client push (live
  updates, deferred-render completions) rides SSE; client→server events are a
  fetch POST. That is the whole premise of D3, and it is sufficient for the
  interactivity Liquid targets. WebSocket's advantage is lower-latency
  *bidirectional* streaming — valuable for high-frequency cases
  (collaborative editing, games) that are not v1.0's target.
- **It is additive.** The event dispatch, CSRF (D15), and per-session
  serialization (D20) machinery is transport-agnostic; a WebSocket upgrade path
  can be added later without a rewrite. Deferring costs nothing structurally.

## Decision

**Do not add WebSocket in v1.0.** v1.0's transport stays **stdlib-only SSE +
fetch POST** (D3). WebSocket remains on the [roadmap](../roadmap.md) as a
**post-v1.0** item, to be added when a concrete latency-bound case appears (D4) —
at which point it is an additive, opt-in transport alongside SSE, not a
replacement.

This removes #107 from the v1.0 milestone; the remaining v1.0 feature workstreams
are forms (#105, done), DOM morphing (#106, done), and auth & durable sessions
(#108), then the API-stability cut (#109).

## Consequences

- **Positive.** v1.0 keeps the stdlib-first transport promise: no third-party
  runtime dependency in core, and a smaller public surface to freeze at 1.0.
- **Positive.** The decision is reversible upward at low cost — the dispatch and
  session machinery is transport-agnostic, so a WebSocket path is additive in a
  v1.x point release.
- **Negative / accepted.** v1.0 has no bidirectional low-latency transport; an
  adopter who needs one must wait for the post-v1.0 item (or drive their own WS
  alongside the app). This is stated up front on the roadmap, so it is a known
  trade-off, not a surprise.
- **Follow-up.** When a real latency-bound adopter appears, revisit with a
  library choice (dependency justification per D24) and a design ADR covering
  SSE↔WS negotiation, reconnect/resume, and how CSRF and event serialization
  carry over the socket.
