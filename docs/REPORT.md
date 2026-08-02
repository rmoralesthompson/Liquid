# Liquid Documentation Analysis Report

**Date:** 2026-08-02
**Scope:** All documentation in `docs/` (now preserved under [docs/source/](source/)), plus `README.md` and `CLAUDE.md`.

---

## 1. What the docs describe

Liquid is a proposed **server-driven UI framework for Go** with an Angular-inspired developer experience. The core idea:

- **Components are plain Go structs** implementing `Selector()` and `Template()`, with an `NgOnInit()` lifecycle hook. State fields and event-handler methods live on the same struct — one file, one scope.
- **Templates use Angular-style sugar** (`.lsx` files or inline strings): `{{ Field }}` interpolation, `*goIf`, `*goFor`, `(click)="Method"`, `[input]` property bindings, `[hydroId]`, `*goTranslate`. A build-time **AOT preprocessor** transpiles this sugar into native `html/template` syntax, embedded into the binary via `go:embed`.
- **Interactivity without a client framework** ("Hydro-Streaming"): rendered HTML carries a component identity token (`data-hydro-id`). A ~10-line inline script posts `{hydroId, action}` to the server on user events; the server resolves the token to the live component instance, invokes the method via reflection, re-renders, and returns/streams an HTML patch (fetch POST or WebSocket push). Server-side `BehaviorSubject` observables can push re-renders proactively.
- **Framework services** mirroring Angular: reflection-based dependency injection, hierarchical routing with `:param` binding via struct tags, `CanActivate` route guards, HTTP interceptor chains, reactive form validation, scoped component CSS (`Assets()`), server-side i18n, auto-injected CSRF tokens, and a CLI scaffolder (`liquid generate component`).
- **An explicitly agent-first design**: AI agents generate `.lsx` + struct pairs, compile them through the AOT tool (which catches errors before runtime and feeds them back for self-healing), and deploy routes live. A **Blueprint Catalog** of pre-vetted templates constrains what agents can emit.

This is a coherent and genuinely interesting product thesis: **Phoenix LiveView / Blazor Server ergonomics, in Go, with Angular's mental model, optimized as a target for AI code generation.**

## 2. State of the documentation

The three source docs are **pasted AI-chat transcripts, not specifications**. Concretely:

- **Formatting is corrupted throughout.** Code blocks have collapsed whitespace (`type Componentinterface`, `import"sync"`, `elseif`), broken imports (`import "://github.com"`), and at least a dozen snippets that cannot compile as written (`t.Logfr`, `http.ParseRequestURIError`, regex submatches used without indices).
- **The docs contradict each other and themselves.** The router's `NewRouter` signature changes arity three times; the module is called `go-ng-framework`, `go-ng`, and Liquid interchangeably; templates are sometimes inline strings, sometimes `.lsx` files, with no defined resolution story.
- **Several subsystems are referenced but never defined:** `cli/compiler.go` (the actual AOT compiler — arguably the most important artifact), `core/i18n.go` (`GlobalTranslationEngine`), `core/persistence.go` (the Redis session layer), and the `RouteMiddleware`/`FunctionalMiddleware` types used in examples.
- **Performance/security claims are marketing, not measurements** ("under 400 microseconds", "millions of concurrent sessions", "completely safe from XSS").

I've moved these to [docs/source/](source/) as raw material and distilled them into clean docs (see §6).

## 3. What's genuinely good in the design

1. **The single-struct component scope.** State + template + handlers in one Go struct really is dramatically easier for an LLM (or a human) to reason about than React's hook/effect/fetch split. This is the strongest claim in the docs and it holds up.
2. **AOT compile-as-guardrail for agents.** Using the Go compiler + a template preprocessor as a validation gate — with structured errors fed back to the agent — is a real differentiator over "agent writes JSX and we hope."
3. **Blueprint Catalog.** Constraining agent output to parameterized, pre-vetted templates is the right safety posture for generated UI.
4. **`html/template` as the substrate.** Contextual auto-escaping for free is the correct XSS baseline.
5. **Honest-to-goodness Go concurrency at render time.** Fan-out data fetching with goroutines before render, with per-panel `context.WithTimeout` degradation, is a real advantage over single-threaded SSR runtimes.

## 4. Claims and designs that must be corrected before implementation

These are load-bearing problems, ranked by severity:

### 4.1 "Memory pointer hydration" is a security bug, not a feature
The docs' flagship feature embeds the component struct's **actual memory address** (`0x7f9a12c8b000`) into HTML and brands this "Binary State Mirror Hydration" that "no other framework can match." In reality:
- Leaking heap addresses to clients defeats ASLR and is an information-disclosure vulnerability.
- The address buys nothing: resolution already goes through a `map[string]*HydroState` registry — an **opaque random token** does the identical job with zero risk.
- It also breaks the Redis "resume any session on any node" claim, since a raw pointer is meaningless on another machine (or after a restart).

**Correction:** keep the registry pattern, key it by cryptographically random session-scoped tokens. This is exactly how LiveView does it, and it's fine.

