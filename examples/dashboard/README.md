# Example: dashboard

The D17 integration app — one page exercising every Liquid v0.1 subsystem
end-to-end.

![The Liquid example dashboard: a glassmorphism UI with a live market ticker, a server-rendered SVG chart, and interactive cards](../../docs/dashboard.png)

## Run it

From the repo root:

```sh
go run ./cmd/liquid dev examples/dashboard        # watch + rebuild + reload on http://localhost:8080
```

`liquid dev` compiles the `.lsx` templates, builds the app with the
`liquiddev` tag (which adds the reload/overlay script to every page), and
restarts it on every source change; a broken template renders the D13
diagnostics as an in-browser overlay instead. (The overlay is served by the
running app — if the very first build fails there is no app to host it yet,
and the diagnostics appear in the terminal only.) To run without the dev
loop:

```sh
go run ./cmd/liquid build examples/dashboard/ui   # compile the .lsx templates
go run ./examples/dashboard                       # serve on http://localhost:8080
```

The generated `ui/*_gen.go` files are committed, so the build command is only
needed after editing a `.lsx` template or a component struct
(`cmd/liquid`'s `TestExampleDashboardBuildsCleanAndGenIsFresh` fails if they
drift).

## What each card exercises

| Card | Files | Subsystem |
| --- | --- | --- |
| Counter | `ui/counter.{go,lsx}` | `(click)` event round-trip and HTML patching (D10/D11) |
| Requests per second | `ui/metric.{go,lsx}` | SSE server push from a `BehaviorSubject` service via DI (D3/D8) |
| Deploys this week | `ui/stat_card.{go,lsx}` | nested interactive child fed by `[input]` bindings (D14) |
| Board name | `ui/renamer.{go,lsx}` | `(submit)` + auto-injected CSRF token (D12/D15) |
| Markets ticker | `ui/ticker.{go,lsx}` | live rail of structured data (`[]Quote`) pushed over SSE from a shared subject; `*goFor` over the rows (D3) |
| Portfolio value | `ui/chart.{go,lsx}` | **server-rendered SVG** time-series, redrawn live over SSE — no client charting library |
| Admin area link | `ui/admin.{go,lsx}`, guard in `main.go` | guarded route: allow / redirect / deny (D4/D19) |

### Ticker and chart: live data without `OnInit`

The ticker and chart are pushed cards like the metric, but they carry **no
per-instance state**. Child components skip `OnInit`, so instead of seeding a
field, each reads its injected subject through a template-visible method
(`Ticker.Quotes`, `Chart.Line`/`Area`/`Last`/…). That makes the first
(page-load) render and every SSE re-render draw from the same source, and the
`Subscriptions()` binding exists only to drive the push (its `apply` is a
no-op). The `driveMarket`/`driveSeries` goroutines in `main.go` are the
fake-data feeds; the subjects are seeded deterministically (`seedAssets`,
`seedSeries`) so the first paint and the tests are stable.

**Charts are just SVG.** `Chart` maps the rolling value window into a fixed
`viewBox` and emits a `<polyline>`/`<path>` on the server — the same way any
other markup is rendered, so it redraws over the ordinary D3 push path. Any
chart expressible as SVG (lines, areas, bars, gauges) works the same way; the
data-to-geometry math lives in `chart.go`.

Component structs and templates live in `ui/` — the directory `liquid build`
compiles. The wiring (`newApp`, the guard, the metric ticker) sits above it
in `main.go`, because code referencing the components as `liquid.Component`
only type-checks once the `Template` methods are generated — keeping it out
of `ui/` lets a from-scratch build always succeed.

The runtime tests (`main_test.go`) drive these features through
`liquidtest`; run them with `go test -race ./examples/dashboard`.

## Accessibility checklist (D21)

- [x] The live metric region declares `aria-live="polite"` so assistive tech
  announces pushed updates — the patch swap itself emits no announcement
  (asserted by `TestMetricRegionDeclaresAriaLive`).
- [x] Every focusable element inside an interactive component has a stable
  `id` (`#increment`, `#pin`, `#new-name`, `#save`) — the runtime restores
  focus by element `id` after a patch.
- [x] Focus survives a patch: focus a button, trigger a round-trip, focus
  stays put (browser-verified; see the checklist run on ticket #13).
- [x] Typed input is never clobbered: text typed into "New name" survives
  patches to the same card because the runtime skips the actively-focused
  input's value (browser-verified).
- [x] The page is one `<main>` landmark with a single `h1`; each card is a
  `<section>` with its own heading.

Known limitations (documented in D21 and `core/runtime.js`): patching is an
`innerHTML` swap, not a DOM merge — focus on an element without an `id` is
lost, selection restore is best-effort, and a patch does not update
attributes on the `[hydroId]` boundary element itself. Full morphdom-style
merging is the v0.2+ answer.
