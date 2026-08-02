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
	// refCSRFRoot is a <form> element, which requires the struct to carry
	// the CSRFToken string field the framework fills (D15).
	refCSRFRoot
	// refChildTag is a child-selector element, which must name a component
	// declared in the package (D14).
	refChildTag
	// refInput is an [input] binding on a child-selector element: the
	// expression references the parent struct, and the bound name must be an
	// assignable field on the child struct.
	refInput
)

// pos is a position in raw .lsx source: 1-based line, 1-based byte column,
// pointing at what the author typed so diagnostics land on it exactly.
type pos struct {
	line int
	col  int
}

// structRef is one reference a template makes against the paired struct —
// a {{ ... }} interpolation, a directive/binding expression, or a tag-level
// field requirement ([hydroId], <form>) — positioned at the first byte of
// its expression so a diagnostic points at the identifier an agent should
// edit, not at the braces.
type structRef struct {
	expr string
	pos
	kind refKind
	// binding is the canonical spelling of the event binding a refAction
	// came from — (click), (submit) — or the bound child field name of a
	// refInput, so diagnostics name what the author typed.
	binding string
	// sel is the child selector a refInput binds into.
	sel string
	// namePos is a refInput's attribute-name position, where diagnostics
	// about the bound child field (rather than the expression) anchor.
	namePos pos
}

// scanInterpolations walks raw .lsx source for {{ ... }} tokens, recording
// each token's trimmed inner expression and position. Positions refer to the
// original source text, before HTML parsing, so diagnostics point at what the
// author actually typed. An opening {{ with no matching }} yields an LSX001
// diagnostic and ends the scan, since everything after it is ambiguous.
func scanInterpolations(file string, src []byte) ([]structRef, []Diagnostic) {
	var interps []structRef
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
		interps = append(interps, structRef{
			expr: strings.TrimSpace(expr),
			pos:  pos{line: c.line, col: c.col},
		})
		c.advance(end - c.i)
	}
	return interps, nil
}
