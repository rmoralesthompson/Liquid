# Liquid Architecture

> Status: **design spec** — distilled from the exploratory material in [source/](source/), with known defects corrected (see [REPORT.md](REPORT.md) §4). No code exists yet; this document is the reference for implementation.

Liquid is a server-driven UI framework for Go. Components live and execute entirely on the server; the browser receives fully rendered HTML plus a tiny fixed runtime script that relays user events back to the server and applies returned HTML patches. The mental model is Angular's (components, directives, DI, guards); the runtime model is Phoenix LiveView's / Blazor Server's, implemented with Go's concurrency primitives.

## Positioning (honest version)

| | Client SPA (React/Angular) | HTMX / Hotwire | LiveView / Blazor Server | **Liquid** |
| --- | --- | --- | --- | --- |
| Where state lives | Browser | Server (stateless) | Server (stateful session) | Server (stateful session) |
| Client payload | Large JS bundle + hydration JSON | Small lib | Small runtime | Small fixed runtime (~1–2 KB) |
| Update mechanism | Client re-render | HTML fragment swap | Server diff over WS | HTML patch over fetch/SSE |
| Language | JS/TS | Any | Elixir / C# | **Go** |
| Differentiators | — | — | — | AOT-compiled templates in the binary; agent-first generation pipeline; goroutine fan-out data loading |

Liquid does **not** eliminate server round-trips for interaction, and it is **not** zero-JavaScript — it is *zero application JavaScript*. Its bets are: Go's deployment/concurrency story, compile-time validation as a guardrail for AI-generated UI, and a single-struct component scope that is easy for agents to edit.

## Core concepts

### Components

A component is a Go struct implementing the `Component` interface:

```go
type Component interface {
    Selector() string   // custom element tag, e.g. "app-user-card"
    Template() string   // .lsx markup (usually embedded from a file at build time)
}

// Optional lifecycle & capability interfaces:
type OnInit interface{ OnInit(ctx liquid.Ctx) error } // runs before render, after bindings (D18);
                                                      // liquid.Ctx embeds context.Context + request accessors;
                                                      // error → framework error page, panics recovered by router
type HeadProvider interface{ Head() liquid.Head }     // per-route <title> + meta (D22)
type AssetProvider interface{ Assets() string }       // scoped CSS injected into <head> (deferred past v0.1)
```

- **Exported fields** are template-visible state and binding targets.
- **Struct tags** declare bindings: `pathParam:"id"` binds URL params; input bindings from parents map by field name.
- **Exported methods registered as actions** (see Interactivity) handle user events.

**Lifecycle & concurrency rule:** routes register component *types*, not instances. The router creates a **fresh instance per request** (`reflect.New`), injects dependencies, binds params/inputs, calls `OnInit(ctx)`, renders. Long-lived instances exist only inside an interactive session's registry entry (below). Nothing about a component may be shared mutable state across requests.

### Templates (`.lsx`) and the AOT compiler

`.lsx` files are HTML with Angular-style sugar. A build-time compiler (`liquid` CLI) parses them into an **HTML node tree** (`golang.org/x/net/html` — not line regexes), applies directive transforms, and emits standard `html/template` sources embedded into the binary via `go:embed` / generated Go code. Templates are compiled **once**; requests execute cached `*template.Template`s.

The compiler is also the agent guardrail: syntax errors, unknown directives, references to fields that don't exist on the paired struct, and unregistered `(click)` actions are all reported at build time with structured diagnostics.

Directive semantics are specified in [template-syntax.md](template-syntax.md). Summary of transforms:

| Sugar | Compiles to |
| --- | --- |
| `{{ Field }}` | `{{ .Field }}` (auto-escaped by `html/template`) |
| `*goIf="Cond"` | `{{if .Cond}}…{{end}}` around the element |
| `*goFor="let x of List"` | `{{range $x := .List}}…{{end}}` around the element |
| `(click)="Method"` | `data-liquid-action="Method"` + runtime listener; `Method` added to the component's action allowlist |
| `[hydroId]` | `data-hydro-id="{{ .HydroID }}"` |
| `[input]="ParentField"` on child selectors | reflection-based parent→child field copy at render |
| `*goTranslate="KEY"` | server-side i18n lookup injected as element text |
| `<form>` | hidden CSRF token input auto-injected |

### Rendering pipeline

```
HTTP request
  → interceptor chain (Angular HttpInterceptor-style, may short-circuit)
  → route match (compiled :param patterns)
  → route guards (CanActivate)
  → new component instance ← DI injection ← pathParam binding
  → OnInit(ctx) error     (fan out goroutines for data; ctx timeouts per source;
                           error → framework error page, panic recovered — D18)
  → render component tree (cached templates; child selectors resolved from a
                           component registry, instantiated per occurrence,
                           inputs bound, recursively rendered)
  → document shell (title, scoped Assets() styles, runtime script)
  → stream to ResponseWriter
```

### Interactivity ("hydro" sessions)

For interactive components:

