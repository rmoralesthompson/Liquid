# Liquid — Implementation Handoff

**Date:** 2026-08-02 · **For:** a fresh session continuing implementation. **Frontier: [#4 — `*goIf` / `*goFor` directives](https://github.com/rmoralesthompson/Liquid/issues/4) and [#5 — Route params, guards, `OnInit`](https://github.com/rmoralesthompson/Liquid/issues/5)** (independent — #4 is compiler-side, #5 is `core/`-side; #6 opens once #5 lands).

## 1. What Liquid is (30 seconds)

A server-driven UI framework for Go: **Phoenix LiveView's runtime model + Angular's mental model, designed as a target for AI code generation.** Components are single Go structs paired with `.lsx` templates (HTML + Angular-style sugar), AOT-compiled to `html/template`. Interactivity is server-round-trip: a tiny fixed runtime posts events, the server patches HTML; push via SSE. The compiler doubles as the agent guardrail — structured diagnostics feed agent self-repair.

## 2. Where things stand

**Design is closed** (24 decisions, D1–D24, all settled — full text in [design-decisions.md](design-decisions.md); don't re-litigate). **Planning is published to the tracker:** [issue #1](https://github.com/rmoralesthompson/Liquid/issues/1) is the v0.1 spec (PRD, incl. the agreed two-seam testing model); **#2–#13** are tracer-bullet tickets with GitHub-native blocking edges (each also carries a `Blocked by` text fallback — keep both in sync if the graph changes).

**Tickets #2 and #3 are done, reviewed, committed, closed** (#2: `59a759f`; #3: see `git log`). Code that now exists:

- `go.mod` — module `github.com/rmoralesthompson/liquid`, **`go 1.23.0`** (D24 floor — beware: `go get` likes to bump this; `golang.org/x/net` is pinned at `v0.38.0`, the last line compatible with 1.23).
- `cmd/liquid/main.go` — CLI skeleton, `build` verb, thin `run(args)` wrapper.
- `cmd/liquid/internal/compiler/` — the AOT compiler: pairs `foo.lsx` ↔ `foo.go` by filename, parses `.lsx` with `x/net/html` (**node tree, never regex** — invariant), rewrites `{{ Field }}` → `{{ .Field }}` in text nodes and attribute values, emits a generated `Template() string` method as `foo_gen.go` (gofmt'd via `go/format`). Note: emits generated Go rather than literal `go:embed` — a doc-sanctioned deviation, recorded in a comment on #2.
- **Diagnostics (#3, D13):** `Build(ctx, dir)`/`Vet(ctx, dir)` → `([]Diagnostic, error)` — user-fixable problems come back as diagnostics (`diagnostic.go`: D13 JSON contract, `Severity`/`Code` named types, codes LSX001 unclosed `{{` · LSX002 missing paired .go · LSX003 struct missing from package · LSX004 unknown field/method ref with Levenshtein "did you mean"); mechanical failures stay `error`. `scan.go` scans **raw source** for `{{ }}` positions (1-based line / byte col, pointing at the identifier, not the braces) — the transform itself stays node-tree, invariant intact. `vet.go` cross-checks refs via `go/types` using `golang.org/x/tools/go/packages` (**pinned v0.31.0** — later minors may bump the `go` directive past 1.23; check before upgrading). Component packages need a `go.mod` for `packages.Load` — fixtures each carry one. No `_gen.go` is written for a file with diagnostics. CLI: `liquid <build|vet> [dir] [--json]`; any diagnostic → non-zero exit; `--json` always emits an array (`[]` when clean); unknown flags/extra args rejected. Deferrals recorded on #4 (directive-level malformed-syntax diagnostics; structured diagnostics for a type-broken paired package).
- `core/` — package `liquid`: `Component` interface (`Selector()`/`Template()`); `App` with `New(opts...)` (`WithLogger` for pluggable slog), `Route` (template parsed **once, at registration**; prototype validated — non-nil reference-typed fields rejected to enforce the per-request-instance invariant), `ServeHTTP` (GET/HEAD only, exact-path match, fresh prototype copy per request, buffered render → 500+log on error).
- Tests: 12 across 3 packages, all `-race` green — compiler seam (fixture → generated output; generated text executes as `html/template`), runtime seam (`httptest`: render, escaping, 404, 405, registration-time template errors, injected-logger 500 path, concurrency), and an **end-to-end tracer** (`cmd/liquid/tracer_test.go`: real `run(["build", dir])` → serve the generated template → assert HTML).
- CI (`.github/workflows/ci.yml`) activates on `go.mod` presence: golangci-lint (config is **`.golangci-lint.yml`** — nonstandard filename, passed via `--config`) + `go test -race`. Lint is strict: wrapcheck (wrap errors crossing package boundaries), revive `exported` (doc comments on all exported items).

## 3. How to work (the per-ticket pipeline)

1. **Work the frontier**: open tickets with zero open blockers. After #3, the frontier is **#4** and **#5** (#6 waits on #5). Claim by assigning: `gh issue edit <n> --add-assignee @me`.
2. Per ticket: `/tdd` → `/code-review` (dual-axis: Standards vs CLAUDE.md + smells; Spec vs the ticket's acceptance criteria — fetch with `gh issue view <n> --comments`, comments included) → fix findings → commit (`Closes #<n>`) → push.
3. Testing seams are **pre-agreed** (recorded in #1) — don't renegotiate: (1) the `liquid` CLI/compiler boundary: fixtures in → generated code + diagnostics out; (2) the HTTP runtime seam (later wrapped by `liquidtest`, ticket #7). Red before green; report pins (green-on-write tests) honestly.
4. Defer out-of-scope review findings by commenting them onto the owning ticket, not by expanding the slice.
5. Commands: `go build ./...` · `go test -v -race ./...` · `gofmt -l .` · `go vet ./...` · `golangci-lint run`.

## 4. The frontier tickets specifically

**[#4 — `*goIf` / `*goFor` directives](https://github.com/rmoralesthompson/Liquid/issues/4)** (compiler-side): extends `transform` in `compiler.go` (node-tree rewrites — never regex) and the diagnostic vocabulary. Read #4's comments first — two deferrals from #3's review live there (directive-level malformed-syntax diagnostics; structured diagnostics when the paired Go package itself doesn't type-check). Directive semantics: [template-syntax.md](template-syntax.md); D11 matters once `(click)` allowlisting appears (that's #7's territory, but vet rules for handler signatures start here).

**[#5 — Route params, guards, `OnInit`](https://github.com/rmoralesthompson/Liquid/issues/5)** (core-side): touches `core/` only — minimal overlap with #4, safe to parallelize. D4 (guards), D18 (`OnInit(ctx liquid.Ctx) error`, `liquid.Ctx` embeds `context.Context`), D19 (redirect variant). The runtime seam (`httptest`) is the testing boundary.

## 5. Reading order for a fresh context

1. This file.
2. `gh issue view <frontier-ticket> --comments` — the ticket + any deferred findings.
3. [design-decisions.md](design-decisions.md) — for #4: D1/D13 (compiler, diagnostics), D11; for #5: D4, D18, D19.
4. The code: `cmd/liquid/internal/compiler/` (`compiler.go`, `scan.go`, `vet.go`, `diagnostic.go` + tests — the pattern to extend), `core/app.go`.
5. [architecture.md](architecture.md) §Templates + §Agentic pipeline; [CLAUDE.md](../CLAUDE.md) invariants.
6. [REPORT.md](REPORT.md) §4 and `docs/source/` — only for the "why" behind invariants; treat all source-doc code as untrusted.
