package liquid_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	liquid "github.com/rmoralesthompson/liquid/core"
)

// The Fanout mechanics (cancellation, wait-for-all, timeouts, panics) are
// white-box tested in fanout_test.go; this file pins the one wire-visible
// behavior: a component whose OnInit fans out renders like any other, and a
// failed source takes the OnInit-error path.

type fanDashboard struct {
	Sales   int
	Tickets string
}

func (d *fanDashboard) Selector() string { return "app-fan-dashboard" }

func (d *fanDashboard) Template() string {
	return "<p>sales={{ .Sales }} tickets={{ .Tickets }}</p>"
}

func (d *fanDashboard) OnInit(ctx liquid.Ctx) error {
	err := ctx.Fanout(
		liquid.Load(&d.Sales, func(ctx context.Context) (int, error) { return 7, nil }),
		liquid.Load(&d.Tickets, func(ctx context.Context) (string, error) { return "3 open", nil }),
	)
	if err != nil {
		return fmt.Errorf("loading dashboard: %w", err)
	}
	return nil
}

func TestFanoutComponentRendersOverHTTP(t *testing.T) {
	srv := newServer(t, "/dash", &fanDashboard{})

	resp, body := get(t, srv.URL+"/dash")

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %q)", resp.StatusCode, http.StatusOK, body)
	}
	if want := "<p>sales=7 tickets=3 open</p>"; !strings.Contains(body, want) {
		t.Errorf("body = %q, want it to contain %q", body, want)
	}
}

type fanFailing struct {
	Sales int
}

func (d *fanFailing) Selector() string { return "app-fan-failing" }

func (d *fanFailing) Template() string { return neverRenderedHTML }

func (d *fanFailing) OnInit(ctx liquid.Ctx) error {
	err := ctx.Fanout(
		liquid.Load(&d.Sales, func(ctx context.Context) (int, error) {
			return 0, errors.New("warehouse db down")
		}),
	)
	if err != nil {
		return fmt.Errorf("loading dashboard: %w", err)
	}
	return nil
}

func TestFanoutSourceErrorTakesTheOnInitErrorPath(t *testing.T) {
	srv := newServer(t, "/dash", &fanFailing{})

	resp, body := get(t, srv.URL+"/dash")

	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusInternalServerError)
	}
	if strings.Contains(body, "warehouse db down") || strings.Contains(body, neverRenderedHTML) {
		t.Errorf("body = %q, must leak neither the loader error nor the template", body)
	}
}
