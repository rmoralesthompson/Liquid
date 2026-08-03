package deferred

// SlowStats is a fixture deferred child; HydroID is the patch boundary the
// deferred swap targets.
type SlowStats struct {
	HydroID string
	Range   string
}

// Selector returns the custom element tag for the component.
func (c *SlowStats) Selector() string { return "app-slow-stats" }
