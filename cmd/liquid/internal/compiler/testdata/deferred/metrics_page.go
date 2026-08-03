package deferred

// MetricsPage is a fixture parent deferring a slow child.
type MetricsPage struct {
	Title string
	Days  string
}

// Selector returns the custom element tag for the component.
func (c *MetricsPage) Selector() string { return "app-metrics-page" }
