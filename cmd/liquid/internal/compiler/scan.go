package compiler

import (
	"bytes"
	"strings"
)

// interpolation is one {{ ... }} token found in raw .lsx source, positioned
// at the first byte of its inner expression (1-based line, 1-based byte
// column) so a diagnostic points at the identifier an agent should edit, not
// at the braces.
type interpolation struct {
	expr string
	line int
	col  int
}

// scanInterpolations walks raw .lsx source for {{ ... }} tokens, recording
// each token's trimmed inner expression and position. Positions refer to the
// original source text, before HTML parsing, so diagnostics point at what the
// author actually typed. An opening {{ with no matching }} yields an LSX001
// diagnostic and ends the scan, since everything after it is ambiguous.
func scanInterpolations(file string, src []byte) ([]interpolation, []Diagnostic) {
	var interps []interpolation
	line, col := 1, 1
	for i := 0; i < len(src); {
		if src[i] == '\n' {
			line++
			col = 1
			i++
			continue
		}
		if !bytes.HasPrefix(src[i:], []byte("{{")) {
			col++
			i++
			continue
		}
		innerLen := bytes.Index(src[i+2:], []byte("}}"))
		if innerLen < 0 {
			return interps, []Diagnostic{{
				File:       file,
				Line:       line,
				Col:        col,
				Severity:   SeverityError,
				Code:       CodeMalformedTemplate,
				Message:    "unclosed interpolation: {{ has no matching }}",
				Suggestion: "close the interpolation with }}",
			}}
		}
		exprLine, exprCol := line, col+2
		for _, b := range src[i+2 : i+2+innerLen] {
			if b == '\n' {
				exprLine++
				exprCol = 1
				continue
			}
			if b == ' ' || b == '\t' || b == '\r' {
				exprCol++
				continue
			}
			break
		}
		interps = append(interps, interpolation{
			expr: strings.TrimSpace(string(src[i+2 : i+2+innerLen])),
			line: exprLine,
			col:  exprCol,
		})
		for range src[i : i+innerLen+4] {
			if src[i] == '\n' {
				line++
				col = 1
			} else {
				col++
			}
			i++
		}
	}
	return interps, nil
}
