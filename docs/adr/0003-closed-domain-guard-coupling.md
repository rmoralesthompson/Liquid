# ADR-0003: Closed-domain payload enforcement requires a declared guard in v0.1

- **Status:** Accepted
- **Date:** 2026-08-07
- **Tracking issue:** [#85](https://github.com/rmoralesthompson/Liquid/issues/85)
- **Relates to:** [#79](https://github.com/rmoralesthompson/Liquid/issues/79) (D30 implementation), [#1](https://github.com/rmoralesthompson/Liquid/issues/1) (v0.1 spec); decision D30

## Context

D30 added the value axis of least privilege: a payload field typed as a Go
const-set (a named type backed by a typed `const` block) is a **closed domain**
the dispatch seam enforces — an out-of-set value is refused 400 before any
handler runs. The compiler enumerates that const-set via `go/types` and emits a
generated `PayloadDomains` method the seam reads at registration.

But in v0.1 a handler is `func(e liquid.Event)` — its payload is the untyped
`liquid.Event`, so `go/types` cannot see any payload struct from the handler
alone. The **only** place a per-action payload type is named to the compiler is
a boundary guard's parameter: `func (c *T) <Action>Guard(p <Payload>) bool`.
`ActionContracts` (`cmd/liquid/internal/compiler/contract.go`) therefore anchors
both axes — guard presence and closed-domain enumeration — on that guard
payload struct.

The consequence, surfaced by the post-merge review of #79: **a closed-domain
enum field on an action that declares no guard is never enumerated**, so
`PayloadDomains()` omits it and the seam does not enforce it. The typed enum
field *looks* enforced but isn't. The author's only signal is the generic
`LSX018` "unguarded action" warning — which does not say that writing the enum
field was, by itself, insufficient. Closed-domain least privilege is thus
silently coupled to declaring a guard: a sharp footgun.

### Options weighed

1. **Associate a payload type with an action independent of a guard** — e.g. a
   `<Name>Payload` marker type or a typed-payload handler signature — so closed
   domains enumerate without a guard. This is the real fix, but it invents a new
   public API convention and overlaps the deliberately-deferred typed-payload
   handler feature — a surface change immediately after tagging v0.1.0.
2. **A stronger, more specific diagnostic** than `LSX018` for the
   unguarded-closed-domain case. Infeasible on its own: without the guard the
   compiler cannot *see* the closed domain, so it cannot detect the case to warn
   about it specifically. Only helps once the payload type is discoverable —
   i.e. only after option 1.
3. **Document the coupling as intended v0.1 behavior** and revisit when typed
   payloads land — the payload struct becomes visible for free then, closing the
   gap without a bespoke mechanism.

## Decision

**Adopt option 3 for v0.1.** The guard↔closed-domain coupling is **intended
v0.1 behavior**: a closed-domain field is enforced at the seam only when its
action declares a guard.

- Record the coupling in the contract compiler's own rationale
  (`contract.go` package comment) so it reads as a decision, not an oversight,
  pointing here.
- **Sharpen the `LSX018` suggestion** to name the footgun directly: it now says
  a closed-domain (enum) payload field is enforced at the seam only when the
  action declares the guard. The compiler still cannot see the domain without a
  guard, so this is guidance, not detection.
- Document the coupling in [`docs/limitations.md`](../limitations.md) (D9
  honesty) and in the D30 note of
  [`docs/design-decisions.md`](../design-decisions.md).
- **Pin the behavior with a test** (`TestUnguardedClosedDomainIsNotEnforced`):
  an unguarded action with a closed-domain enum field generates no
  `PayloadDomains` and draws `LSX018`. The test states, in code, that this is a
  known gap — and is the tripwire to flip when option 1 lands.

The real fix (option 1) is deferred with the typed-payload handler feature: once
a handler can name its payload type, closed domains enumerate without a guard
and this ADR should be superseded.

## Consequences

- **Positive.** No public API change and no speculative mechanism right after the
  v0.1.0 tag. The footgun is now documented in three places an author actually
  reads: the build warning, the limitations doc, and the decision log.
- **Positive.** The pin test converts an implicit gap into a checked expectation;
  when typed payloads make the payload visible, the test fails loudly and forces
  a deliberate revisit rather than letting the behavior drift silently.
- **Negative / accepted.** Until typed payloads land, an author must declare a
  guard (even `return true`) to get closed-domain enforcement. This is stated up
  front, so it is a known trade-off, not a surprise. Writing a closed-domain
  field without a guard yields a warning, not enforcement.
- **Follow-up.** Revisit when typed-payload handlers are designed; superseding
  ADR should make the payload type discoverable without a guard and flip
  `TestUnguardedClosedDomainIsNotEnforced`.
