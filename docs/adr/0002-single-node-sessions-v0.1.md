# ADR-0002: Ship v0.1.0 single-node; multi-node is not a v1.0 prerequisite

- **Status:** Accepted
- **Date:** 2026-08-06
- **Tracking issue:** [#89](https://github.com/rmoralesthompson/Liquid/issues/89)
- **Relates to:** [#88](https://github.com/rmoralesthompson/Liquid/issues/88) (limitations doc), [#91](https://github.com/rmoralesthompson/Liquid/issues/91) (cut the release); decisions D2, D4, D9, D15

## Context

D2 fixed Liquid's v0.1 session model as **single-node, in-memory**: interactive
component instances live in an in-memory registry keyed by an opaque random
token and evicted by idle-timeout GC. The same decision deliberately shaped the
registry entry type (`HydroState`) so that a serialization backend —
`Snapshot()`/`Restore()` over Redis or similar — *can* be added later, and
explicitly deferred building it. D4 lists Redis persistence as out-of-scope for
v0.1.

The practical consequence is that v0.1 cannot be horizontally scaled behind a
plain round-robin load balancer: a live session's state exists only in the
process that created it, so a request routed to a different instance loses that
session's interactivity. Running more than one instance requires **sticky
sessions** (session affinity). This is documented plainly in
[`docs/limitations.md`](../limitations.md) per the D9 honesty policy (#88).

Before tagging v0.1.0 we had to decide whether that constraint **blocks** a
1.0 — i.e. whether a Redis-backed, "resume anywhere" multi-node session store
becomes a prerequisite that must be built first — or whether v0.1.0 ships as-is
with the limit stated and the backend put on the roadmap.

### Inputs weighed

- **The extension seam already exists.** D2 designed `HydroState` for
  `Snapshot()`/`Restore()`. Deferring the backend is cheap precisely because the
  shape it plugs into is already in place; nothing about shipping single-node
  paints us into a corner.
- **No named multi-instance adopter.** There is no first adopter on record who
  requires horizontal scaling out of the box. Building the backend now would be
  speculative against an unproven need (consistent with the D8/D4 "add it when a
  real case appears" stance).
- **D9 honesty is satisfied.** Single-node is fine to ship *if clearly stated*,
  and it is — `limitations.md` lets a prospective adopter decide in one read
  whether the sticky-session constraint fits their deployment.
- **The milestone is a taggable v0.1.0.** The goal of this milestone is an
  honest, usable first tag, not a production multi-node platform. Delaying the
  tag to build a deferred capability inverts that goal.

## Decision

**Ship v0.1.0 single-node (Option A).** Horizontal scaling is **not** a v1.0
prerequisite.

- v0.1.0 tags with the in-memory, single-node session model as designed (D2).
- The single-node / sticky-session constraint stays documented plainly in
  `limitations.md` (#88), with no "resume anywhere" capability claimed until it
  ships (D2, D9).
- A **Redis-backed session-persistence backend** (`Snapshot()`/`Restore()` over
  the existing `HydroState` seam) is placed on the **v0.2 roadmap**
  ([`docs/roadmap.md`](../roadmap.md)), not built now.
- This unblocks [#91](https://github.com/rmoralesthompson/Liquid/issues/91)
  (cut the v0.1.0 release).

Whether multi-node is required for a *1.0* is deliberately left open until a
concrete multi-instance adopter appears; it is no longer a gate on *v0.1.0*.

## Consequences

- **Positive.** Fastest honest path to the first tag. Zero new scope. The
  decision is reversible upward at low cost because the `Snapshot()`/`Restore()`
  seam is already there — adding the backend later is additive, not a
  refactor.
- **Positive.** The framework's claims stay defensible: we ship what we can
  honestly stand behind and name the limit, matching how D9 treats performance
  claims.
- **Negative / accepted.** v0.1 adopters who need more than one instance must run
  sticky sessions until v0.2. This is stated up front, so it is a known
  trade-off at adoption time, not a surprise.
- **Follow-up.** v0.2 roadmap entry for Redis-backed sessions
  (`docs/roadmap.md`). Revisit the v1.0 question when a real multi-instance
  adopter materializes.
