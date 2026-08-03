package compiler

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
)

// directive is one directive or binding attribute found in raw .lsx source,
// positioned at the first byte of its expression so a diagnostic points at
// what the author should edit. namePos points at the attribute name itself,
// for diagnostics about the attribute rather than its expression.
type directive struct {
	name string // canonical spelling: *goIf, *goFor, (click), [hydroId], <child>, [input]
	expr string
	pos
	namePos pos
	tag     int    // ordinal of the enclosing < tag, for the one-per-element rule
	attr    string // an [input] binding's child field name, as the author typed it
	sel     string // the child selector: an <child>'s own tag, an [input]'s enclosing tag
}

// The two directive kinds the tag scanner records without a kinds-table
// entry: child-selector tags are dynamic (any hyphenated element), and their
// [input] bindings take arbitrary attribute names.
const (
	kindChildTag = "<child>"
	kindInput    = "[input]"
)

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
	tag, inTag, childSel := 0, false, ""
	for !c.done() {
		if !inTag {
			if c.hasPrefix("<!--") {
				c.skipPast("-->")
				continue
			}
			if c.peek() == '<' {
				inTag = true
				tag++
				childSel = ""
				at := pos{line: c.line, col: c.col}
				switch name := c.tagName(); {
				case name == "form":
					dirs = append(dirs, directive{name: "<form>", tag: tag, pos: at, namePos: at})
				case strings.Contains(name, "-"):
					// A custom-element tag is a child-component occurrence
					// (D14); its bracketed attributes scan as [input] bindings.
					childSel = name
					dirs = append(dirs, directive{name: kindChildTag, expr: name, sel: name, tag: tag, pos: at, namePos: at})
				}
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
		default:
			var d directive
			var ok bool
			switch k, input := c.matchKind(), c.matchInputAttr(childSel); {
			case k != nil:
				d, ok = c.scanDirectiveValue(k.canonical, tag)
			case input != "":
				d, ok = c.scanDirectiveValue("["+input+"]", tag)
				d.name, d.attr, d.sel = kindInput, input, childSel
			default:
				c.advance(1)
				continue
			}
			if !ok {
				return dirs
			}
			dirs = append(dirs, d)
		}
	}
	return dirs
}

// tagName reads the element name at a '<' the cursor sits on, lowercased —
// HTML tag names are case-insensitive. A closing tag or non-name (comment,
// doctype, stray <) yields "".
func (c *cursor) tagName() string {
	i := c.i + 1
	start := i
	for i < len(c.src) && isTagNameByte(c.src[i]) {
		i++
	}
	return strings.ToLower(string(c.src[start:i]))
}

// isTagNameByte reports whether b may appear in an element name: letters,
// digits, and the hyphens that mark custom elements.
func isTagNameByte(b byte) bool {
	return b == '-' || b >= '0' && b <= '9' || b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z'
}

// matchInputAttr reports the field name of an [input] binding attribute
// starting at the cursor, or "". Only child-selector elements take input
// bindings — bracketed attributes elsewhere are left alone (the attribute
// directive extension point) — and the framework's own bracketed bindings
// ([hydroId]) are matched by matchKind first, never here.
func (c *cursor) matchInputAttr(childSel string) string {
	if childSel == "" || c.done() || c.peek() != '[' {
		return ""
	}
	rest := c.src[c.i+1:]
	end := bytes.IndexByte(rest, ']')
	if end <= 0 || !c.boundaryAt(end+2) {
		return ""
	}
	name := string(rest[:end])
	if !isSimpleIdent(name) {
		return ""
	}
	return name
}

// matchKind reports which directive kind, if any, starts an attribute at the
// cursor. Tag-level kinds (<form>) are recognized by the tag scanner, never
// as attributes.
func (c *cursor) matchKind() *directiveKind {
	for i := range directiveKinds {
		k := &directiveKinds[i]
		if k.lowered[0] == '<' {
			continue
		}
		if c.foldPrefix(k.lowered) && c.boundaryAt(len(k.lowered)) {
			return k
		}
	}
	return nil
}

