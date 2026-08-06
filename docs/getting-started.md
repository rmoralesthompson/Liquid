# Getting started with Liquid

This guide takes you from an empty machine to a running, interactive Liquid
component you built yourself: a click counter, then a value the server pushes to
the page live over SSE. Follow it top to bottom — every command is
copy-pasteable and every snippet compiles against the current code.

Liquid is a server-driven UI framework for Go. Your components are Go structs;
their markup lives in `.lsx` files that the Liquid CLI compiles ahead of time
into your binary. The browser gets fully rendered HTML plus one small, fixed
JavaScript runtime (the same file for every app) that relays clicks back to the
server and applies the HTML patches it returns. It is not zero-JavaScript — it
is *zero application JavaScript*: you never write any.

## Prerequisites

- **Go 1.23 or newer** — check with `go version`.
- **git**.

Liquid has not published a tagged release yet, so for now you work inside a
clone of the repository and build your app as a directory within it. That is
the same layout the bundled examples use.

## Step 1 — Get Liquid and install the CLI

Clone the repo and install the `liquid` command:

```sh
git clone https://github.com/rmoralesthompson/Liquid.git
cd Liquid
go install ./cmd/liquid
```

`go install` puts a `liquid` binary in your Go bin directory. If `liquid` isn't
found afterward, add that directory to your `PATH`:

```sh
export PATH="$PATH:$(go env GOPATH)/bin"
```

Confirm the CLI is available:

```sh
liquid
# usage: liquid <build|vet|manifest|generate|dev|lsp> [args]
```

The verbs you'll use in this guide:

- `liquid generate component <name> [dir]` — scaffold a component (a paired
  struct + template).
- `liquid build [dir]` — compile the `.lsx` templates in a directory into Go.
- `liquid dev [dir]` — watch your app, recompile and restart it on every save.

`liquid vet` (static checks) and `liquid manifest` (a machine-readable map of
your components, handy for tooling and agents) round out the set.

> All the following commands are run from the repository root (the `Liquid`
> directory you just entered).

## Step 2 — Scaffold your first component

Generate a component named `app-counter` into a new `myapp/ui` directory:

```sh
liquid generate component app-counter myapp/ui
```

```
created myapp/ui/app_counter.go
created myapp/ui/app_counter.lsx
next: liquid build myapp/ui
```

