package compiler_test

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/rmoralesthompson/liquid/cmd/liquid/internal/compiler"
)

// vet runs compiler.Vet and fails the test on a mechanical (non-diagnostic)
// error, returning any diagnostics for the caller to assert on.
func vet(t *testing.T, dir string) []compiler.Diagnostic {
	t.Helper()
	diags, err := compiler.Vet(context.Background(), dir)
	if err != nil {
		t.Fatalf("Vet: %v", err)
	}
	return diags
}

// TestVetFlagsBareSubscribeAsLeak covers the D29 provable-leak case: a direct
// Subscribe call whose cancel is discarded (used as a bare statement) can
// never be released, so it is a build error.
func TestVetFlagsBareSubscribeAsLeak(t *testing.T) {
	dir := copyFixture(t, "leaks")

	diags := vet(t, dir)

	want := []compiler.Diagnostic{{
		File:       filepath.Join(dir, "leaky.go"),
		Line:       19,
		Col:        11,
		Severity:   compiler.SeverityError,
		Code:       "LSX017",
		Message:    "this Subscribe call discards its cancel, so the subscription is never released when the session ends",
		Suggestion: "declare the subscription with liquid.Observe inside Subscriptions() so the framework cancels it on session GC (D25)",
	}}
	if !reflect.DeepEqual(diags, want) {
		t.Errorf("diagnostics = %+v, want %+v", diags, want)
	}
}

// TestVetFlagsDerivedCombinatorSubscription covers the combinator arm of D29:
// a bare Subscribe on a derived value (liquid.Map, D25) is a provable leak just
// like a subscription to the underlying subject.
func TestVetFlagsDerivedCombinatorSubscription(t *testing.T) {
	dir := copyFixture(t, "derived")

	want := []compiler.Diagnostic{{
		File:       filepath.Join(dir, "rollup.go"),
		Line:       21,
		Col:        8,
		Severity:   compiler.SeverityError,
		Code:       "LSX017",
		Message:    "this Subscribe call discards its cancel, so the subscription is never released when the session ends",
		Suggestion: "declare the subscription with liquid.Observe inside Subscriptions() so the framework cancels it on session GC (D25)",
	}}
	if diags := vet(t, dir); !reflect.DeepEqual(diags, want) {
		t.Errorf("diagnostics = %+v, want %+v", diags, want)
	}
}

// TestVetWarnsOnCapturedSubscription covers the escalation boundary: a direct
// Subscribe whose cancel is captured is still outside the managed path, but
// the framework cannot prove it leaks, so it is a warning, not an error.
func TestVetWarnsOnCapturedSubscription(t *testing.T) {
	dir := copyFixture(t, "captured")

	diags := vet(t, dir)

	want := []compiler.Diagnostic{{
		File:       filepath.Join(dir, "captured.go"),
		Line:       21,
		Col:        41,
		Severity:   compiler.SeverityWarning,
		Code:       "LSX017",
		Message:    "this Subscribe call is not tied to the session lifecycle, so the subscription risks leaking when the session ends",
		Suggestion: "declare the subscription with liquid.Observe inside Subscriptions() so the framework cancels it on session GC (D25)",
	}}
	if !reflect.DeepEqual(diags, want) {
		t.Errorf("diagnostics = %+v, want %+v", diags, want)
	}
}

// TestVetLeavesManagedSubscriptionAlone covers the false-positive floor: a
// component that follows a subject through liquid.Observe inside
// Subscriptions() owns no subscription lifecycle itself, so the D29 check must
// stay silent.
func TestVetLeavesManagedSubscriptionAlone(t *testing.T) {
	dir := copyFixture(t, "managed")

	if diags := vet(t, dir); len(diags) != 0 {
		t.Errorf("managed subscription produced diagnostics: %+v", diags)
	}
}

// TestVetSuppressesSubscriptionWithDirective covers the escape hatch: an
// //liquid:allow-subscribe comment on the call's line, or the line above,
// silences the D29 diagnostic — the false positive the ticket keeps rare and
// suppressible.
func TestVetSuppressesSubscriptionWithDirective(t *testing.T) {
	dir := copyFixture(t, "allowsub")

	if diags := vet(t, dir); len(diags) != 0 {
		t.Errorf("suppressed subscriptions produced diagnostics: %+v", diags)
	}
}
