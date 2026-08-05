# Liquid — Design Decisions

Decision log for the framework. Decisions D1–D29 are settled and binding unless explicitly revisited.

**D1–D9 accepted 2026-08-02; D10–D17 accepted 2026-08-02; D18–D24 accepted 2026-08-02; D25–D29 accepted 2026-08-05** (owner: Richard).

## Settled

### D1. Template compilation: preprocess → `html/template` (Option A)

`liquid build` parses `.lsx` files into an HTML node tree (`golang.org/x/net/html`), applies directive transforms, and emits standard `html/template` text embedded via `go:embed`, parsed once at startup. Templates are never regex/line-processed and never re-parsed per request.

- Additionally: a `vet` step cross-checks template field references against the paired struct via `go/types`, producing structured, agent-readable diagnostics without full codegen.
- The compiler backend must be swappable so full Go codegen (templ-style, Option B) can replace it later without changing `.lsx` semantics.

### D2. Component/state lifecycle: in-memory sessions, single-node v0.1

- Plain pages: fresh component instance per request (`reflect.New` from registered types). No shared mutable component state, ever.
- Interactive components: LiveView-style — the instance lives in an in-memory registry for the session, keyed by an opaque random token, evicted by idle-timeout GC.
- v0.1 is single-node. The registry entry type (`HydroState`) is designed so a serialization backend (`Snapshot()/Restore()` → Redis) can be added later; the "resume anywhere" story is deferred until then and not claimed before.

### D3. Interactivity transport: fetch + SSE (v0.1), WebSocket later

- Client → server events: `fetch` POST to `/hydro-event`.
- Server → client push (`BehaviorSubject` emissions): **SSE**.
- Both are stdlib-only — no WebSocket dependency and no hand-rolled framing (the source-doc WS sketch violates RFC 6455). WebSocket is a v0.2 upgrade if event latency demands it, via a maintained library.

### D4. v0.1 scope

**In:** component model + lifecycle, AOT compiler (`liquid build` + `vet`), router with `:param` binding and `CanActivate` guards, hydro event loop (fetch), SSE push, component nesting with `[input]` bindings (compiler-driven, not regex), CSRF engine (fixed token codec), minimal DI, CLI scaffolder (cheap once the compiler exists).

**Out (deferred):** forms/validation, i18n (`*goTranslate`), Redis persistence, interceptor chains (plain `http.Handler` middleware suffices), scoped assets, blueprint catalog, attribute-directive registry, benchmarks (no numeric claims until measured).

### D5. Naming

- Framework name: **Liquid**. All `go-ng` / `go-ng-framework` references are dead.
- Lifecycle hook: `OnInit()` — `Ng` prefixes dropped everywhere.
- Template extension: `.lsx` (`.liquid` clashes with Shopify).
- Selector convention: `app-` prefix stays as the default, not enforced.
- Module path: `github.com/rmoralesthompson/liquid` (lowercase per Go import-path convention; GitHub repo is `rmoralesthompson/Liquid`, resolves the same via git regardless of case).

### D6. Template location: `.lsx` files canonical

Resolved by filename convention (`user_card.go` ↔ `user_card.lsx`, same package dir); the compiler embeds the template and pairs it with the struct. Inline `Template()` strings remain as an escape hatch but skip build-time checks and are documented as discouraged.

### D7. Repo layout: monorepo

As in [CLAUDE.md](../CLAUDE.md): `cmd/liquid/` (compiler, scaffolder, dev server), `core/` (framework packages), `examples/` (runnable apps), `docs/`. Generated files live next to their source as `*_gen.go`.

### D8. DI: minimal reflection injector (option b)

Singleton providers registered by type, injected into component struct fields at instantiation; concrete-type and interface matching; hard error on unresolvable required deps. No hierarchy until a concrete need exists. Rationale: agents can add a field and have it "just work."

### D9. Honesty debt: settled as policy