1. At render, the server creates a **session registry entry** keyed under the browser's `liquid_session` cookie: `sessionID → {hydroId → HydroState}` (D15). Each `hydroId` is a **cryptographically random opaque string** (never a memory address — see REPORT §4.1), embedded as `data-hydro-id`; entries expire via idle-timeout GC.
2. The fixed runtime script listens for elements with `data-liquid-action`; on event it sends `{hydroId, action, payload, csrfToken}` to `/hydro-event` via fetch POST.
3. The server enforces a body-size limit (D20), validates CSRF, resolves the token, checks `action` against the component's **compile-time allowlist** (never bare `MethodByName` on client input), invokes the handler (`func()` or `func(e liquid.Event)` — D11), re-renders the `[hydroId]` subtree, and responds with a small envelope: the HTML patch *or* `{redirect: "/path"}` (D19). The runtime swaps `innerHTML` at the `[hydroId]` boundary (D14 — no diffing engine in v0.1), preserving focus by element `id` and never overwriting the actively-focused input's value (D21).
4. **Server push:** components may subscribe to `BehaviorSubject[T]` streams; emissions re-render and push patches over the session's **SSE** stream (D3). Subscriptions must be tied to session lifetime (unsubscribe on GC) to avoid leaks. An SSE reconnect triggers a full re-render of current state — no missed-patch replay (D20).
5. **Hardening (D20):** events for one session are serialized behind a per-session mutex; the registry is bounded (per-session and global caps, LRU eviction, configurable with sane defaults).

v0.1 transport is stdlib-only (fetch + SSE). WebSocket is a v0.2 upgrade via a maintained library (e.g. `nhooyr.io/websocket`); a hand-rolled implementation is explicitly out of scope (the sketch in the source docs doesn't unmask client frames and violates RFC 6455).

### State & reactivity

`BehaviorSubject[T]` provides synchronous, mutex-guarded observable state (current value + subscribers, `Next`/`Subscribe`/`Value`). Design rules:

- App-lifetime subjects live in **services** registered with the injector.
- Request-scoped reads use `.Value()`; only interactive sessions hold subscriptions, with mandatory unsubscribe hooks.

### Dependency injection

A reflection-based injector: services registered by type, components declare dependencies as struct fields, the router populates them at instantiation. v1 scope: singleton providers, concrete-type and interface matching, clear error on unresolvable required deps. (The source docs claim "hierarchical" DI but define a flat map; hierarchy is deferred until a concrete need exists.)

### Security model

- **XSS:** all interpolation flows through `html/template` contextual escaping; blueprint/catalog templates are pre-vetted and parameterized, so agents never inject raw markup.
- **CSRF:** HMAC-signed, expiring tokens bound to session ID, auto-injected into forms and hydro-event payloads, validated before any action dispatch.
- **Action dispatch:** compile-time allowlist per component; unknown actions are 404s.
- **Session tokens:** random, opaque, idle-expiring. No memory addresses, ever.
- **Server-side secrecy:** API keys, prompts, and business logic never leave the server — inherent to the model.

### Agentic pipeline

The agent workflow the framework is designed around:

```
agent writes/edits component .lsx + struct
  → `liquid build` (AOT compile: template + Go type check)
  → structured errors fed back to agent for self-repair, OR
  → binary/route table updated (dev-mode hot reload)
```

Supporting pieces: the **Blueprint Catalog** (`BlueprintCatalog`) of parameterized, pre-vetted templates keyed by intent, style guides applied at hydration, and required-field validation so agents get deterministic errors instead of silent misrenders.

## Subsystem inventory & v0.1 scope

| Subsystem | Source doc status | v0.1? |
| --- | --- | --- |
| Component model + lifecycle | sketched | ✅ core |
| AOT template compiler (CLI) | invoked but never defined | ✅ core — build first |
| Router (params, guards) | sketched (buggy) | ✅ params; guards ✅ (small) |
| Hydro event loop (fetch) | sketched (insecure) | ✅ with corrections |
| Component nesting + inputs | sketched (infinite-loop bug) | ✅ via compiler, not regex |
| CSRF engine | sketched (parse bug) | ✅ (small, fix Sscanf) |
| DI container | sketched | ⚠️ minimal singleton version |
| Server push (SSE) + BehaviorSubject | sketched as WS (RFC-violating) | ✅ SSE in v0.1; WS in v0.2 via library |
| Forms/validation | sketched | ❌ defer |
| i18n (`*goTranslate`) | referenced, undefined | ❌ defer |
| Redis session persistence | referenced, undefined | ❌ defer (needs serializable-state design) |
| Interceptors/middleware | sketched | ❌ defer (std `http.Handler` middleware suffices initially) |
| Scoped assets | sketched | ❌ defer |
| CLI scaffolder (`generate component`) | sketched | ✅ cheap once compiler exists |
| `liquidtest` component test harness | new (gap review, D23) | ✅ minimal: render, query, fire action, assert |
| Head API + `/static/` file serving | new (gap review, D22) | ✅ minimal |
| Blueprint catalog | sketched | ❌ defer to agent-tooling phase |
| Benchmarks vs Node SSR | sketched | ❌ defer; no numeric claims until measured |

All design decisions are settled (D1–D17) — see [design-decisions.md](design-decisions.md) for the full log and [HANDOFF.md](HANDOFF.md) for current state and build order.
