# Liquid — Project Guidelines

Liquid is a server-driven UI framework for Go (Angular-style component model, LiveView-style interactivity, agent-first tooling). **Greenfield: no Go code exists yet.** The design lives in `docs/`:

- `docs/REPORT.md` — analysis of the original design material; known defects to avoid
- `docs/architecture.md` — authoritative architecture spec
- `docs/template-syntax.md` — `.lsx` directive reference
- `docs/design-decisions.md` — open questions (check before assuming a decision is settled)
- `docs/source/` — raw exploratory material; **inspiration only, not reference code** (snippets there do not compile and contain known security/correctness bugs)

## Build & Test (once code exists)

- Build: `go build ./...`
- Test: `go test -v -race ./...`
- Single test: `go test -v -run TestName ./...`
- Format: `go fmt ./...`
- Lint: `golangci-lint run` (config: `.golangci-lint.yml`)

## Go standards

- Short receiver names (`r *Router`); single-method interfaces end in `-er` where natural.
- Errors returned as last value; wrap with `fmt.Errorf("context: %w", err)`.
- `ctx context.Context` is always the first parameter; never stored in structs.
- Imports in 3 groups: stdlib, third-party, local.
- Goroutines need a defined lifecycle and owner; no leaked channels or subscribers.
- Prefer stdlib; every new dependency needs justification.
- Logging via `log/slog` with a pluggable handler; no `fmt.Println` in core (D24).
- Minimum Go version: 1.23 (D24).

## Framework invariants (do not violate)

- Component instances are **per-request** (or per interactive session) — never shared mutable singletons across requests.
- Hydro session tokens are **opaque random strings** — never memory addresses or anything derived from them.
- Event dispatch goes through the compile-time **action allowlist** — never `MethodByName` on client input.
- All template interpolation flows through `html/template` contextual escaping.
- Template transforms operate on a parsed **HTML node tree**, not line/regex processing.
- Events for the same hydro session are **serialized** — never dispatched concurrently against one live component instance.
- The in-memory session registry is **bounded** (per-session and global caps with eviction) — unauthenticated traffic must not be able to grow it without limit.
- No performance claims in docs/README without a benchmark backing them.

## Target layout

```
liquid/
├── cmd/liquid/       # CLI: AOT compiler, scaffolder, dev server
├── core/             # component model, router, hydro, DI, csrf, state
├── examples/         # runnable example apps
└── docs/
```

## Agent skills

### Issue tracker

GitHub Issues at [rmoralesthompson/Liquid](https://github.com/rmoralesthompson/Liquid), via the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

Default five-role vocabulary (`needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`), used as-is. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context layout: `CONTEXT.md` + `docs/adr/` at the repo root, created lazily by `/domain-modeling` as terms and decisions get resolved. See `docs/agents/domain.md`.
