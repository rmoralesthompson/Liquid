package clicktypo

// Clicktypo is a fixture component whose (click) misspells its handler.
type Clicktypo struct {
	HydroID string
	Count   int
}

// Increment handles the +1 button.
func (c *Clicktypo) Increment() { c.Count++ }
