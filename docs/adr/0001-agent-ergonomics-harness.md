# ADR-0001: Measure agent ergonomics with a regression harness

- **Status:** Proposed
- **Date:** 2026-08-05
- **Tracking issue:** [#71](https://github.com/rmoralesthompson/Liquid/issues/71)
- **Depends on:** [#58](https://github.com/rmoralesthompson/Liquid/issues/58) (`liquid manifest --json`, PR #70), [#60](https://github.com/rmoralesthompson/Liquid/issues/60) (render snapshots), [#59](https://github.com/rmoralesthompson/Liquid/issues/59) (deterministic render); the D13 diagnostic contract and D29 `vet` leak check

## Context

Liquid's headline bet is that it is agent-first: a single-struct component scope
an agent can edit atomically, structured build diagnostics, and a
generate → build → feed-errors-back → self-repair loop (`architecture.md`,
"Agentic pipeline"). Today that claim is **asserted throughout the docs and
measured nowhere.**

This is exactly the situation the project already refuses to tolerate for
performance: the architecture spec and the v0.1 scope table both forbid numeric
performance claims without a benchmark to back them (`architecture.md`, subsystem
inventory: "no numeric claims until measured"). Agent ergonomics has no
equivalent guardrail. A future change to the compiler, the diagnostic format, or
the template surface could make the framework measurably *harder* for an agent to
write against, and nothing would catch it. The claim would silently rot.

We want the agent-ergonomics claim held to the same standard as the performance
claim: defensible by a number, and protected by a regression test.

## Decision

Adopt an **agent-ergonomics harness** as the mechanism that turns "designed for
agents" into a measured, regression-tested figure. It is a *regression test*, not
an absolute grade — it measures this framework against a pinned model and a fixed
task corpus, and gates changes that degrade the loop.

### Metrics

1. **First-pass compile rate** — does the agent's *first* emitted `.lsx` + struct
   pass `liquid build` / `liquid vet` clean? (0/1 per task)
2. **Repairs-to-green** — number of generate → build → feed-diagnostics-back
   iterations to a clean build, capped at N (N = give-up).
3. **Spec-match** — once it builds, does it do what the task asked? Scored against
   a structural oracle, not human review.
4. **Diagnostic actionability** (derived) — does feeding a given diagnostic back
   actually reduce repairs-to-green versus a stripped-down version of the same
   error? This is the number that justifies (or condemns) the detail level of
   messages like LSX017.

### Oracles (why this is mostly integration, not new infrastructure)

Spec-match needs no human in the scoring loop, because the pieces already exist
or are in flight:

| Need | Provided by |
| --- | --- |
| Structural answer key ("did it expose action `X`, field `Y`?") | #58 `liquid manifest --json` |
| Stable rendered output to diff against expected | #59 deterministic render |
| Golden expected-DOM fixtures per task | #60 render snapshots |
| Machine-parseable diagnostics to score and feed back | D13 diagnostic contract / D29 `vet` |

Spec-match = run `manifest --json` on the agent's output and compare to the
task's expected manifest, then render and diff against the #60 snapshot.

### Two tiers

The naive design — run a real LLM against the full corpus in CI — is both
expensive and *flaky*, because the model is stochastic. Split it:

**Tier A — diagnostic-contract checks. No LLM, per-PR, deterministic.**
A corpus of deliberately-*broken* components (a `(click)` bound to a missing
method, a type-mismatched `[input]`, a discarded `Subscribe`). Assert the
compiler/`vet` output *itself*, not an agent's reaction to it:

- every diagnostic carries `{code, file, line, fix-hint}` per the D13 contract;
- the broken `(click)` yields exactly the expected code at the expected line;
- LSX017 fires as an **error** on a discarded cancel and a **warning** on a
  captured one.

This is fast, free, and catches the most probable regression: a compiler refactor
that drops a diagnostic's line number or code. It runs on every PR.

**Tier B — full agent-loop scoring. LLM in the loop, nightly / on-demand.**
The real generate → build → repair loop against the task corpus. Because it is
stochastic and costly:

- N samples per task (e.g. 5); report **mean + variance**, never a point;
- pin model + temperature; treat the baseline as a **tolerance band**
  (`first-pass rate ≥ target − band`), not an exact assertion, so it does not flap;
- gate on regression against a stored baseline, not against an absolute target;
- never on the PR critical path.

### Task corpus (start ~8–10, grow from real usage)

Tiered by the agent skill each exercises: greenfield component; add interactivity
(`(click)` action); parent → child `[input]` wiring; follow an observable via the
managed `Observe`-in-`Subscriptions()` path; repair-only (hand it a broken
component + diagnostics, score the fix in isolation from generation); and a
"trap" whose obvious solution trips a guardrail (raw `Subscribe`), verifying the
agent is steered onto the managed path by the error. Each task is
`{prompt, expected_manifest.json, expected_render.snapshot, cap_N}`.

## Consequences

**Positive**

- The agent-first claim becomes defensible by a number and protected by a test —
  parity with the performance-claim discipline.
- Tier A delivers the core guarantee *before any LLM integration*: a change that
  degrades diagnostic quality fails the build.
- It doubles as a design feedback loop — "diagnostic actionability" tells us
  which diagnostics actually help an agent self-repair and which are noise.

**Negative / limits (stated so the harness is not oversold)**

- **Tier B is a distribution, not a fact.** A single green run proves nothing;
  results are reported with variance.
- **It measures the loop, not a ceiling.** Scores reflect *this framework + this
  model*; swapping models moves the number. It is a regression test, not an
  absolute grade.
- **The corpus is the bias.** The tasks encode our assumptions about what agents
  do; a wrong corpus yields a confident wrong number. The corpus is grown from
  real usage and marked as sampled.
- **Tier B costs real tokens/time** — nightly or on-demand, never per-commit.

## Scope and sequencing

Agent-tooling-phase work per the v0.1 subsystem scope table — *decide now, build
after core.* This ADR records the decision; #71 tracks the build. The smallest
useful increment is **Tier A alone** (~6 broken-component fixtures asserting the
D13/D29 contract), shippable as soon as the compiler's diagnostic surface exists.
Tier B is unblocked once #58 (`manifest --json`) and #60 (render snapshots) land,
after which it is largely glue over those oracles.

## Alternatives considered

- **Assert-only (status quo).** Rejected: the project already rejects this shape
  of unmeasured claim for performance; agent ergonomics deserves the same bar.
- **Docs + MCP server as "agent-friendliness."** Necessary but not a measurement;
  it makes the framework easier to *describe* to an agent, not verifiably easier
  to *write*. Orthogonal to this ADR.
- **Full LLM loop in CI (single tier).** Rejected: stochastic and expensive on
  the PR critical path. The Tier A / Tier B split preserves a deterministic
  per-PR gate while keeping the costly loop off the hot path.