Component names are custom-element tags, so they must be lowercase and contain a
hyphen (that's what lets one component nest inside another later). The generator
turns `app-counter` into the struct `AppCounter` in `app_counter.go`:

```go
package ui

// AppCounter is the app-counter component. Exported fields are template state;
// add an OnInit(ctx liquid.Ctx) error method to load them per request.
type AppCounter struct {
	// Title renders as {{ Title }} in app_counter.lsx.
	Title string
}

// Selector returns the custom-element tag this component renders as.
func (c *AppCounter) Selector() string { return "app-counter" }
```

and a matching `app_counter.lsx` template:

```html
<section>
  <h2>{{ Title }}</h2>
  <p>app-counter is ready — edit app_counter.lsx and app_counter.go, then run: liquid build myapp/ui</p>
</section>
```

The struct's **exported fields are template state**: `{{ Title }}` in the `.lsx`
resolves to the struct's `Title` field, contextually auto-escaped. The two files
are one component — a Go struct paired with its markup.

## Step 3 — Wire a minimal app

A Liquid app is an `http.Handler` you build with `liquid.New()`. Create
`myapp/main.go`:

```go
package main

import (
	"log/slog"
	"net/http"
	"os"

	liquid "github.com/rmoralesthompson/liquid/core"
	"github.com/rmoralesthompson/liquid/myapp/ui"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	app := liquid.New()
	if err := app.Route("/", &ui.AppCounter{}); err != nil {
		logger.Error("routing /", "err", err)
		os.Exit(1)
	}

	const addr = ":8080"
	logger.Info("listening", "addr", "http://localhost"+addr)
	if err := http.ListenAndServe(addr, app); err != nil {
		logger.Error("serving", "err", err)
		os.Exit(1)
	}
}
```

`app.Route("/", &ui.AppCounter{})` renders that component at `/`. Because the app
is a plain `http.Handler`, you serve it with the standard library's
`http.ListenAndServe` — Liquid picks no port for you; your app binds its own.

## Step 4 — Run it with the dev server

Start the dev loop, pointing it at your app directory:

```sh
liquid dev myapp
```

`liquid dev` compiles every `.lsx` under `myapp`, builds and starts the app, and
then watches the tree — each time you save a `.go` or `.lsx` file it recompiles
and restarts automatically. If a build fails, it keeps the last good version
running and shows the errors as an overlay in the browser.

Open <http://localhost:8080>. You'll see your scaffolded component — an empty
heading and the "app-counter is ready…" line. That is a server-rendered Liquid
component. Leave `liquid dev` running for the rest of the guide; every save
below reloads the page.

> Prefer a one-shot build? `liquid build myapp/ui` compiles the templates once
> (writing the generated `*_gen.go` files), after which `go run ./myapp` runs
> the app without the CLI. `liquid dev` just does this for you on every save.

## Step 5 — Make it interactive: a click counter

Now turn the static scaffold into a click counter. Two changes make a component
interactive:

1. A field literally named `HydroID string`. It marks the component's
   **interactive boundary** — the element the server re-renders and patches in
   place. The framework fills it in with an opaque per-session token.
2. An exported method to handle the event, bound from the template with
   `(click)="MethodName"`.

Replace `myapp/ui/app_counter.go` with:

```go
package ui

// AppCounter is an interactive click counter.
type AppCounter struct {
	// HydroID marks the component interactive; the runtime patches the
	// element carrying it.
	HydroID string
	// Count is the number of clicks so far.
	Count int
}

// Selector returns the custom-element tag this component renders as.
func (c *AppCounter) Selector() string { return "app-counter" }

// Increment handles the +1 button.
func (c *AppCounter) Increment() { c.Count++ }
```

and replace `myapp/ui/app_counter.lsx` with:

```html
<section [hydroId] id="counter">
  <h2>My first Liquid component</h2>
  <p><span id="count">{{ Count }}</span> clicks</p>
  <button id="increment" (click)="Increment">Increment</button>
</section>
```

What the directives do:

- `[hydroId]` on the root element compiles to `data-hydro-id="{{ .HydroID }}"` —
  the patch boundary. Any component that handles events or receives server
  pushes needs it.
- `(click)="Increment"` binds the button's click to the `Increment` method. At
  build time the compiler checks that `Increment` actually exists on the struct
  and adds it to the component's **action allowlist** — the server only ever
  dispatches allowlisted actions, never an arbitrary method named by the
  browser.

Save both files. `liquid dev` recompiles and restarts. Reload
<http://localhost:8080> and click **Increment** — the count goes up. Each click
is one round-trip: the browser posts the action, the server runs `Increment` on
your component, re-renders the `[hydroId]` subtree, and sends back the HTML patch
the runtime swaps in.

If you make a mistake — say you bind `(click)="Increment"` but rename the method
— `liquid dev` reports it as a structured build error instead of failing at
runtime. That compile-time feedback loop is a core part of Liquid's design.

## Step 6 — Push a live value over SSE

Clicks are user-driven. Liquid can also push updates from the *server* to the
page over Server-Sent Events (SSE), with no client polling. A component follows
an observable value — a `liquid.BehaviorSubject[T]` — and re-renders whenever
that value changes.

You'll add three things: a subject provided to the app as a shared service, a
background goroutine that advances it once a second, and a subscription on the
component that mirrors the value into template state.

First, update `myapp/ui/app_counter.go` to inject the subject and subscribe:

```go
package ui

import liquid "github.com/rmoralesthompson/liquid/core"

// AppCounter is an interactive click counter with a live server tick.
type AppCounter struct {
	// HydroID marks the component interactive; the runtime patches the
	// element carrying it.
	HydroID string
	// Count is the number of clicks so far.
	Count int
	// Ticks is the app-lifetime subject that drives the live value; the
	// framework injects it by type.
	Ticks *liquid.BehaviorSubject[int] `inject:""`
	// Elapsed is the latest value pushed from Ticks.
	Elapsed int
}

// Selector returns the custom-element tag this component renders as.
func (c *AppCounter) Selector() string { return "app-counter" }

// Increment handles the +1 button.
func (c *AppCounter) Increment() { c.Count++ }

// Subscriptions declares the live binding: every emission from Ticks updates
// Elapsed and pushes a re-render over the session's SSE stream. The framework
// activates the subscription after render and cancels it when the session ends,
// so you never manage its lifecycle by hand.
func (c *AppCounter) Subscriptions() []liquid.Subscription {
	return []liquid.Subscription{
		liquid.Observe(c.Ticks, func(v int) { c.Elapsed = v }),
	}
}
```

Add the live value to `myapp/ui/app_counter.lsx`. Marking the region
`aria-live="polite"` lets assistive technology announce the pushed change:

```html
<section [hydroId] id="counter" aria-live="polite">
  <h2>My first Liquid component</h2>
  <p><span id="count">{{ Count }}</span> clicks</p>
  <button id="increment" (click)="Increment">Increment</button>
  <p>Server tick: <span id="elapsed">{{ Elapsed }}</span> (pushed live over SSE)</p>
</section>
```

Finally, create the subject in `main.go`, register it with `app.Provide`, and
drive it from a goroutine. The full `myapp/main.go`:

```go
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"

	liquid "github.com/rmoralesthompson/liquid/core"
	"github.com/rmoralesthompson/liquid/myapp/ui"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	// The shared value the component follows. Providing it lets the framework
	// inject it into any component with a matching `inject:""` field.
	ticks := liquid.NewBehaviorSubject(0)

	app := liquid.New()
	if err := app.Provide(ticks); err != nil {
		logger.Error("providing ticks", "err", err)
		os.Exit(1)
	}
	if err := app.Route("/", &ui.AppCounter{}); err != nil {
		logger.Error("routing /", "err", err)
		os.Exit(1)
	}

	// Advance the subject once a second; each Next() pushes to every
	// subscribed page.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				ticks.Next(ticks.Value() + 1)
			}
		}
	}()

	const addr = ":8080"
	logger.Info("listening", "addr", "http://localhost"+addr)
	if err := http.ListenAndServe(addr, app); err != nil {
		logger.Error("serving", "err", err)
		os.Exit(1)
	}
}
```

Save everything. Reload <http://localhost:8080>: the "Server tick" number now
climbs on its own, once a second, with no interaction — pushed from the server
over SSE — while the click counter still works independently. `ticks.Next(...)`
emits a new value, the component's subscription copies it into `Elapsed`, and the
framework re-renders and patches the page.

That's the whole loop: user events in over fetch, server-driven updates out over
SSE, both patching the same `[hydroId]` boundary.

## What you built

- A Go struct + `.lsx` component, scaffolded with `liquid generate`.
- An app served as a standard `http.Handler` via `liquid.New()` and `app.Route`.
- Interactivity through a `HydroID` boundary and a compile-time-checked
  `(click)` action allowlist.
- A live value pushed from the server with `BehaviorSubject` + `Subscriptions`,
  streamed over SSE.

## Next steps

- **Explore fuller examples.** [`examples/dashboard`](../examples/dashboard) is a
  single page exercising every v0.1 subsystem — the counter and SSE metric you
  just built, plus a live market ticker, a server-rendered SVG chart, nested
  components fed by `[input]` bindings, a guarded route, and a CSRF-protected
  form. [`examples/fantasy`](../examples/fantasy) is a second app in the same
  style. Both are runnable: `liquid dev examples/dashboard`.
- **Learn the template directives.** The
  [`.lsx` syntax reference](template-syntax.md) covers `*goIf`, `*goFor`,
  parent→child `[input]` bindings, forms with automatic CSRF, and deferred
  rendering.
- **Understand the model.** The [architecture spec](architecture.md) explains
  the rendering pipeline, hydro sessions, dependency injection, and the security
  model in depth.
