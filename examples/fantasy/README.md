# Example: fantasy

A fantasy-football starting lineup whose **projected points stream live over
SSE**. It's a focused counterpart to the [`dashboard`](../dashboard) example:
one interactive card (the roster) fed structured data (`[]ui.Player`) pushed
from a shared subject, in a gunmetal-and-neon theme with server-drawn SVG
headshots — no client charting library and no JS framework.

## Run it

From the repo root:

```sh
go run ./cmd/liquid dev examples/fantasy          # watch + rebuild + reload on http://localhost:8080
```

Or without the dev loop:

```sh
go run ./cmd/liquid build examples/fantasy/ui     # compile the .lsx templates
go run ./examples/fantasy                         # serve on http://localhost:8080
```

The generated `ui/*_gen.go` files are committed, so the build command is only
needed after editing a `.lsx` template or a component struct.

## What it shows

| Piece | Files | Subsystem |
| --- | --- | --- |
| Lineup page | `ui/lineup.{go,lsx}` | routed static shell — theme, brand bar, shared SVG defs — composing the interactive child |
| Live roster | `ui/roster.{go,lsx}` | `*goFor` over structured rows (`[]Player`) pushed over SSE from a shared `BehaviorSubject`, re-rendered on every emission (D3/D20) |
| Player model | `ui/player.go` | display formatting — all numbers are preformatted on the server; templates do no arithmetic |

### The data flow

> **All players and teams are fictional.** `seedLineup` assembles names and
> franchises at random from invented token pools — it never uses the name or
> likeness of a real person or organisation, and must not be changed to. A
> fixed PRNG seed keeps the generated roster deterministic (stable first paint
> and tests) without hand-picking anyone.

`main.go` owns the raw lineup (`seedLineup`) and the feed. `driveProjections`
random-walks each player's projected points once a second and republishes the
whole lineup through a `BehaviorSubject[[]ui.Player]`; the `Roster` child
subscribes to it (`Subscriptions` → `liquid.Observe`), so every emission
re-renders the rail over the session's SSE stream. Like the dashboard's ticker,
the roster holds **no per-instance state** — the template reads the current
lineup straight off the injected subject via `Roster.Players`, so the first
(page-load) render and every pushed re-render draw from the same source. The
seed is deterministic, so the first paint and the tests are stable.

Each row carries a **Player Name, Team, headshot, and projected points** — the
four fields the model exposes (`ui.Player`), plus position and jersey number.

### Headshots are just SVG

There are no image files. Each headshot is an inline SVG head-and-shoulders
silhouette clipped to a disk (`ui/roster.lsx`); the disk's team colour comes
from a CSS class keyed off `Player.TeamClass` (e.g. `avatar--kc`), so `fill`
never has to carry a dynamic value through `html/template`'s CSS context. Swap
in `<img src>` real headshots and nothing else changes.

### Theme

Gunmetal steel panels (`--panel`) on a dark field, with gentle neon-green
highlights (`--neon`) on the projected points, the live pill, and the headshot
rings. All CSS is inline in `ui/lineup.lsx`; it respects
`prefers-reduced-motion` and collapses to a single column on narrow screens.

## Tests

`main_test.go` drives the app through `liquidtest`: the seeded lineup renders,
a projection update is pushed over SSE (a bumped total arrives on the stream),
and the live region declares `aria-live="polite"` (D21). Run them with:

```sh
go test -race ./examples/fantasy
```
