package strayclick

// Strayclick is a fixture component binding (click) without a [hydroId]
// patch root.
type Strayclick struct {
	Count int
}

// Poke handles the button.
func (c *Strayclick) Poke() { c.Count++ }
