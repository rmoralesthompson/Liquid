# ADR-0005: DOM morphing for hydro patches

- **Status:** Accepted
- **Date:** 2026-08-07
- **Tracking issue:** [#106](https://github.com/rmoralesthompson/Liquid/issues/106)
- **Relates to:** decisions D14 (patch granularity), D21 (patch UX / focus), D20 (SSE reconnect); the v1.0 milestone (#2)

## Context

A hydro event (D14) re-renders the whole `[hydroId]` subtree on the server and
sends the HTML; the client applied it by replacing the boundary's children
wholesale (`root.replaceChildren(...)`). That swap preserved focus by element
`id` and the focused input's in-flight value (D21), but discarded everything
else that lives in DOM identity: **scroll position, in-progress CSS transitions,
media playback, `<details>` open state, and the identity of list items** (a
re-rendered list rebuilds every node, so a keyed reorder flickers and loses
per-item state).

DOM **morphing** fixes this: instead of replacing the subtree, walk the old and
new trees and mutate the old one in place to match the new — matching nodes by
`id` so identity, and everything hanging off it, survives.

### Options weighed

1. **Vendor a proven morph library.** Inline a small, battle-tested morph
   algorithm into the served runtime; replace the wholesale swap with an
   in-place morph. Correct on the many subtle edge cases (form state, SVG,
   custom elements, keyed reordering, `<option selected>`) that a library has
   already hardened.
2. **Hand-roll the morph.** Write our own tree-reconciliation in `runtime.js`.
   Zero third-party code, but reimplements a subtle algorithm and carries a
   higher bug surface to test and then freeze at v1.0.
3. **Server-side diffing (LiveView-style).** Retain the prior render on the
   server, diff, and send granular DOM operations. Smallest payloads, but a
   large architecture change (server-side prior-state retention, a diff
   algorithm, a new wire format) that departs from the "server renders HTML"
   model — out of proportion for v1.0.

## Decision

**Vendor [Idiomorph](https://github.com/bigskysoftware/idiomorph)** (option 1).

- Idiomorph is small (~10 KB minified), **0BSD-licensed** (the most permissive
  license — no attribution required, no dependency-management friction), and
  matches nodes by `id`, which is exactly the keyed-list behavior we want.
- It is vendored verbatim at `core/idiomorph.min.js`, embedded with `go:embed`,
  and served at `/liquid/idiomorph.js` — a sibling of the runtime script under
  the framework's `/liquid/` namespace. This is **client JS only**: no Go module
  dependency, and the framework already serves its own runtime script, so there
  is no new runtime fetch of third-party code.
- The document shell loads `idiomorph.js` **before** `runtime.js` (both
  deferred, so document order), so `Idiomorph` is defined when `applyPatch`
  runs.
- `applyPatch` (`core/runtime.js`) morphs the re-render into the live subtree
  with `Idiomorph.morph(root, next, { morphStyle: "innerHTML" })`, then still
  restores the focused input's in-flight value and selection — so the **D21
  guarantee is unchanged** while identity, scroll, media, and transition state
  are now preserved. Both event patches and SSE-pushed patches (D20, including a
  reconnect's full re-render) flow through the same `applyPatch`, so they all
  morph.

The framework's "prefer stdlib, justify every dependency" rule (D24) is a **Go**
rule; this is vendored, unmodified, permissively-licensed client JS, embedded in
the binary — no supply-chain surface at runtime and no Go dependency.

## Consequences

- **Positive.** Patches preserve scroll, focus, media, transitions, and list-item
  identity; keyed lists reorder rather than rebuild (give items stable `id`s).
  Correctness comes from a hardened library rather than a hand-rolled algorithm.
- **Positive.** No Go dependency and no new client fetch of third-party code
  (the library is served by the framework, like the runtime script).
- **Negative / accepted.** ~10 KB of vendored third-party JS ships in the binary
  and loads per page. Updating it is a manual re-vendor of the upstream dist.
- **Boundary limitation retained.** `morphStyle: "innerHTML"` morphs the
  boundary's children, so a patch still does not update attributes on the
  `[hydroId]` element itself (unchanged from D14).
- **Testing note.** The morph algorithm is the vendored library's; the Go tests
  cover that it is served, loaded before the runtime, and invoked by
  `applyPatch`. In-browser morph behavior is Idiomorph's own (well-tested)
  responsibility.
