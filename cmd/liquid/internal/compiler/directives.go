package compiler

import (
	"bytes"
	"fmt"
	"strings"
)

// directive is one structural directive attribute found in raw .lsx source,
// positioned at the first byte of its expression (1-based line, 1-based byte
// column) so a diagnostic points at what the author should edit.
type directive struct {
	name string // canonical spelling: *goIf or *goFor
	expr string
	line int
	col  int
	tag  int // ordinal of the enclosing < tag, for the one-per-element rule
}

// cursor walks a byte slice tracking the 1-based line and byte column of the
// current position, so scans over raw source can report positions matching
// what the author typed.
type cursor struct {
	src  []byte
	i    int
	line int
	col  int
}

func (c *cursor) done() bool { return c.i >= len(c.src) }

func (c *cursor) peek() byte { return c.src[c.i] }

// advance moves the cursor n bytes forward, stopping at end of input.
func (c *cursor) advance(n int) {
	for ; n > 0 && c.i < len(c.src); n-- {
		if c.src[c.i] == '\n' {
			c.line++
			c.col = 1
		} else {
			c.col++
		}
		c.i++
	}
}

func (c *cursor) hasPrefix(s string) bool {
	return bytes.HasPrefix(c.src[c.i:], []byte(s))
}

func (c *cursor) foldPrefix(s string) bool {
	return hasFoldPrefix(c.src[c.i:], s)
}

// skipPast advances just beyond the next occurrence of s, or to the end of
// input when s never occurs again.
func (c *cursor) skipPast(s string) {
	if j := bytes.Index(c.src[c.i:], []byte(s)); j >= 0 {
		c.advance(j + len(s))
		return
	}
	c.advance(len(c.src) - c.i)
}

// skipSpace advances past any run of whitespace.
func (c *cursor) skipSpace() {
	for !c.done() {
		switch c.peek() {
		case ' ', '\t', '\r', '\n':
			c.advance(1)
		default:
			return
		}
	}
}

// boundaryAt reports whether the byte n ahead of the cursor ends an HTML
// attribute name (=, whitespace, /, > or end of input), so *goIf never
// matches inside a longer attribute name.
func (c *cursor) boundaryAt(n int) bool {
	if c.i+n >= len(c.src) {
		return true
	}
	switch c.src[c.i+n] {
	case '=', ' ', '\t', '\r', '\n', '/', '>':
		return true
	}
	return false
}

// scanDirectives walks raw .lsx source for structural directive attributes,
// recording each one's trimmed expression and position. Like
// scanInterpolations, it reads the original text — before HTML parsing — so
// positions match what the author typed. It follows the attribute syntax the
// HTML parser accepts: directives are recognized only inside a tag (never in
// text or comments), whitespace is allowed around =, and values may be
// quoted, unquoted, or absent. An unterminated quoted value ends the scan;
// the HTML parser's tolerance decides what such input means.
func scanDirectives(src []byte) []directive {
	var dirs []directive
	c := &cursor{src: src, line: 1, col: 1}
	tag, inTag := 0, false
	for !c.done() {
		if !inTag {
			if c.hasPrefix("<!--") {
				c.skipPast("-->")
				continue
			}
			if c.peek() == '<' {
				inTag = true
				tag++
			}
			c.advance(1)
			continue
		}
		switch {
		case c.peek() == '>':
			inTag = false
			c.advance(1)
		case c.peek() == '"', c.peek() == '\'':
			// A quoted value of some other attribute; < and > inside it do
			// not open or close tags.
			q := c.peek()
			c.advance(1)
			c.skipPast(string(q))
		case c.foldPrefix("*goif") && c.boundaryAt(len("*goif")):
			d, ok := c.scanDirectiveValue("*goIf", tag)
			if !ok {
				return dirs
			}
			dirs = append(dirs, d)
		case c.foldPrefix("*gofor") && c.boundaryAt(len("*gofor")):
			d, ok := c.scanDirectiveValue("*goFor", tag)
			if !ok {
				return dirs
			}
			dirs = append(dirs, d)
		default:
			c.advance(1)
		}
	}
	return dirs
}

