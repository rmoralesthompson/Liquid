package field

// Field exercises the (input) and (change) value-event bindings (#104).
type Field struct {
	HydroID string
	Draft   string
}

// Selector returns the custom element tag for the component.
func (c *Field) Selector() string { return "app-field" }

// Typed handles keystrokes on the draft input; the runtime debounces (input).
func (c *Field) Typed() {}

// Committed handles the select's (change), which fires on commit.
func (c *Field) Committed() {}
