package brokengo

// Brokengo is a fixture whose paired Go package fails type-checking.
type Brokengo struct {
	Name string
}

var _ = undefinedSymbol