// scanDirectiveValue consumes one directive attribute starting at its name,
// returning the directive positioned at its expression. ok is false when a
// quoted value has no closing quote, which ends the caller's scan.
func (c *cursor) scanDirectiveValue(name string, tag int) (directive, bool) {
	namePos := pos{line: c.line, col: c.col}
	c.advance(len(name))
	c.skipSpace()
	d := directive{name: name, tag: tag, pos: pos{line: c.line, col: c.col}, namePos: namePos}
	if c.done() || c.peek() != '=' {
		return d, true // valueless attribute: empty expression
	}
	c.advance(1)
	c.skipSpace()
	if !c.done() && (c.peek() == '"' || c.peek() == '\'') {
		q := c.peek()
		c.advance(1)
		c.skipSpace()
		d.pos = pos{line: c.line, col: c.col}
		valLen := bytes.IndexByte(c.src[c.i:], q)
		if valLen < 0 {
			return d, false
		}
		d.expr = strings.TrimSpace(string(c.src[c.i : c.i+valLen]))
		c.advance(valLen + 1)
		return d, true
	}
	d.pos = pos{line: c.line, col: c.col}
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

// checkDirectives validates directive expressions against their kinds'
// grammar, returning one diagnostic per malformed expression, plus the
// file-level patch-boundary and one-structural-per-element rules.
func checkDirectives(file string, dirs []directive) []Diagnostic {
	var diags []Diagnostic
	childTags := make(map[int]string) // tag ordinal → child selector
	for _, d := range dirs {
		switch d.name {
		case kindChildTag:
			childTags[d.tag] = d.sel
			continue
		case kindInput:
			if diag := checkInputExpr(file, d); diag != nil {
				diags = append(diags, *diag)
			}
			continue
		}
		k := kindByCanonical(d.name)
		if k == nil {
			continue
		}
		if sel, onChild := childTags[d.tag]; onChild && (k.action || d.name == "[hydroId]") {
			diags = append(diags, childBindingDiag(file, d, sel))
			continue
		}
		if d.name == "*liquidDefer" {
			if _, onChild := childTags[d.tag]; !onChild {
				diags = append(diags, misplacedDeferDiag(file, d))
				continue
			}
		}
		if k.check == nil {
			continue
		}
		if diag := k.check(file, d); diag != nil {
			diags = append(diags, *diag)
		}
	}
	if d := checkHydroRoot(file, dirs); d != nil {
		diags = append(diags, *d)
	}
	return append(diags, conflictingDirectiveDiags(file, dirs)...)
}

// childBindingDiag builds the LSX014 for an event binding or [hydroId] on a
// child-selector element, which the child's render replaces wholesale.
func childBindingDiag(file string, d directive, sel string) Diagnostic {
	return Diagnostic{
		File:     file,
		Line:     d.namePos.line,
		Col:      d.namePos.col,
		Severity: SeverityError,
		Code:     CodeChildBindingUnsupported,
		Message: fmt.Sprintf("%s cannot bind on the child selector %s; the element is replaced by the child's own render",
			d.name, sel),
		Suggestion: fmt.Sprintf("move the %s into the %s component's own template", d.name, sel),
	}
}

// misplacedDeferDiag builds the LSX015 for *liquidDefer on anything that is
// not a child-selector element.
func misplacedDeferDiag(file string, d directive) Diagnostic {
	return Diagnostic{
		File:       file,
		Line:       d.namePos.line,
		Col:        d.namePos.col,
		Severity:   SeverityError,
		Code:       CodeMisplacedDefer,
		Message:    "*liquidDefer must sit on a child-selector element; only a nested component occurrence can render deferred",
		Suggestion: "defer a nested component usage such as <app-stats *liquidDefer>, not a plain element",
	}
}

// conflictingDirectiveDiags reports every element carrying more than one
// structural directive (LSX006).
func conflictingDirectiveDiags(file string, dirs []directive) []Diagnostic {
	var diags []Diagnostic
	for i := 1; i < len(dirs); i++ {
		if dirs[i].tag != dirs[i-1].tag || !isStructural(dirs[i].name) || !isStructural(dirs[i-1].name) {
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

// checkInputExpr validates an [input] binding's expression: a bare parent
// field path, like a *goIf condition. Anything else is an LSX013.
func checkInputExpr(file string, d directive) *Diagnostic {
	if d.expr != "" && isFieldPath(d.expr) {
		return nil
	}
	return &Diagnostic{
		File:     file,
		Line:     d.line,
		Col:      d.col,
		Severity: SeverityError,
		Code:     CodeBadInputBinding,
		Message:  fmt.Sprintf("malformed [input] expression %q: want a parent field path such as %q", d.expr, "Owner"),
		Suggestion: fmt.Sprintf("write [%s]=%q — input bindings take the field name bare, without braces",
			d.attr, "Owner"),
	}
}

// checkHydroRoot enforces the patch-boundary rule: an event binding needs
// some element in the file to declare [hydroId], or there is nothing for the
// runtime to swap when the event's patch comes back (D14). The diagnostic
// anchors at the first event binding, reported once per file.
func checkHydroRoot(file string, dirs []directive) *Diagnostic {
	var firstBinding *directive
	for i, d := range dirs {
		if d.name == "[hydroId]" {
			return nil
		}
		if k := kindByCanonical(d.name); k != nil && k.action && firstBinding == nil {
			firstBinding = &dirs[i]
		}
	}
	if firstBinding == nil {
		return nil
	}
	return &Diagnostic{
		File:     file,
		Line:     firstBinding.namePos.line,
		Col:      firstBinding.namePos.col,
		Severity: SeverityError,
		Code:     CodeMissingHydroRoot,
		Message: fmt.Sprintf("%s needs a patch boundary, but no element in %s declares [hydroId]",
			firstBinding.name, filepath.Base(file)),
		Suggestion: "add [hydroId] to the component's root element",
	}
}

// isStructural reports whether name is a structural directive — one that
// wraps its element in a control block and so cannot share an element with
// another structural directive. Event bindings such as (click) are not
// structural and coexist freely.
func isStructural(name string) bool {
	k := kindByCanonical(name)
	return k != nil && k.structural
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
// a *goIf condition, a *goFor list, a (click) handler, a [hydroId]
// declaration — as positioned expressions for the vet cross-check, each
// kind's ref extractor deciding what there is to check.
func directiveRefs(dirs []directive) []structRef {
	var refs []structRef
	childTags := make(map[int]string) // tag ordinal → child selector
	for _, d := range dirs {
		switch d.name {
		case kindChildTag:
			childTags[d.tag] = d.sel
			refs = append(refs, structRef{expr: d.expr, pos: d.pos, kind: refChildTag})
			continue
		case kindInput:
			refs = append(refs, structRef{expr: d.expr, pos: d.pos, kind: refInput, binding: d.attr, sel: d.sel, namePos: d.namePos})
			continue
		}
		if d.name == "*liquidDefer" {
			if sel, onChild := childTags[d.tag]; onChild {
				refs = append(refs, structRef{pos: d.namePos, kind: refDefer, sel: sel})
			}
			continue
		}
		k := kindByCanonical(d.name)
		if k == nil || k.ref == nil {
			continue
		}
		if ref, ok := k.ref(d); ok {
			refs = append(refs, ref)
		}
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
