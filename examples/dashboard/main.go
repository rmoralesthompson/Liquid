// Command dashboard is the D17 example app: one page exercising every
// Liquid v0.1 subsystem — a (click) counter, an SSE-pushed live metric, a
// nested card fed by [input] bindings, a guarded route, and a CSRF-protected
// (submit) form.
//
// Build the templates, then run it:
//
//	go run ./cmd/liquid build examples/dashboard/ui
//	go run ./examples/dashboard
//
// The components live in the ui subpackage — the directory liquid build
// compiles — while this package holds the wiring that needs the generated
// Template methods to exist.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"time"

	liquid "github.com/rmoralesthompson/liquid/core"
	"github.com/rmoralesthompson/liquid/examples/dashboard/ui"
)

// adminKey guards /admin. A real app would check a session or header; the
// example uses a query key so the guard's three outcomes are one URL edit
// apart.
const adminKey = "letmein"

// adminGuard runs before the Admin component is instantiated (D4): the
// right key passes, a missing key redirects to the dashboard (D19), and a
// wrong key is denied outright.
func adminGuard(ctx liquid.Ctx) liquid.GuardResult {
	switch ctx.Query("key") {
	case adminKey:
		return liquid.Allow()
	case "":
		return liquid.Redirect("/")
	default:
		return liquid.Deny()
	}
}

// newApp wires the dashboard: the shared requests/sec subject as a service,
// the interactive cards as child components, and the two routes. Tests
// build the same app around their own subject.
func newApp(requests *liquid.BehaviorSubject[int]) (*liquid.App, error) {
	app := liquid.New()
	if err := app.Provide(requests); err != nil {
		return nil, fmt.Errorf("providing the requests subject: %w", err)
	}
	for _, child := range []liquid.Component{&ui.Counter{}, &ui.Metric{}, &ui.StatCard{}, &ui.Renamer{}} {
		if err := app.Register(child); err != nil {
			return nil, fmt.Errorf("registering %s: %w", child.Selector(), err)
		}
	}
	if err := app.Route("/", &ui.Dashboard{
		BoardName: "Production",
		StatLabel: "Deploys this week",
		StatValue: "12",
	}); err != nil {
		return nil, fmt.Errorf("routing /: %w", err)
	}
	if err := app.Route("/admin", &ui.Admin{}, liquid.WithGuard(adminGuard)); err != nil {
		return nil, fmt.Errorf("routing /admin: %w", err)
	}
	return app, nil
}

// driveMetric random-walks the requests/sec subject once a second until ctx
// is cancelled — the stand-in for a real telemetry source.
func driveMetric(ctx context.Context, requests *liquid.BehaviorSubject[int]) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			next := requests.Value() + rand.IntN(21) - 10
			if next < 0 {
				next = 0
			}
			requests.Next(next)
		}
	}
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	requests := liquid.NewBehaviorSubject(42)
	app, err := newApp(requests)
	if err != nil {
		logger.Error("wiring app", "err", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go driveMetric(ctx, requests)

	const addr = ":8080"
	logger.Info("dashboard listening", "addr", "http://localhost"+addr)
	if err := http.ListenAndServe(addr, app); err != nil {
		logger.Error("serving", "err", err)
		os.Exit(1)
	}
}
