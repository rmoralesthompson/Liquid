# Example: dashboard

The D17 integration app — one page exercising every Liquid v0.1 subsystem
end-to-end.

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
| Admin area link | `ui/admin.{go,lsx}`, guard in `main.go` | guarded route: allow / redirect / deny (D4/D19) |

Component structs and templates live in `ui/` — the directory `liquid build`
compiles. The wiring (`newApp`, the guard, the metric ticker) sits above it
in `main.go`, because code referencing the components as `liquid.Component`
only type-checks once the `Template` methods are generated — keeping it out
of `ui/` lets a from-scratch build always succeed.

The runtime tests (`main_test.go`) drive the same five features through
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
