# Benchmarks — a measured baseline

Liquid publishes **measured numbers, not performance claims.** D9 forbids
comparative or superlative wording ("fast", "faster than X", "low latency")
unless a benchmark backs it, so this page reports only what the benchmarks
actually measured, on one machine, at one point in time.

**These numbers are informational, not a guarantee.** They are a single-machine
snapshot to answer "roughly, what does the framework cost per operation?" — not
a service-level objective, and not a comparison against any other framework.
Your hardware, Go version, component complexity, and load will produce different
results. Treat them as an order-of-magnitude baseline you can reproduce and
watch for regressions, nothing more.

## What is measured

Two seams, driven in-process through `App.ServeHTTP` against an
`httptest.Recorder` — the real handler stack, no TCP socket — so the numbers
reflect framework work rather than loopback networking. The source is
[`core/bench_test.go`](../core/bench_test.go).

- **`Render/Leaf`** — a `GET` render of a single leaf component
  (`<div class="card">{{ .Name }}</div>`) through the full route → render →
  document-shell path.
- **`Render/NestedTree`** — a `GET` render of a three-level component tree
  (page → panel → card) with string interpolation at each level, wired through
  the compiled `liquidChild` seam. Child-bearing templates are re-parsed per
  render, so this exercises a heavier path than a leaf.
- **`HydroEventDispatch`** — one `/hydro-event` round trip end-to-end: CSRF
  check, session and action-allowlist lookup, action dispatch under the
  per-session mutex, subtree re-render, and envelope encode. The interactive
  session is established once; only the dispatch is timed.

## Results

Measured on the environment below. `ns/op` is nanoseconds per operation; `B/op`
and `allocs/op` are bytes and heap allocations per operation (from
`-benchmem`).

| Benchmark            | ns/op  | B/op   | allocs/op |
| -------------------- | -----: | -----: | --------: |
| `Render/Leaf`        |  9,448 |  2,913 |        53 |
| `Render/NestedTree`  | 62,088 | 29,235 |       299 |
| `HydroEventDispatch` | 21,532 | 11,035 |        89 |

**Environment:** `goos: darwin`, `goarch: amd64`, Intel Core i7-9750H @ 2.60GHz,
Go 1.24.2. Run with `-race` off (the race detector perturbs both timing and
allocation counts). Numbers vary run-to-run by roughly ±10%; the figures above
are a representative single run, not a best-of.

## Reproducing

```
make bench
```

which runs `go test -run '^$' -bench . -benchmem ./core`. Re-run on your own
hardware before drawing any conclusion — the point of this page is that the
measurement is reproducible, not that these specific numbers hold anywhere else.
