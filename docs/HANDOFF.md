# Liquid — Implementation Handoff

**Date:** 2026-08-02 · **For:** a fresh session continuing implementation. **Next ticket: [#3 — Structured diagnostics + `liquid vet`](https://github.com/rmoralesthompson/Liquid/issues/3).**

## 1. What Liquid is (30 seconds)

A server-driven UI framework for Go: **Phoenix LiveView's runtime model + Angular's mental model, designed as a target for AI code generation.** Components are single Go structs paired with `.lsx` templates (HTML + Angular-style sugar), AOT-compiled to `html/template`. Interactivity is server-round-trip: a tiny fixed runtime posts events, the server patches HTML; push via SSE. The compiler doubles as the agent guardrail — structured diagnostics feed agent self-repair.

## 2. Where things stand

**Design is closed** (24 decisions, D1–D24, all settled — full text in [design-decisions.md](design-decisions.md); don't re-litigate). **Planning is published to the tracker:** [issue #1](https://github.com/rmoralesthompson/Liquid/issues/1) is the v0.1 spec (PRD, incl. the agreed two-seam testing model); **#2–#13** are tracer-bullet tickets with GitHub-native blocking edges (each also carries a `Blocked by` text fallback — keep both in sync if the graph changes).

**Ticket #2 is done, reviewed, committed, closed** (commit `59a759f`). Code that now exists:

- `go.mod` — module `github.com/rmoralesthompson/liquid`, **`go 1.23.0`** (D24 floor — beware: `go get` likes to bump this; `golang.org/x/net` is pinned at `v0.38.0`, the last line compatible with 1.23).
- `cmd/liquid/main.go` — CLI skeleton, `build` verb, thin `run(args)` wrapper.
- `cmd/liquid/internal/compiler/` — the AOT compiler: pairs `foo.lsx` ↔ `foo.go` by filename, parses `.lsx` with `x/net/html` (**node tree, never regex** — invariant), rewrites `{{ Field }}` → `{{ .Field }}` in text nodes and attribute values, emits a generated `Template() string` method as `foo_gen.go` (gofmt'd via `go/format`). Note: emits generated Go rather than literal `go:embed` — a doc-sanctioned deviation, recorded in a comment on #2.
- `core/` — package `liquid`: `Component` interface (`Selector()`/`Template()`); `App` with `New(opts...)` (`WithLogger` for pluggable slog), `Route` (template parsed **once, at registration**; prototype validated — non-nil reference-typed fields rejected to enforce the per-request-instance invariant), `ServeHTTP` (GET/HEAD only, exact-path match, fresh prototype copy per request, buffered render → 500+log on error).
- Tests: 12 across 3 packages, all `-race` green — compiler seam (fixture → generated output; generated text executes as `html/template`), runtime seam (`httptest`: render, escaping, 404, 405, registration-time template errors, injected-logger 500 path, concurrency), and an **end-to-end tracer** (`cmd/liquid/tracer_test.go`: real `run(["build", dir])` → serve the generated template → assert HTML).
- CI (`.github/workflows/ci.yml`) activates on `go.mod` presence: golangci-lint (config is **`.golangci-lint.yml`** — nonstandard filename, passed via `--config`) + `go test -race`. Lint is strict: wrapcheck (wrap errors crossing package boundaries), revive `exported` (doc comments on all exported items).

## 3. How to work (the per-ticket pipeline)

1. **Work the frontier**: open tickets with zero open blockers. After #2, the frontier is **#3** and **#5** (#4 waits on #3; #6 waits on #3+#5). Claim by assigning: `gh issue edit <n> --add-assignee @me`.
2. Per ticket: `/tdd` → `/code-review` (dual-axis: Standards vs CLAUDE.md + smells; Spec vs the ticket's acceptance criteria — fetch with `gh issue view <n> --comments`, comments included) → fix findings → commit (`Closes #<n>`) → push.
3. Testing seams are **pre-agreed** (recorded in #1) — don't renegotiate: (1) the `liquid` CLI/compiler boundary: fixtures in → generated code + diagnostics out; (2) the HTTP runtime seam (later wrapped by `liquidtest`, ticket #7). Red before green; report pins (green-on-write tests) honestly.
4. Defer out-of-scope review findings by commenting them onto the owning ticket, not by expanding the slice.
5. Commands: `go build ./...` · `go test -v -race ./...` · `gofmt -l .` · `go vet ./...` · `golangci-lint run`.

## 4. Ticket #3 specifically

**Scope** ([issue #3](https://github.com/rmoralesthompson/Liquid/issues/3), decision D13): `liquid build`/`vet` emit diagnostics as JSON (`{file, line, col, severity, code, message, suggestion}`) behind `--json`, human text by default; `vet` cross-checks template references against the paired struct via `go/types`; malformed `.lsx` → diagnostic + non-zero exit; fixture tests assert exact codes/positions.

**Carry-over from #2's review** (already commented on #3): no failing-fixture tests exist yet; the compiler doesn't verify the paired struct exists (mismatch silently emits uncompilable code); consider a `ctx` param on the compiler's entry point and a dedicated type for compiled template text.

**Where it slots in:** `compiler.Build`/`compileFile`/`compileLSX` currently return wrapped `error`s with no position info — #3 turns these paths into position-carrying diagnostics. Interpolation rewriting happens in `rewriteInterpolations` (plain token scan inside node text/attrs). The `x/net/html` parser is lenient (it won't error on most malformed HTML — "malformed" diagnostics will mostly come from Liquid-level checks, not the parser). Struct field lookup for `vet` wants `go/types` over the fixture package — `golang.org/x/tools/go/packages` is the usual loader; adding it is a new dependency, which CLAUDE.md says needs justification (it has one: D1 mandates a `go/types`-based vet — but check it doesn't drag the `go` directive above 1.23).

**Also unblocked, if parallelizing:** [#5 — Route params, guards, `OnInit`](https://github.com/rmoralesthompson/Liquid/issues/5) (touches `core/` only; minimal overlap with #3's `cmd/liquid/internal/compiler/`).

## 5. Reading order for a fresh context

1. This file.
2. `gh issue view 3 --comments` — the ticket + carry-over findings.
3. [design-decisions.md](design-decisions.md) — D13 (diagnostics), D11 (handler signatures, needed for later vet rules), D1 (compiler shape).
4. The code: `cmd/liquid/internal/compiler/compiler.go` + `compiler_test.go` (the pattern to extend), `core/app.go`.
5. [architecture.md](architecture.md) §Templates + §Agentic pipeline; [CLAUDE.md](../CLAUDE.md) invariants.
6. [REPORT.md](REPORT.md) §4 and `docs/source/` — only for the "why" behind invariants; treat all source-doc code as untrusted.
