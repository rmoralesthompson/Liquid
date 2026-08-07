# Example: fantasy

A live **fantasy-football league dashboard**. The first page (`/`) shows your
weekly head-to-head, the league standings, the other matchups around the league,
and a gameday news-and-stats ticker along the bottom — **every score, ladder,
and ticker item streaming in over SSE** from server-side feeds, with no client
charting library and no JS framework. A parameterised second page (`/team/:id`)
shows **any league team's full lineup** — your team's is the live roster, every
other team's is a seeded static one — reachable by clicking a matchup tile or a
standings row.

It's a fuller counterpart to the [`dashboard`](../dashboard) example: four
independent live feeds fan out to five interactive cards across two pages.

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
| League dashboard (page 1) | `ui/league.{go,lsx}` | routed page — theme + brand bar — nesting the four live cards below |
| Weekly matchup | `ui/matchup.{go,lsx}` | your head-to-head: live scores that **flash on each change**, projections, an SVG win-probability bar; **both tiles link to that team's full lineup**; the **top performer is wired to your live roster**, with a `(click)` toggle that expands your top four |
| League standings | `ui/standings.{go,lsx}` | `*goFor` ladder that re-sorts server-side as points-for tick up; **every row links to that team's lineup**; a `(click)` **Full / Top 6** toggle and an `(input)` **search box** that filters by team or manager |
| Around the league | `ui/around.{go,lsx}` | the week's other matchups, live scores; **click a game to feature it** — the row expands to a win-probability bar (the `(click)` carries the game's index as a typed payload) |
| Gameday ticker | `ui/ticker.{go,lsx}` | a rolling window of fictional news/stat items, pinned to the bottom, **scrolling right-to-left** (a duplicated CSS-animated track), pushed as they "happen" |
| Team lineup (`/team/:id`) | `ui/lineup.{go,lsx}` + `ui/roster.{go,lsx}` + `ui/team.go` | any team's full lineup on the same broadcast theme, resolved from the `:id` slug via an injected `TeamStore`: a top nav that links **back to the dashboard**, a matchup hero, a projected total, and `*goFor` rows with position-coloured badges, server-drawn SVG headshots, and per-player projection bars. Your team renders the **live** roster (SSE); every other team a **static** seeded one; an unknown slug shows a not-found panel |
| Models | `ui/model.go`, `ui/player.go` | display formatting — all numbers preformatted on the server; templates do no arithmetic |

### The data flow

> **Everything is fictional.** `seedLeague`, `seedLineup`, `seedMatch`,
> `seedTicker`, and `seedSlate` assemble names, managers, players, clubs, and a
> league at random from invented token pools — never the name or likeness of a
> real person or organisation, and they must not be changed to. Fixed PRNG seeds
> keep the generated data deterministic (stable first paint and tests) without
> hand-picking anyone.

`main.go` owns four live feeds — `BehaviorSubject`s for the matchup, the
standings, the ticker window, and the other-matchups slate (plus the lineup for
page two) — and a driver goroutine per feed that walks the numbers on a timer
and republishes via `Next`. Each interactive card declares the feed it needs
with an `inject:""` field, `app.Provide`s make the subjects injectable, and each
card's `Subscriptions()` (→ `liquid.Observe`) re-renders it over the session's
SSE stream on every emission. The cards hold **no per-instance state** — their
templates read the current snapshot straight off the injected subject, so the
first (page-load) render and every pushed re-render draw from the same source.

### The win bar and headshots are just SVG

There are no image files or client charting. The matchup's win-probability bar
is an inline `<svg>` whose fill rect's `width` is the win-percent as a number in
a `0 0 100` viewBox — a numeric SVG attribute, so it never has to pass a `%`
through `html/template`'s CSS context. Each roster headshot is likewise an
inline SVG silhouette clipped to a disk, its team colour delivered via a CSS
class keyed off `Player.TeamClass`. Swap in real assets and nothing else changes.

### Theme

A stadium-night broadcast theme: glassy panels on a dark field with faint
yard-line texture, neon-green highlights on your team and amber on the opponent,
pulsing live pills, and a full-width ticker. All CSS is inline in
`ui/league.lsx` (and `ui/lineup.lsx` for page two); both respect
`prefers-reduced-motion` and collapse to a single column on narrow screens.

## Tests

`main_test.go` drives the app through `liquidtest`: the dashboard renders all
three cards with the right row/item counts, a standings update is pushed over
SSE, and — on your team's `/team/:id` page — the seeded lineup renders, a
projection update arrives on the stream, and the live region declares
`aria-live="polite"` (D21). Further tests pin the navigation: both matchup tiles
and every standings row link to the right team's lineup, another team's page
renders its static roster, and an unknown slug shows the not-found panel. Run:

```sh
go test -race ./examples/fantasy
```