Before anything is published: no memory-pointer branding, no "zero JS", no "impossible in any other framework", no unmeasured performance numbers. Enforced via the invariants list in [CLAUDE.md](../CLAUDE.md).

### D10. Action allowlist representation (O2a): compiler-generated from bindings

The AOT compiler scans `.lsx` files for `(click)="Method"` (and later `(submit)`, `(input)`) and emits the allowlist automatically — no manual opt-in step. Consistent with D1/D6/D8's compiler-driven approach: agents write a binding and the wiring "just works." A method not referenced by any template binding is never allowlisted; there is no struct-tag escape hatch for v0.1 (revisit only if a concrete need for pre-template allowlisting appears).

### D11. Event handler payload model (O2b): optional typed `liquid.Event` parameter

Handlers are either `func()` (no payload — plain clicks) or `func(e liquid.Event)` (needs data). `liquid.Event` exposes typed accessors (`e.String("field")`, `e.Int(...)`) and `e.Bind(&struct)` for forms. The compiler's `vet` step (D1) enforces exactly these two signatures at build time — a malformed handler is a build error, not the silent-type-mismatch runtime bug REPORT.md §4.6 flagged in the source docs' `[input]` bindings.

### D12. v0.1 event coverage (O2c): `(click)` + `(submit)`

Both bind through the same compiler-generated allowlist (D10) and `liquid.Event` payload model (D11); `(submit)` additionally exercises the `<form>` CSRF auto-injection already speced in template-syntax.md end-to-end in v0.1. `(input)`/`(change)` remain deferred. **O2 fully resolved.**

### D13. Compiler CLI surface & agent diagnostics format

- **Commands** (settled by naming convention already consistent across architecture.md/template-syntax.md/README): `liquid build` (AOT compile, default text output), `liquid vet` (type/field-reference check without full build), `liquid generate component <name>` (scaffolder, cheap once the compiler exists), `liquid dev` (file-watch + rebuild; reload mechanism itself is O5).
- **Diagnostics**: structured JSON array via `--json` (text remains the default for terminal/human use). Each element: `{file, line, col, severity, code, message, suggestion}`. `severity` is `error`/`warning`; `code` is a stable machine-matchable identifier (e.g. `LSX001`); `suggestion` is a short fix hint when the compiler can produce one. This is the literal contract an agent parses to self-repair — field names are the API, more important than richness.

### D14. Patch granularity (O6): whole `[hydroId]` subtree swap; nested components inherit the parent's session

On a hydro event, the server re-renders the entire component subtree rooted at its `[hydroId]` element and returns it as one HTML blob; the runtime script swaps `innerHTML` at that boundary. Matches LiveView's own v1 approach — no diffing engine in v0.1 (per D9, no "faster" claim until it's measured against this baseline). Only a component that itself declares `[hydroId]` gets a session-registry entry (D2); a plain nested child re-renders for free as part of its interactive ancestor's subtree swap — no independent session/patch root unless it declares its own `[hydroId]`.

**D14.1 — deferred rendering (`*liquidDefer`, #26).** A deferred child occurrence ships a fallback slot and loads in a session-registry-owned background goroutine, its content pushed when ready (template-syntax.md). Design points settled during implementation:

- **One token, three roles.** `liquidDefer` mints a single token that is the slot's `data-hydro-id`, the child's `HydroID`, and its registry key — so the completed child is an ordinary live hydro instance (events dispatch, subscriptions push), not a special case.
- **Deferred work = the child's `OnInit`,** run on a context *detached* from the request (the request's own context dies when the shell ships) and owned by the registry entry — cancelled on eviction/expiry like a subscription pump (D20), so a page cannot spawn unbounded background work and an in-flight load drops cleanly.
- **Completion transport = a `swap` SSE frame.** `innerHTML` swap (D14) would drop the child's own root element, so completion instead replaces the fallback slot wholesale (`outerHTML`); *subsequent* subscription updates are ordinary focus-preserving patches at the now-present boundary.
- **Dispatch safety (D20.1).** The entry is registered synchronously (counts against the per-session cap) but gated non-ready; `/hydro-event` misses the token until the load publishes under the dispatch mutex, so the background `OnInit` never races a dispatched handler.
- **No performance claim** for the "ships at shell speed" pitch without a benchmark (D9) — the feature is described by behavior only.

