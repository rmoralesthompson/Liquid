package typedform

// Plan is a closed-domain enum (D30): a named type backed by a typed const set.
type Plan string

// The plans a signup admits.
const (
	Free Plan = "free"
	Pro  Plan = "pro"
)

// SignupForm is Submit's typed payload (#105, ADR-0004). Because the handler
// names it, its closed-domain Plan field enforces at the seam without a guard —
// the resolution of #85.
type SignupForm struct {
	Email string
	Plan  Plan
}

// Signup collects a signup through a typed-payload submit.
type Signup struct {
	HydroID   string
	CSRFToken string
}

// Selector returns the custom element tag for the component.
func (c *Signup) Selector() string { return "app-signup" }

// Submit is a typed-payload handler: the seam binds the form into SignupForm,
// enforces Plan's closed domain, and (if present) runs SignupForm.Validate.
func (c *Signup) Submit(f SignupForm) { _ = f }