### 4.2 Route components are singletons → data races and cross-user state leakage
Every routing example stores one component instance per route and mutates its fields on each request (`bindPathParams`, `NgOnInit`, handler methods). Two concurrent requests to `/users/:id` will interleave writes to the same struct. Worse, one user's data can render into another user's response.
**Correction:** routes must register component *types*; the router instantiates a fresh instance per request (`reflect.New`), as the child-component renderer already does.

### 4.3 The regex, line-by-line template parser is not viable
`ParseTemplate` processes one line at a time with regexes. Multi-line tags break it, one directive per line is silently assumed, nested structural directives are unsupported, and wrapping the whole line in `{{if}}...{{end}}` mangles anything else on that line. The child-component renderer has an **infinite loop**: its regex matches *any* empty element (`<div ...></div>` included); unregistered tags are `continue`d but never removed, so the `for { ... }` never terminates on ordinary HTML.
**Correction:** build the preprocessor on a real HTML parser (`golang.org/x/net/html`) operating on the node tree, and cache compiled templates once (several doc versions re-parse templates on every request).

### 4.4 Client-supplied reflection dispatch is an RCE-shaped hole
`HandleHydroEvent` calls `MethodByName(payload.Action)` with an attacker-controlled string — any exported method on the component (or anything reachable) becomes an endpoint.
**Correction:** explicit action registration (allowlist), generated at AOT time from `(click)` bindings found in templates.

### 4.5 "Zero JS" isn't zero, and the competitive claims are wrong
The docs repeatedly claim "0 bytes of JavaScript" while shipping inline `<script>` blocks (hydroEmit, WebSocket patcher). The honest pitch is a **small, fixed, framework-owned runtime (~1–2 KB)** — like LiveView's. The docs also claim LiveView/HTMX require full round-trips for every update while Liquid doesn't; Liquid's own design does the same server round-trip. Positioning should be "LiveView for Go, designed for agents," not "a paradigm no framework can match." Credibility matters if this is ever published.

### 4.6 Assorted implementation defects in the sketches
- `BehaviorSubject.Subscribe` from `NgOnInit` leaks subscribers (no unsubscribe; per-request components pile onto app-lifetime subjects).
- Hand-rolled WebSocket handshake never unmasks client frames, has no ping/pong or close handling (client→server frames are *required* to be masked per RFC 6455, so reads are garbage).
- CSRF: `fmt.Sscanf` with `%[^:]:%d:%s` can't round-trip the token format it generates; the session ID is hardcoded.
- DI is a flat singleton map by exact concrete type — no interface satisfaction, no scoping, despite "hierarchical" claims.
- Input bindings (`[userId]="Prop"`) silently drop on any type mismatch and only strings are supported for path params.

None of these are fatal — they're the expected quality of chat-generated sketches — but the implementation should treat the source docs as *inspiration*, not reference code.

## 5. What's missing entirely (needed for a real plan)

| Gap | Why it matters |
| --- | --- |
| The AOT compiler CLI itself | The centerpiece of both DX and the agent story; only its invocation is documented |
| Template/component file resolution (`.lsx` vs inline, discovery, embedding) | Determines project layout and the codegen design |
| Session/state lifecycle | When is a component born, how long does it live, sticky sessions vs serialized state, GC policy |
| Serializable state for Redis resumption | Live structs can't cross machines; needs explicit state marshaling design |
| Error handling & dev experience | Error pages, hot reload, template compile diagnostics |
| The actual go.mod / module path / repo layout | Docs give three different names |
| Any working code | The repo currently contains **zero Go files** — this is a greenfield build |

## 6. Documentation changes made

- **[docs/source/](source/)** — original three files preserved verbatim (renamed lowercase, marked as raw material).
- **[docs/architecture.md](architecture.md)** — new clean architecture spec distilled from the sources, with the §4 corrections applied (opaque tokens, per-request instantiation, allowlisted actions, honest positioning).
- **[docs/template-syntax.md](template-syntax.md)** — new consolidated `.lsx` directive reference (previously scattered across three files with inconsistencies).
- **[docs/design-decisions.md](design-decisions.md)** — open questions that need answers before/while implementing; intended as the agenda for the brainstorm.
- **[CLAUDE.md](../CLAUDE.md)** — rewritten from a raw chat transcript into an actual project-guidelines file.
- `README.md` — left as-is for now; it's the least-corrupted file and rewriting it makes more sense once the implementation plan settles naming and scope.

## 7. Recommended next steps

1. **Brainstorm session (next):** work through [design-decisions.md](design-decisions.md) — the big four are template compilation strategy (regex vs HTML-tree vs full codegen), component lifecycle/state model, the interactivity transport (fetch vs WebSocket vs both), and honest scope for v0.1.
2. **Define a minimal vertical slice** for v0.1: component model + AOT preprocessor + router + one interactive event round-trip. Defer DI, forms, i18n, Redis, guards.
3. **Stand up the Go module** (pick the name — repo is `liquid`, so `module github.com/<org>/liquid` seems right) with the layout already sketched in CLAUDE.md.
4. Build the AOT compiler on `golang.org/x/net/html` from day one — it is the foundation everything else (directives, CSRF injection, action allowlists, hydro IDs) hangs off.