### D15. Session & CSRF plumbing (O3): one session cookie, hydro tokens nested under it

*Amended 2026-08-03 (issues [#45](https://github.com/rmoralesthompson/Liquid/issues/45), [#46](https://github.com/rmoralesthompson/Liquid/issues/46), [#47](https://github.com/rmoralesthompson/Liquid/issues/47), closing THREAT-MODEL.md's three open decisions): signature-only token encoding, patch-envelope re-mint, warn-once `Secure` posture.*

- **Cookie**: `liquid_session`, `HttpOnly` + `Secure` + `SameSite=Lax`, value is a cryptographically random opaque session ID — distinct from any individual component's `[hydroId]` token. `Secure` is unconditional — no override knob. Because a plain-HTTP **non-localhost** deployment silently loses the cookie (and with it all interactivity), the session-mint path emits a once-per-process `slog` warning when serving plain HTTP off a non-localhost `Host` (#47); the message notes it can be ignored when TLS terminates upstream.
- **Session store**: in-memory map (same idle-timeout-GC mechanism as D2), `sessionID → {hydroId → HydroState}` — one browser session can host many interactive components, matching the O7 example app's nested-card requirement.
- **CSRF**: HMAC-SHA256(server secret, `sessionID + expiry`), encoded **signature-only** as `expiry:signature` — the session ID is recovered server-side from the request's cookie, never embedded in the token (#45; the original `sessionID:expiry:signature` format put the HttpOnly cookie's value in DOM reach via the meta tag and hidden inputs). Replaces the source docs' broken `fmt.Sscanf` round-trip (REPORT.md §4.6). Auto-injected into `<form>` (D6/template-syntax.md) and hydro-event payloads (D11); validated (recompute HMAC from the cookie's session ID, check expiry) before any action dispatch (D10's allowlist check happens after CSRF passes) — the refusal order guarantees the cookie is present before CSRF validation runs. **Re-minted on every patch envelope** (#46): a full-page render mints the token, and each `/hydro-event` patch answer re-mints it against the current clock so its expiry tracks the session's *sliding* idle deadline rather than the original render's fixed horizon; the runtime restamps the fresh token into the `liquid-csrf` meta tag and the swapped subtree's hidden `csrf_token` inputs, so a continuously used page never earns a spurious 403. A redirect answer navigates away and carries no token.
- **Rotation**: token expiry tracks the session's idle-timeout window (D2); the token is regenerated on each full-page render **and re-minted in every patch envelope** (#46), with the runtime refreshing the `liquid-csrf` meta tag and hidden inputs — so token lifetime follows the session's sliding `lastActive + idle-window` deadline instead of the render-time stamp. No separate manual rotation policy in v0.1 — there's no auth/privilege-escalation flow yet to rotate against; revisit when auth lands.

### D16. Dev experience (O5): SSE-triggered full refresh + error overlay

`liquid dev` file-watches, rebuilds on change, and reuses the existing SSE transport (D3) — no new mechanism. Successful rebuild pushes a `reload` event; client runs `location.reload()`. A failed build pushes the D13 JSON diagnostic instead of a reload event, rendered as a dev-only in-browser overlay (thin UI layer over data D13 already produces). State-preserving patch-over-SSE hot reload is out of scope for v0.1 (no diffing/reconciliation engine — matches D14's "no diffing engine in v0.1" stance).

### D17. v0.1 example app (O7): dashboard with counter, SSE metric, nested card, guarded route, and a form

One vertical-slice app exercising every settled decision end-to-end: `(click)` events (D10/D11) via a counter, SSE server push (D3) via a live metric, a nested child component with `[input]` bindings, a `CanActivate`-guarded route (D4), and a small form (e.g. "rename this dashboard") exercising `(submit)` + CSRF (D12/D15). Built and iterated on via `liquid dev` (D16), doubling as the first real workout of the compiler diagnostics loop (D13) for agent self-repair.

### D18. Request context & error contract (O8): `OnInit(ctx liquid.Ctx) error`

The lifecycle hook is `OnInit(ctx liquid.Ctx) error`, where `liquid.Ctx` embeds `context.Context` (cancellation/deadlines for the goroutine fan-out story) and exposes request accessors (params, query, headers, session). An error return maps to a framework error page (dev: full diagnostic; prod: clean 500); the router wraps rendering in panic recovery. Action handlers reach the same `liquid.Ctx` via `liquid.Event`. This supersedes the zero-arg `OnInit()` shown in earlier spec drafts and brings components in line with CLAUDE.md's ctx-first standard.

### D19. Navigation & redirects (O9): patch-or-redirect envelope

Hydro responses are a small envelope: patch HTML *or* `{redirect: "/path"}`, honored by the runtime script. `CanActivate` gains a redirect result variant (deny → 403 vs redirect → login flow). Full client-side URL patching/history management stays deferred.

### D20. Session concurrency & abuse hardening (O10)

Requirements (already binding via CLAUDE.md invariants) plus settled mechanisms:

1. **Serialized dispatch:** per-session mutex for v0.1 (mailbox/actor model revisited if contention profiles demand it).
2. **Bounded registry:** configurable caps — max components per `liquid_session`, max total sessions — with LRU eviction and sane defaults.
3. **Request limits:** body-size limit on `/hydro-event`, enforced before JSON decode.
4. **SSE reconnect:** triggers a full re-render of current state; no missed-patch replay in v0.1.

### D21. Patch UX & accessibility (O11): focus preservation, a11y checklist

The runtime preserves focus by element `id` across `[hydroId]` swaps and never overwrites the actively-focused input's value; remaining limitations are documented. The D17 example app gets an a11y checklist; `aria-live` guidance for push-updated regions lives in [template-syntax.md](template-syntax.md). Full morphdom-style DOM merging is the v0.2+ answer.

### D22. Document head & static assets (O12)

Optional `Head() liquid.Head` component interface (title + meta list) in v0.1; static files served via stdlib `http.FileServer` under `/static/` with basic cache headers. Fingerprinting/bundling deferred.

### D23. Component test harness (O13): `liquidtest` in v0.1

Minimal harness — render a component to HTML, query it, fire an allowlisted action with a payload, assert the patch/envelope. It is both the framework's own test vehicle and the agent's verification step after `vet` passes ("it compiles" is not "it works"). API designed alongside the hydro loop since it exercises the same internals.

### D24. Operational & project standards (O14)

- **Logging:** `log/slog` throughout core; no `fmt.Println`. Pluggable handler.
- **Shutdown:** graceful — stop accepting events, close SSE streams with a `reload` hint, halt GC loops.
- **CSP:** runtime script served as a static file (no inline JS); dev-only error overlay may relax this in dev mode only. A recommended CSP header ships in the docs.
- **Go version:** minimum Go 1.23.
- **Versioning:** semver; `v0.x` = no compatibility promises, stated in README when it's rewritten.
- **License:** **Apache-2.0** (chosen 2026-08-02; patent grant preferred over MIT for a framework meant for ecosystem adoption). `LICENSE` file at repo root, copyright Richard Morales Thompson.

### D25. Derived reactive state: stream combinators over `BehaviorSubject[T]`

*Accepted 2026-08-05, with D26–D29, as the agent-first / reactive-views batch. Each extends the framework for agent-generated dashboards while staying inside v0.1's settled architecture — no wire-format or transport change. All are accepted but unbuilt; see [HANDOFF.md](HANDOFF.md) §4 for build order.*


**Problem.** `BehaviorSubject[T]` (architecture.md §State & reactivity) gives a single observable value with `Next`/`Subscribe`/`Value`, and SSE (D3) pushes its emissions. That is enough for *one* live value but not for a dashboard, which is overwhelmingly **derived** state: totals, quantiles, and filtered rollups that recompute when an upstream source *or* a user-controlled filter changes. Today each derived tile must hand-roll a goroutine that subscribes to N upstreams, recomputes on every `Next`, pushes into its own subject, and **unsubscribes on session GC**. That last step is the leak hazard architecture.md §90 and the CLAUDE.md bounded-registry invariant explicitly warn about — and it is the most repetitive, highest-risk code in any generated dashboard. Making the *generator* (agent or human) responsible for subscription lifecycle per tile is the wrong boundary.

**Decision.** Add a small combinator layer over the existing subject whose subscription lifecycle is owned by the framework, registered against the session's unsubscribe-on-GC hook (D2/D20) so generated code never manages it:

```go
total    := liquid.Map(orders, sum)                          // 1→1 projection
p95      := liquid.CombineLatest(latencies, window, p95Of)   // recompute when EITHER input changes
metrics  := liquid.Interval(ctx, 5*time.Second, fetch)       // poll source as a stream
smoothed := liquid.Throttle(metrics, 250*time.Millisecond)   // backpressure for chatty sources
```

- **`CombineLatest`** is load-bearing: it is what lets one filter control (date range, environment selector) fan out to every dependent tile without the author wiring N→M subscriptions by hand.
- **`Interval`/poll + `Throttle`** cover the other dashboard reality — most tiles are periodic pulls, and not every tick should become an SSE patch.
- Derived subjects are read/subscribed exactly like a `BehaviorSubject[T]`, so they compose with server push (D3), patch swaps (D14), and DI-registered app-lifetime subjects (architecture.md §99) with **no wire-format or transport change**.

**Scope & non-goals.**
- v0.1 combinator set is intentionally minimal (`Map`, `CombineLatest`, `Interval`, `Throttle`); a broader operator library is deferred until concrete need, per the D8/D4 "add when a real case appears" stance.
- A template-level `@poll(5s)` / data-source directive in `.lsx` is explicitly **out of scope here** but the combinators are designed so it can layer on as thin sugar later (consistent with D1's compiler-driven approach) — this decision does not foreclose it.
- **Not** a diffing/reconciliation engine — derived emissions still trigger a whole-`[hydroId]` subtree swap (D14). Combinators change *how derived values are computed*, not *how patches are applied*.
- Charts/visualization primitives are **not** in scope and are a deliberate non-goal (a userland/component-library concern, not framework-shaped — see the frontier note below).

**Rationale.** Composes with what exists (same subject, same SSE, same GC hook) at low architectural cost, and moves the leak-prone subscription lifecycle behind the framework boundary — the single biggest reliability win for agent-generated reactive views specifically.

### D26. Machine-readable component/action manifest: `liquid manifest --json`

**Problem.** The AOT compiler already resolves, for every component, its paired struct fields (D1 `vet` via `go/types`), its `[input]` bindings (D4 nesting), and its compile-time action allowlist (D10). An agent composing a view has no way to read that ground truth except by parsing source — the one thing the compiler exists to avoid re-doing per request (D1). D13 gives an agent a contract for *fixing what it broke*; there is no equivalent contract for *discovering what already exists to compose*.

**Decision.** Add `liquid manifest` emitting a stable JSON graph of the compiled app: for each component, its selector, source file, struct fields (name/type/`[input]`-ness), allowlisted actions with their D11 signatures (`func()` vs `func(liquid.Event)`), declared `[hydroId]` roots, and `Head()`/route associations where known. Text output is the default (consistent with D13); `--json` is the agent contract.

- **The field names are the API** — same stance as D13. Richness is secondary to a stable, machine-matchable shape.
- Derived from data the compiler already holds at `build`/`vet` time — no new analysis pass, no runtime cost.
- Complements D13: D13 = "how do I repair a broken build," D26 = "what is here to build with." Together they close the agent's read/write loop over the component graph.

**Scope & non-goals.** v0.1 manifest describes the *static* compiled graph only — not live session state, not runtime instances (D2). A stable `code`/version field on the envelope so agents can match against schema changes; no backward-compat promise while `v0.x` (D24). Not a plugin/extension registry (attribute-directive registry stays deferred per D4).

**Rationale.** This is the sharpest expression of the agent-first thesis in CLAUDE.md: the compiler already knows the whole component graph, so handing it to a non-human author as data (not source to re-parse) is nearly free and is something none of the human-authored-first frameworks (LiveView/Hotwire/Livewire/templ) were built to provide.

### D27. Render snapshot assertions in `liquidtest` (D23)

**Problem.** D23's harness can render a component, fire an allowlisted action, and assert on the resulting patch/envelope — but assertions are hand-written. A non-human author cannot eyeball a rendered `[hydroId]` subtree for visual regressions, which is exactly the failure mode for dashboards (D17) and derived-state tiles (D25). Server-driven UI regressions are overwhelmingly "the rendered HTML changed," and that class is mechanically checkable.

**Decision.** Extend `liquidtest` (D23) with golden-snapshot assertions: render a component (or fire an action and capture the patch), compare against a committed snapshot file, fail on diff, and regenerate under an explicit `-update` flag. Snapshots key on the `[hydroId]` subtree boundary (D14) so a patch and a full render assert through the same path.

- Reuses D23's existing render/query/fire internals — additive, not a new harness.
- The failure diff is emitted in the D13 structured shape where practical, so an agent consumes a snapshot mismatch the same way it consumes a build diagnostic and can self-verify → self-repair.
- Depends on deterministic rendering (D28) to avoid false diffs from timestamps/ordering/random IDs.

**Scope & non-goals.** Text/HTML-subtree snapshots only — **not** screenshot/pixel diffing (no browser dependency in v0.1, consistent with D3's stdlib-only stance). No implicit auto-update in CI; regeneration is always an explicit local flag so a drifting snapshot is never silently blessed.

**Rationale.** Turns "it compiles" (`vet`) → "it renders the same as before" into a check an agent runs autonomously, which is the verification step D23 already frames as the harness's second purpose. Directly serves D25: derived tiles are precisely what you snapshot.

### D28. Deterministic render mode for reproducible output

**Problem.** Snapshot assertions (D27), agent self-verification (D23), and diffable generated output all break when a render embeds non-deterministic values — wall-clock timestamps, map-iteration ordering, and the cryptographically random `[hydroId]`/CSRF tokens (D15). Those tokens are *correctly* random in production (a framework invariant — never derived, never a memory address), so determinism must be an opt-in test/CI concern, never the production path.

**Decision.** A test/CI-only deterministic mode (build tag or `liquid.Ctx` flag, resolved alongside the `liquiddev` surface) that: seeds a fixed token source for `[hydroId]`/CSRF generation, pins any framework-surfaced clock to an injectable value, and enforces stable ordering for framework-controlled iteration. Off by default; production always uses the CSPRNG path (D15) and real clock.

- The token source and clock become injectable seams (fits D8 DI: swap the provider in tests), not global mutable state.
- Application-level non-determinism (a component reading `time.Now()` itself) is the author's responsibility; the framework guarantees only that *its own* emitted values are pinnable.

**Scope & non-goals.** Does **not** weaken production security — the invariant that hydro/CSRF tokens are opaque CSPRNG output (D15, CLAUDE.md) is unchanged; deterministic mode is unreachable in a normal build. Not a general "record/replay" facility.

**Rationale.** Small and load-bearing: it is the precondition that makes D27 and reliable agent verification possible at all. Without it, snapshot diffs are noise.

### D29. `vet`-level reactivity leak check (depends on D25)

**Problem.** The bounded-registry / no-leaked-subscription invariant (CLAUDE.md; architecture.md §90) is today only a runtime hazard — a subscription without a session-bound unsubscribe leaks silently and fails under load, invisible to tests. This is exactly the class of bug a non-human author is prone to introduce, and it surfaces at the worst possible time.

**Decision.** Extend the `vet` pass (D1) to statically flag a `Subscribe`/combinator subscription (D25) that is not tied to a session lifecycle hook, emitting it through the D13 diagnostic contract (`{file, line, col, severity, code, message, suggestion}`) with a stable `code`. A subscription created outside an interactive session's managed scope, or without registering an unsubscribe, is a build **warning** (escalating to error where the compiler can prove the leak).

- Piggybacks on `go/types` analysis `vet` already runs (D1) — no new toolchain.
- Turns a runtime invariant into a build-time signal an agent is *told about* and can self-repair (D13), rather than one it discovers via a production incident.
- Naturally scoped by D25: once derived subscriptions go through framework combinators, "is this subscription lifecycle-managed?" becomes a decidable static question.

**Scope & non-goals.** Depends on D25 landing first (nothing to check until combinators exist). Best-effort static analysis — it flags the detectable patterns (bare `Subscribe` without a managed owner), not a soundness proof; false negatives are acceptable, false positives should be rare and suppressible. Does not replace the runtime bounded-registry caps (D20) — defense in depth, not a substitute.

**Rationale.** "The framework catches the class of bug the agent is prone to" is the agent-first moat: human-authored-first frameworks never built this because a careful human is assumed. Converting the D25 leak hazard into a D13-delivered diagnostic is the guardrail that makes derived reactive state safe to generate.

**Implementation note (#61).** Points settled while building the check:

- **New code `LSX017`.** Emitted through the existing D13 contract (`cmd/liquid/internal/compiler/vet_leak.go`), positioned at the `Subscribe` selector in the paired Go source. `liquid build` and `liquid vet` run it once per component package; `liquid lsp` surfaces it at the template top like any cross-file finding (no second code path — the detector is a `Facts` method on the shared analysis surface).
- **Detected pattern.** A call to the `Subscribe` method declared on a Liquid core observable (`BehaviorSubject`, `Derived`, or the `Observable` interface, resolved by `go/types` so an unrelated same-named method is not flagged) in the package's own source. The framework-owned `liquid.Observe` path is never a `Subscribe` call, so it is never flagged. Core's own internal `Subscribe` calls are invisible — `vet` only scans application packages, never `core`.
- **Warning vs. error.** A captured cancel is a **warning** (it may be released somewhere the static check can't follow); a discarded cancel — a bare statement, a `go`/`defer` call, or an assignment entirely to blanks — is an **error** (the cancel is unreachable, so the leak is provable). Exit contract: `liquid build`/`vet` fail on an **error**, but a **warning** is reported (text, `--json`, LSP) without failing the invocation — LSX017 is the first diagnostic to exercise the D13 warning severity, so the CLI now keys its exit code on error-severity findings rather than any finding.
- **Suppression.** A `//liquid:allow-subscribe` comment on the call's line or the line above silences it, for the rare deliberate hand-managed subscription.
- **Best-effort.** A directory that will not load (a bare template with no module) is skipped rather than erroring — false negatives are acceptable (D29 scope), and the template gating already reports the real problem.

---

**Settled decisions D1–D29 are binding. See [HANDOFF.md](HANDOFF.md) for current state and build order.**
