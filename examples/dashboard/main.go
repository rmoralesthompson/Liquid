// Command dashboard is the D17 example app: one page exercising every
// Liquid v0.1 subsystem — a (click) counter, an SSE-pushed live metric, a
// nested card fed by [input] bindings, a guarded route, and a CSRF-protected
// (submit) form — plus a live market ticker and a server-rendered SVG chart,
// both pushed over SSE from shared subjects.
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
	"math"
	"math/rand/v2"
	"os"
	"os/signal"
	"syscall"
	"time"

	liquid "github.com/rmoralesthompson/liquid/core"
	"github.com/rmoralesthompson/liquid/examples/dashboard/ui"
)

// adminKey guards /admin. A real app would check a session or header; the
// example uses a query key so the guard's three outcomes are one URL edit
// apart.
const adminKey = "letmein"

// seriesWindow is how many points the portfolio chart keeps.
const seriesWindow = 48

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

// asset is one raw ticker instrument: its live price and the session's opening
// price, from which the displayed percentage change is derived.
type asset struct {
	symbol      string
	price, open float64
}

// seedAssets is the ticker's starting book — fixed so the first render (and
// the tests) are deterministic; the feed walks these once it starts.
func seedAssets() []asset {
	return []asset{
		{"BTC", 42318.20, 41800},
		{"ETH", 3184.55, 3210},
		{"SOL", 168.42, 160},
		{"XRP", 0.6231, 0.61},
		{"ADA", 0.5847, 0.60},
	}
}

// quotesOf renders the raw book into display quotes.
func quotesOf(assets []asset) []ui.Quote {
	out := make([]ui.Quote, len(assets))
	for i, a := range assets {
		change := (a.price - a.open) / a.open * 100
		out[i] = ui.MakeQuote(a.symbol, a.price, change)
	}
	return out
}

// seedSeries builds the chart's starting window deterministically: a gentle
// uptrend with a little wobble, ending near the screenshot's P&L figure.
func seedSeries() []float64 {
	const start, end = 100823.39, 127815.20
	out := make([]float64, seriesWindow)
	for i := range out {
		t := float64(i) / float64(seriesWindow-1)
		out[i] = start + t*(end-start) + math.Sin(float64(i)*0.7)*1500
	}
	return out
}

// newApp wires the dashboard: the shared subjects as services, the interactive
// cards as child components, and the two routes. Tests build the same app
// around their own subjects.
func newApp(
	requests *liquid.BehaviorSubject[int],
	market *liquid.BehaviorSubject[[]ui.Quote],
	series *liquid.BehaviorSubject[[]float64],
) (*liquid.App, error) {
	app := liquid.New()
	for _, svc := range []any{requests, market, series} {
		if err := app.Provide(svc); err != nil {
			return nil, fmt.Errorf("providing %T: %w", svc, err)
		}
	}
	for _, child := range []liquid.Component{
		&ui.Counter{}, &ui.Metric{}, &ui.StatCard{}, &ui.Renamer{}, &ui.Ticker{}, &ui.Chart{},
	} {
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

// driveMarket random-walks each instrument once a second and republishes the
// book — the stand-in for a real market data feed.
func driveMarket(ctx context.Context, market *liquid.BehaviorSubject[[]ui.Quote], assets []asset) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			for i := range assets {
				assets[i].price *= 1 + (rand.Float64()*2-1)*0.01
			}
			market.Next(quotesOf(assets))
		}
	}
}

// driveSeries appends a new portfolio value once a second, keeping a rolling
// window — the stand-in for a real portfolio valuation stream.
func driveSeries(ctx context.Context, series *liquid.BehaviorSubject[[]float64]) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			window := series.Value()
			last := window[len(window)-1]
			next := last * (1 + (rand.Float64()*2-1)*0.012)
			window = append(window[1:], next)
			series.Next(window)
		}
	}
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	requests := liquid.NewBehaviorSubject(42)
	assets := seedAssets()
	market := liquid.NewBehaviorSubject(quotesOf(assets))
	series := liquid.NewBehaviorSubject(seedSeries())
	app, err := newApp(requests, market, series)
	if err != nil {
		logger.Error("wiring app", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go driveMetric(ctx, requests)
	go driveMarket(ctx, market, assets)
	go driveSeries(ctx, series)

	const addr = ":8080"
	logger.Info("dashboard listening", "addr", "http://localhost"+addr)
	// Serve applies production timeouts and drains live SSE streams on
	// SIGINT/SIGTERM, so a deploy does not sever sessions abruptly.
	if err := app.Serve(ctx, liquid.ServeConfig{Addr: addr}); err != nil {
		logger.Error("serving", "err", err)
		os.Exit(1)
	}
	logger.Info("dashboard stopped")
}