// scanDirectiveValue consumes one directive attribute starting at its name,
// returning the directive positioned at its expression. ok is false when a
// quoted value has no closing quote, which ends the caller's scan.
func (c *cursor) scanDirectiveValue(name string, tag int) (directive, bool) {
	c.advance(len(name))
	c.skipSpace()
	d := directive{name: name, tag: tag, line: c.line, col: c.col}
	if c.done() || c.peek() != '=' {
		return d, true // valueless attribute: empty expression
	}
	c.advance(1)
	c.skipSpace()
	if !c.done() && (c.peek() == '"' || c.peek() == '\'') {
		q := c.peek()
		c.advance(1)
		c.skipSpace()
		d.line, d.col = c.line, c.col
		valLen := bytes.IndexByte(c.src[c.i:], q)
		if valLen < 0 {
			return d, false
		}
		d.expr = strings.TrimSpace(string(c.src[c.i : c.i+valLen]))
		c.advance(valLen + 1)
		return d, true
	}
	d.line, d.col = c.line, c.col
	start := c.i
	for !c.done() && !isUnquotedValueEnd(c.peek()) {
		c.advance(1)
	}
	d.expr = string(c.src[start:c.i])
	return d, true
}

// isUnquotedValueEnd reports whether b terminates an unquoted HTML attribute
// value.
func isUnquotedValueEnd(b byte) bool {
	switch b {
	case ' ', '\t', '\r', '\n', '/', '>':
		return true
	}
	return false
}

// checkDirectives validates directive expressions against their grammar,
// returning one diagnostic per malformed expression.
func checkDirectives(file string, dirs []directive) []Diagnostic {
	var diags []Diagnostic
	for _, d := range dirs {
		var message, suggestion string
		switch d.name {
		case "*goIf":
			if isFieldPath(d.expr) {
				continue
			}
			message = fmt.Sprintf("malformed *goIf expression %q: want a field path such as %q", d.expr, "IsActive")
			suggestion = "bind *goIf to a boolean field or method on the component struct"
		case "*goFor":
			if loopVar, list, ok := parseGoFor(d.expr); ok && isSimpleIdent(loopVar) && isFieldPath(list) {
				continue
			}
			message = fmt.Sprintf("malformed *goFor expression %q: want %q", d.expr, "let <var> of <FieldPath>")
			suggestion = `use the form *goFor="let item of Items"`
			if isSimpleIdent(d.expr) {
				suggestion = fmt.Sprintf(`write *goFor="let item of %s"`, d.expr)
			}
		default:
			continue
		}
		diags = append(diags, Diagnostic{
			File:       file,
			Line:       d.line,
			Col:        d.col,
			Severity:   SeverityError,
			Code:       CodeMalformedDirective,
			Message:    message,
			Suggestion: suggestion,
		})
	}
	for i := 1; i < len(dirs); i++ {
		if dirs[i].tag != dirs[i-1].tag {
			continue
		}
		diags = append(diags, Diagnostic{
			File:     file,
			Line:     dirs[i].line,
			Col:      dirs[i].col,
			Severity: SeverityError,
			Code:     CodeConflictingDirectives,
			Message: fmt.Sprintf("conflicting structural directives: %s cannot share an element with %s",
				dirs[i].name, dirs[i-1].name),
			Suggestion: "move one directive to a wrapping element; an element takes at most one structural directive",
		})
	}
	return diags
}

// isFieldPath reports whether s is a bare dot-path of Go identifiers
// (Field, Form.Value) or a loop-variable reference ($item, $item.Name).
func isFieldPath(s string) bool {
	s = strings.TrimPrefix(s, "$")
	for _, seg := range strings.Split(s, ".") {
		if !isSimpleIdent(seg) {
			return false
		}
	}
	return true
}

// directiveRefs extracts the struct references directive expressions make —
// a *goIf condition, a *goFor list — as positioned expressions for the vet
// cross-check. Loop-variable references ($var) resolve at render time, not
// against the struct, so they are skipped.
func directiveRefs(dirs []directive) []interpolation {
	var refs []interpolation
	for _, d := range dirs {
		expr, line, col := d.expr, d.line, d.col
		if d.name == "*goFor" {
			_, list, ok := parseGoFor(d.expr)
			if !ok {
				continue
			}
			line, col = advancePos(line, col, d.expr[:strings.LastIndex(d.expr, list)])
			expr = list
		}
		if strings.HasPrefix(expr, "$") {
			continue
		}
		refs = append(refs, interpolation{expr: expr, line: line, col: col})
	}
	return refs
}

// advancePos walks s from position (line, col), returning the position just
// past its final byte.
func advancePos(line, col int, s string) (int, int) {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return line, col
}

// hasFoldPrefix reports whether b starts with prefix under ASCII
// case-folding.
func hasFoldPrefix(b []byte, prefix string) bool {
	return len(b) >= len(prefix) && strings.EqualFold(string(b[:len(prefix)]), prefix)
}
