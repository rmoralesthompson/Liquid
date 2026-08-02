package compiler

import (
	"bytes"
	"strings"
)

// refKind classifies what a template reference must resolve to on the paired
// struct, so vet can apply the right rule.
type refKind int

const (
	// refField is any field-or-method reference: interpolations, *goIf
	// conditions, *goFor lists.
	refField refKind = iota
	// refAction is an event-handler reference, which must resolve to a
	// dispatchable method (D11).
	refAction
	// refHydroRoot is a [hydroId] declaration, which requires the struct to
	// carry the HydroID string field the framework fills.
	refHydroRoot
)

// interpolation is one struct reference found in raw .lsx source — a
// {{ ... }} token or a directive/binding expression — positioned at the first
// byte of its expression (1-based line, 1-based byte column) so a diagnostic
// points at the identifier an agent should edit, not at the braces.
type interpolation struct {
	expr string
	line int
	col  int
	kind refKind
}

// scanInterpolations walks raw .lsx source for {{ ... }} tokens, recording
// each token's trimmed inner expression and position. Positions refer to the
// original source text, before HTML parsing, so diagnostics point at what the
// author actually typed. An opening {{ with no matching }} yields an LSX001
// diagnostic and ends the scan, since everything after it is ambiguous.
func scanInterpolations(file string, src []byte) ([]interpolation, []Diagnostic) {
	var interps []interpolation
	c := &cursor{src: src, line: 1, col: 1}
	for !c.done() {
		if !c.hasPrefix("{{") {
			c.advance(1)
			continue
		}
		innerLen := bytes.Index(c.src[c.i+2:], []byte("}}"))
		if innerLen < 0 {
			return interps, []Diagnostic{{
				File:       file,
				Line:       c.line,
				Col:        c.col,
				Severity:   SeverityError,
				Code:       CodeMalformedTemplate,
				Message:    "unclosed interpolation: {{ has no matching }}",
				Suggestion: "close the interpolation with }}",
			}}
		}
		expr := string(c.src[c.i+2 : c.i+2+innerLen])
		end := c.i + innerLen + 4
		// Position the expression at its first non-whitespace byte, so the
		// diagnostic points at the identifier, not the braces or padding.
		// skipSpace cannot overrun the closing braces: } is not whitespace.
		c.advance(2)
		c.skipSpace()
		interps = append(interps, interpolation{
			expr: strings.TrimSpace(expr),
			line: c.line,
			col:  c.col,
		})
		c.advance(end - c.i)
	}
	return interps, nil
}
