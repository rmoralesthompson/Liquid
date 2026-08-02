# Liquid — Brainstorm Handoff

**Date:** 2026-08-02 · **For:** a fresh session continuing the design brainstorm toward an implementation plan.
**Repo:** `/Users/richard.morales.thompson/Code/liquid` — **greenfield, zero Go files.** Everything so far is documentation.

## 1. What Liquid is (30 seconds)

A server-driven UI framework for Go: **Phoenix LiveView's runtime model + Angular's mental model, designed as a target for AI code generation.**

- Components = single Go structs (`Selector()`, paired `.lsx` template, `OnInit()` hook, exported fields = state, allowlisted methods = event handlers).
- `.lsx` = HTML + Angular-style sugar (`{{ Field }}`, `*goIf`, `*goFor`, `(click)="Method"`, `[input]` bindings), AOT-compiled to `html/template` and embedded in the binary.
- Interactivity: rendered HTML carries an opaque session token (`data-hydro-id`); a tiny fixed runtime script posts events to the server, which invokes the handler on the live instance and returns an HTML patch. Server push via SSE.
- Agent-first: `liquid build`/`vet` act as a guardrail — structured diagnostics feed back to agents for self-repair; a blueprint catalog (later) constrains generated markup.

## 2. Where things stand

**All 24 design decisions are settled — the brainstorm phase is closed. No open choices remain.** Work completed, in order, across two sessions:

1. **Analyzed the original design material** — three AI-chat transcripts, now preserved in `docs/source/`. Verdict: strong product thesis, but the transcripts are *inspiration only* — code there doesn't compile and contains real security/correctness bugs. Full findings: [REPORT.md](REPORT.md) (esp. §4, the defect list).
2. **Wrote clean specs:** [architecture.md](architecture.md) (authoritative architecture + v0.1 subsystem table), [template-syntax.md](template-syntax.md) (directive reference, resolves contradictions between sources).
3. **D1–D9 accepted** (2026-08-02, by Richard): compilation strategy, lifecycle, transport, v0.1 scope, naming, template location, repo layout, DI, honesty policy.
4. **D10–D17 accepted** (2026-08-02, by Richard, continuing this file's suggested order): action allowlist, event payload model, v0.1 event coverage, compiler CLI + diagnostics format, patch granularity, session/CSRF plumbing, dev-reload story, and the v0.1 example app — closing out every item that was open. Full text of all 17 in [design-decisions.md](design-decisions.md) §Settled.
5. **Rewrote [CLAUDE.md](../CLAUDE.md)** with build commands, Go standards, and binding framework invariants.
6. **Adopted `mattpocock/skills`** (registered as a plugin in `.claude/settings.json`) and ran its `setup-matt-pocock-skills` process: issue tracker is GitHub (`docs/agents/issue-tracker.md`, remote now set to `github.com/rmoralesthompson/Liquid`), default triage labels (`docs/agents/triage-labels.md`), single-context domain docs (`docs/agents/domain.md`). `CLAUDE.md` has an `## Agent skills` block pointing at all three.
7. **Re-aligned the spec docs with D10–D17** (post-decision consistency pass): [architecture.md](architecture.md) now reflects SSE-not-WebSocket transport, the `sessionID → {hydroId → HydroState}` registry keying (D15), `[hydroId]`-boundary innerHTML swaps (D14), and the settled subsystem table; [template-syntax.md](template-syntax.md) now documents the two allowed handler signatures (D11) and `(click)` + `(submit)` v0.1 coverage (D12). The specs and the decision log agree — a fresh context can trust any of them.
8. **Gap review** ("what's missing before we build?") surfaced seven gaps (O8–O14): request-context/error contract for `OnInit`, redirects/navigation, session concurrency + abuse hardening (its requirements became two CLAUDE.md invariants: serialized per-session dispatch, bounded registry), patch focus/a11y semantics, head + static assets, a `liquidtest` harness, and operational standards incl. the missing LICENSE file.
9. **Round-2 recommendations accepted as D18–D24** (2026-08-02, by Richard) and the specs re-aligned again: [architecture.md](architecture.md) now shows `OnInit(ctx liquid.Ctx) error`, `HeadProvider`, the patch-or-redirect envelope, hardening rules, and two new subsystem rows (`liquidtest`, head/static); [template-syntax.md](template-syntax.md) gained an accessibility-notes section and the corrected lifecycle signature; [CLAUDE.md](../CLAUDE.md) gained the `slog` and Go ≥1.23 standards.

## 3. Settled decisions (do not re-litigate; full text in design-decisions.md)

| # | Decision |
| --- | --- |
| D1 | Compiler: `.lsx` → HTML node tree (`x/net/html`) → `html/template` text → `go:embed`, parsed once at boot. Plus a `go/types`-based `vet` cross-checking template refs against structs. Backend swappable for future codegen. |
| D2 | Lifecycle: per-request instances for pages; LiveView-style in-memory session registry (opaque random tokens, idle GC) for interactive components. Single-node v0.1; `HydroState` designed for later `Snapshot()/Restore()` serialization. |
| D3 | Transport: fetch POST for events, **SSE** for server push. Stdlib-only in v0.1; WebSocket deferred to v0.2 via a maintained lib. |
| D4 | v0.1 scope — in: component model, AOT compiler, router (+params, guards), hydro events, SSE push, nesting/inputs, CSRF, minimal DI, scaffolder. Out: forms, i18n, Redis, interceptors, scoped assets, blueprints, benchmarks. |
| D5 | Naming: **Liquid**; `OnInit()` (no `Ng` prefixes); `.lsx`; `app-` selector prefix by convention; module path `github.com/rmoralesthompson/liquid`. |
| D6 | `.lsx` files canonical, paired by filename convention (`user_card.go` ↔ `user_card.lsx`); inline `Template()` is a discouraged escape hatch without build checks. |
| D7 | Monorepo: `cmd/liquid/`, `core/`, `examples/`, `docs/`; generated files as `*_gen.go` beside source. |
| D8 | DI: minimal reflection injector — singletons by type into component fields, interface matching, hard error on missing required deps. |
| D9 | Honesty policy: no pointer branding, no "zero JS", no unmeasured perf claims. |
| D10 | Action allowlist: compiler-generated from `(click)`/`(submit)` bindings found in templates, no struct-tag opt-in. |
| D11 | Event payload: handlers are `func()` or `func(e liquid.Event)`; `liquid.Event` has typed accessors + `Bind(&struct)`; `vet` enforces the signature at build time. |
| D12 | v0.1 events: `(click)` + `(submit)`. `(input)`/`(change)` deferred. |
| D13 | CLI: `liquid build`/`vet`/`generate component`/`dev`. Diagnostics: JSON array of `{file, line, col, severity, code, message, suggestion}` via `--json`, text by default. |
| D14 | Patch granularity: whole `[hydroId]` subtree swap on event; only components declaring their own `[hydroId]` get a session entry, nested plain children ride along for free. |
| D15 | Session: one `liquid_session` cookie per browser session holding `{hydroId → HydroState}`; CSRF is HMAC-signed, bound to session ID, regenerated per page render. |
| D16 | Dev reload: SSE-pushed `reload` event on success; JSON diagnostic → in-browser overlay on failure. No state-preserving hot reload in v0.1. |
| D17 | v0.1 example app: dashboard with counter (click), live metric (SSE), nested card (`[input]`), guarded route, and a form (submit + CSRF) — exercises every decision above end-to-end. |
| D18 | Lifecycle: `OnInit(ctx liquid.Ctx) error` — `liquid.Ctx` embeds `context.Context` + request accessors; error → framework error page; panics recovered. Handlers reach the same Ctx via `liquid.Event`. |
| D19 | Hydro responses are patch-or-`{redirect}` envelopes; `CanActivate` gains a redirect variant. URL/history patching deferred. |
| D20 | Hardening: per-session mutex dispatch; bounded registry (per-session + global caps, LRU); body-size limit on `/hydro-event`; SSE reconnect → full re-render. |
| D21 | Patches preserve focus by `id` and never overwrite the focused input's value; `aria-live` guidance in template-syntax.md; a11y checklist on the example app; DOM-morphing is v0.2+. |
| D22 | Optional `Head() liquid.Head` interface (title/meta); static files via stdlib `http.FileServer` under `/static/`. |
| D23 | `liquidtest` harness in v0.1: render → query → fire action → assert patch/envelope. |
| D24 | Ops standards: `slog`, graceful shutdown, CSP-compatible runtime script, Go ≥1.23, semver `v0.x`. License: **Apache-2.0** (`LICENSE` at repo root). |

**Non-negotiable invariants** (from REPORT §4 + the gap review, enforced in CLAUDE.md): per-request instantiation (no shared mutable components), opaque random tokens (never memory addresses), compile-time action allowlist (never `MethodByName` on client input), all interpolation through `html/template` escaping, HTML-tree parsing (never line regexes), serialized per-session event dispatch, bounded session registry.

## 4. What the next session should do

1. Break the decisions + architecture.md into a build sequence. REPORT.md §7 already suggests an order: **the AOT compiler first** (D1/D13 — everything else hangs off it, and it's the agent guardrail), then the component model + router (D2/D4/D5–D8, D18/D22), then the hydro event loop (D10–D12/D14/D15, D19–D21) with `liquidtest` alongside (D23), then `liquid dev` (D16), then the D17 example app as the integration test.
2. Consider using `/to-spec` (from the now-installed `mattpocock-skills` plugin) to turn this decision log into a formal spec, then `/wayfinder` or `/to-tickets` to break the vertical slice into tracer-bullet tickets against the GitHub issue tracker set up in `docs/agents/issue-tracker.md`.
3. Once tickets exist, `/tdd` + `/implement` + `/code-review` per ticket — the source docs' defect list (REPORT.md §4.6) is exactly the class of bug a red-green-refactor loop with dual-axis review should catch before it lands again.
4. **Nothing is committed to git yet** (`git status` shows everything untracked, no commits on `main`). Worth an initial commit once the docs/config state is confirmed good, before implementation work starts generating a lot more untracked files.

## 5. Reading order for a fresh context

1. This file.
2. [design-decisions.md](design-decisions.md) — the full settled decision log.
3. [architecture.md](architecture.md) — the spec, incl. the v0.1 subsystem table.
4. [template-syntax.md](template-syntax.md) — the `.lsx` directive reference.
5. [REPORT.md](REPORT.md) §4–5 — only if you need the "why" behind an invariant.
6. `docs/source/` — avoid unless mining for a forgotten feature idea; treat all code there as untrusted.
