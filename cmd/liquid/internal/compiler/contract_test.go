package compiler_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rmoralesthompson/liquid/cmd/liquid/internal/compiler"
)

func TestBuildEmitsPayloadDomainsFromActionGuard(t *testing.T) {
	dir := copyFixture(t, "mover")

	diags := build(t, dir)

	for _, d := range diags {
		if d.Severity == compiler.SeverityError {
			t.Fatalf("unexpected error diagnostic building mover: %+v", d)
		}
	}
	gen, err := os.ReadFile(filepath.Join(dir, "mover_gen.go"))
	if err != nil {
		t.Fatalf("expected mover_gen.go beside the source: %v", err)
	}
	got := string(gen)
	// The guard's MovePayload.Dir field is a Direction const-set, so the seam
	// contract enumerates it; Step is an unbounded int and carries no domain.
	for _, want := range []string{
		"func (c *Mover) PayloadDomains() map[string]map[string][]string {",
		`"Move": {`,
		`"dir": {"down", "up"}`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("mover_gen.go missing %q\n--- generated ---\n%s", want, got)
		}
	}
	if strings.Contains(got, `"step"`) {
		t.Errorf("mover_gen.go constrains step, but an unbounded int is no closed domain\n--- generated ---\n%s", got)
	}
}

func TestVetWarnsUnguardedPayloadAction(t *testing.T) {
	dir := copyFixture(t, "renamer")

	diags := vet(t, dir)

	want := []compiler.Diagnostic{{
		File:       filepath.Join(dir, "renamer.go"),
		Line:       16,
		Col:        19,
		Severity:   compiler.SeverityWarning,
		Code:       "LSX018",
		Message:    "action Rename takes an untyped client payload but declares no guard, so nothing constrains its payload values at the dispatch seam (D30)",
		Suggestion: "give Rename a typed payload parameter — func (c *Renamer) Rename(p <Payload>) — so its closed-domain fields enforce and a Validate runs, or add func (c *Renamer) RenameGuard(p <Payload>) bool (D30, ADR-0004)",
	}}
	if !reflect.DeepEqual(diags, want) {
		t.Errorf("diagnostics = %+v, want %+v", diags, want)
	}
}

// TestUnguardedClosedDomainIsNotEnforced pins the residual #85 gap after
// ADR-0004: an untyped func(e liquid.Event) handler still names no payload type
// to the compiler, so a closed-domain enum it carries is neither enumerated into
// PayloadDomains nor enforced without a guard — the author gets LSX018. The fix
// ADR-0004 provides is a typed payload parameter (proven by
// TestTypedPayloadHandlerEnforcesClosedDomainWithoutGuard); this Event handler
// does not adopt one, so the coupling persists for it.
func TestUnguardedClosedDomainIsNotEnforced(t *testing.T) {
	dir := copyFixture(t, "prefs")

	// Build: prefs declares Level (a closed const-set) and a SetPriorityPayload,
	// but SetPriority has no guard, so nothing associates that payload with the
	// action. No PayloadDomains is generated, and "low"/"high" are not enforced.
	for _, d := range build(t, dir) {
		if d.Severity == compiler.SeverityError {
			t.Fatalf("unexpected error diagnostic building prefs: %+v", d)
		}
	}
	if gen, err := os.ReadFile(filepath.Join(dir, "prefs_gen.go")); err == nil {
		got := string(gen)
		if strings.Contains(got, "PayloadDomains") {
			t.Errorf("prefs_gen.go generated PayloadDomains for an unguarded action; the closed domain should be invisible without a guard\n--- generated ---\n%s", got)
		}
		for _, leak := range []string{`"low"`, `"high"`, `"level"`} {
			if strings.Contains(got, leak) {
				t.Errorf("prefs_gen.go enumerated %s, but an unguarded closed domain must not be enforced\n--- generated ---\n%s", leak, got)
			}
		}
	}

	// Vet: the author's only signal is the generic unguarded-action warning,
	// whose sharpened suggestion now names the closed-domain coupling.
	want := []compiler.Diagnostic{{
		File:       filepath.Join(dir, "prefs.go"),
		Line:       38,
		Col:        17,
		Severity:   compiler.SeverityWarning,
		Code:       "LSX018",
		Message:    "action SetPriority takes an untyped client payload but declares no guard, so nothing constrains its payload values at the dispatch seam (D30)",
		Suggestion: "give SetPriority a typed payload parameter — func (c *Prefs) SetPriority(p <Payload>) — so its closed-domain fields enforce and a Validate runs, or add func (c *Prefs) SetPriorityGuard(p <Payload>) bool (D30, ADR-0004)",
	}}
	if diags := vet(t, dir); !reflect.DeepEqual(diags, want) {
		t.Errorf("diagnostics = %+v, want %+v", diags, want)
	}
}

func TestVetSilentOnGuardedPayloadAction(t *testing.T) {
	dir := copyFixture(t, "mover")

	// Mover's Move declares a MoveGuard, so the payload axis is covered and the
	// unguarded-action warning must not fire (D30).
	for _, d := range vet(t, dir) {
		if d.Code == "LSX018" {
			t.Errorf("unexpected LSX018 on a guarded action: %+v", d)
		}
	}
}

func TestManifestSurfacesGuardAndClosedDomains(t *testing.T) {
	graph := manifestOf(t, "mover")

	if len(graph.Components) != 1 {
		t.Fatalf("components = %d, want 1", len(graph.Components))
	}
	wantActions := []compiler.ManifestAction{{
		Name:          "Move",
		Signature:     "func(e liquid.Event)",
		TakesEvent:    true,
		Events:        []string{"click"},
		Guard:         true,
		ClosedDomains: map[string][]string{"Dir": {"down", "up"}},
	}}
	if !reflect.DeepEqual(graph.Components[0].Actions, wantActions) {
		t.Errorf("Actions = %+v, want %+v", graph.Components[0].Actions, wantActions)
	}
}

// TestTypedPayloadHandlerEnforcesClosedDomainWithoutGuard proves the #105 /
// ADR-0004 resolution of #85: a closed-domain field on a typed-payload handler
// is enumerated into PayloadDomains and enforced at the seam without a guard,
// and the handler draws neither LSX008 (it is a valid shape) nor LSX018 (it is
// not unconstrained).
func TestTypedPayloadHandlerEnforcesClosedDomainWithoutGuard(t *testing.T) {
	dir := copyFixture(t, "typedform")

	for _, d := range build(t, dir) {
		if d.Severity == compiler.SeverityError {
			t.Fatalf("unexpected error diagnostic: %+v", d)
		}
		if d.Code == "LSX018" {
			t.Errorf("a typed-payload handler must not draw the unguarded-action warning: %+v", d)
		}
	}

	gen, err := os.ReadFile(filepath.Join(dir, "signup_gen.go"))
	if err != nil {
		t.Fatalf("expected signup_gen.go: %v", err)
	}
	got := string(gen)
	if !strings.Contains(got, "func (c *Signup) PayloadDomains()") {
		t.Errorf("no PayloadDomains generated; a typed payload's closed domain must enforce without a guard\n--- generated ---\n%s", got)
	}
	for _, want := range []string{`"Submit"`, `"plan"`, `"free"`, `"pro"`} {
		if !strings.Contains(got, want) {
			t.Errorf("PayloadDomains missing %q\n--- generated ---\n%s", want, got)
		}
	}
}
