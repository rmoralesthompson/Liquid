package liquidtest_test

import (
	"testing"

	liquid "github.com/rmoralesthompson/liquid/core"
	"github.com/rmoralesthompson/liquid/liquidtest"
)

// gate exercises the D30 payload contract end to end through the harness: Set
// carries a closed-domain Mode and a guarded Amount, hand-written the way
// liquid build emits the contract (Actions, PayloadDomains, and a <Name>Guard).
type gate struct {
	HydroID string
	Mode    string
	Amount  int
}

func (c *gate) Selector() string { return "app-gate" }

func (c *gate) Template() string {
	return `<div data-hydro-id="{{ .HydroID }}">` +
		`<span id="mode">{{ .Mode }}</span><span id="amount">{{ .Amount }}</span></div>`
}

// setPayload is the shape Set binds and SetGuard checks.
type setPayload struct {
	Mode   string
	Amount int
}

// Set trusts the seam to have refused an out-of-domain Mode or a non-positive
// Amount before it runs (D30).
func (c *gate) Set(e liquid.Event) {
	var p setPayload
	_ = e.Bind(&p)
	c.Mode, c.Amount = p.Mode, p.Amount
}

// SetGuard is the D30 boundary guard: a non-positive amount never reaches Set.
func (c *gate) SetGuard(p setPayload) bool { return p.Amount > 0 }

// Actions mirrors the compiler-generated allowlist (D10).
func (c *gate) Actions() []string { return []string{"Set"} }

// PayloadDomains mirrors the compiler-generated closed-domain contract (D30):
// Set's Mode field admits only "off" or "on".
func (c *gate) PayloadDomains() map[string]map[string][]string {
	return map[string]map[string][]string{"Set": {"mode": {"off", "on"}}}
}

func newGateHarness(t *testing.T) *liquidtest.Harness {
	t.Helper()
	app := liquid.New()
	if err := app.Route("/", &gate{}); err != nil {
		t.Fatalf("Route: %v", err)
	}
	return liquidtest.New(t, app)
}

func TestPayloadContractAdmitsAValidGuardedEvent(t *testing.T) {
	page := newGateHarness(t).Get("/")

	patch := page.Fire("Set",
		liquidtest.Field("mode", "on"),
		liquidtest.Field("amount", "5"),
	)

	if patch.Code != 200 {
		t.Fatalf("Code = %d, want 200 for an in-domain, guard-passing payload", patch.Code)
	}
	if got := patch.Text("#amount"); got != "5" {
		t.Errorf(`patch Text("#amount") = %q, want "5"`, got)
	}
	if got := patch.Text("#mode"); got != "on" {
		t.Errorf(`patch Text("#mode") = %q, want "on"`, got)
	}
}

func TestPayloadContractRefusesOutOfDomainValue(t *testing.T) {
	page := newGateHarness(t).Get("/")

	patch := page.Fire("Set",
		liquidtest.Field("mode", "sideways"),
		liquidtest.Field("amount", "5"),
	)

	if patch.Code != 400 {
		t.Errorf("Code = %d, want 400 for an out-of-domain closed-domain value (D30)", patch.Code)
	}
}

func TestPayloadContractRefusesGuardRejection(t *testing.T) {
	page := newGateHarness(t).Get("/")

	patch := page.Fire("Set",
		liquidtest.Field("mode", "on"),
		liquidtest.Field("amount", "0"),
	)

	if patch.Code != 400 {
		t.Errorf("Code = %d, want 400 for a payload the guard rejects (D30)", patch.Code)
	}
}
