package badsig

// Badsig is a fixture component whose (click) handler takes an argument.
type Badsig struct {
	HydroID string
	Count   int
}

// Increment has the wrong shape for a (click) handler.
func (c *Badsig) Increment(n int) { c.Count += n }
