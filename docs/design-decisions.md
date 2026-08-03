# Liquid — Design Decisions

Decision log for the framework. All decisions (D1–D24) are settled and binding unless explicitly revisited.

**D1–D9 accepted 2026-08-02; D10–D17 accepted 2026-08-02; D18–D24 accepted 2026-08-02** (owner: Richard).

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
- **CSRF**: HMAC-SHA256(server secret, `sessionID + expiry`), encoded **signature-only** as `expiry:signature` — the session ID is recovered server-side from the request's cookie, never embedded in the token (#45; the original `sessionID:expiry:signature` format put the HttpOnly cookie's value in DOM reach via the meta tag and hidden inputs). Replaces the source docs' broken `fmt.Sscanf` round-trip (REPORT.md §4.6). Auto-injected into `<form>` (D6/template-syntax.md) and hydro-event payloads (D11); validated (recompute HMAC from the cookie's session ID, check expiry) before any action dispatch (D10's allowlist check happens after CSRF passes) — the refusal order guarantees the cookie is present before CSRF validation runs.
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

---

**All design decisions are settled (D1–D24). See [HANDOFF.md](HANDOFF.md) for current state and build order.**
