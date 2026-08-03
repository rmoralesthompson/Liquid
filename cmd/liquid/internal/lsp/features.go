package lsp

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/rmoralesthompson/liquid/cmd/liquid/internal/compiler"
)

// The canonical directive spellings, matching the compiler's kind table.
const (
	dirGoIf    = "*goIf"
	dirGoFor   = "*goFor"
	dirClick   = "(click)"
	dirSubmit  = "(submit)"
	dirHydroID = "[hydroId]"
	dirForm    = "<form>"
)

// targetKind classifies the semantic token under a document offset.
type targetKind int

const (
	targetNone targetKind = iota
	// targetMember is a paired-struct field or method reference.
	targetMember
	// targetLoopVar is a $var reference or the <var> of a *goFor.
	targetLoopVar
	// targetDirective is a directive or binding attribute name.
	targetDirective
	// targetSelector is a child-component custom-element tag.
	targetSelector
	// targetInputAttr is an [input] binding's attribute name.
	targetInputAttr
)

// target is the semantic token under one document offset.
type target struct {
	kind targetKind
	// name is the member name, loop variable (without $), canonical
	// directive spelling, selector, or child field name.
	name string
	// sel is the owning child selector, for targetInputAttr.
	sel  string
	span span
}

// resolveTarget finds the semantic token at a byte offset: an identifier
// inside an interpolation, or a directive's name or expression.
func (d *document) resolveTarget(off int) target {
	for _, in := range d.sa.Interpolations {
		start := d.offsetOf(in.Line, in.Col)
		if !(span{start, start + len(in.Expr)}).contains(off) {
			continue
		}
		return exprTarget(in.Expr, start, off)
	}
	for _, use := range d.sa.Directives {
		if t := d.directiveTarget(use, off); t.kind != targetNone {
			return t
		}
	}
	return target{}
}

// exprTarget resolves an offset inside a field-path expression. The leading
// segment is a struct member ($-prefixed: a loop variable); deeper path
// segments resolve at render time and have no static target.
func exprTarget(expr string, start, off int) target {
	name := expr
	if i := strings.IndexByte(expr, '.'); i >= 0 {
		name = expr[:i]
	}
	sp := span{start, start + len(name)}
	if !sp.contains(off) {
		return target{}
	}
	if rest, ok := strings.CutPrefix(name, "$"); ok && isIdent(rest) {
		return target{kind: targetLoopVar, name: rest, span: sp}
	}
	if !isIdent(name) {
		return target{}
	}
	return target{kind: targetMember, name: name, span: sp}
}

// directiveTarget resolves an offset against one directive occurrence: its
// name token, or its expression.
func (d *document) directiveTarget(use compiler.DirectiveUse, off int) target {
	nameStart := d.offsetOf(use.NameLine, use.NameCol)
	exprStart := d.offsetOf(use.Line, use.Col)

	switch use.Name {
	case compiler.KindChildTag:
		// The name token is "<selector"; the target is the selector itself.
		sp := span{nameStart + 1, nameStart + 1 + len(use.Sel)}
		if sp.contains(off) {
			return target{kind: targetSelector, name: use.Sel, span: sp}
		}
		return target{}
	case compiler.KindInput:
		sp := span{nameStart + 1, nameStart + 1 + len(use.Attr)}
		if sp.contains(off) {
			return target{kind: targetInputAttr, name: use.Attr, sel: use.Sel, span: sp}
		}
		if use.Expr != "" && (span{exprStart, exprStart + len(use.Expr)}).contains(off) {
			return exprTarget(use.Expr, exprStart, off)
		}
		return target{}
	case dirForm:
		return target{}
	}

	if (span{nameStart, nameStart + len(use.Name)}).contains(off) {
		return target{kind: targetDirective, name: use.Name, span: span{nameStart, nameStart + len(use.Name)}}
	}
	if use.Expr == "" || !(span{exprStart, exprStart + len(use.Expr)}).contains(off) {
		return target{}
	}
	if use.Name == dirGoFor {
		return goForTarget(use.Expr, exprStart, off)
	}
	// *goIf, (click), (submit): the expression is one field path or
	// handler name.
	return exprTarget(use.Expr, exprStart, off)
}

// goForTarget resolves offsets inside "let <var> of <List>": the variable
// declares a loop target, the list half references the struct.
func goForTarget(expr string, exprStart, off int) target {
	loopVar, list, ok := compiler.ParseGoFor(expr)
	if !ok {
		return target{}
	}
	varIdx := goForVarIndex(expr)
	if sp := (span{exprStart + varIdx, exprStart + varIdx + len(loopVar)}); sp.contains(off) {
		return target{kind: targetLoopVar, name: loopVar, span: sp}
	}
	listIdx := strings.LastIndex(expr, list)
	if (span{exprStart + listIdx, exprStart + listIdx + len(list)}).contains(off) {
		return exprTarget(list, exprStart+listIdx, off)
	}
	return target{}
}

// goForVarIndex locates the loop variable's byte index inside a *goFor
// expression that already parsed: the token after the "let" keyword. A
// plain strings.Index would misfire on variables that are substrings of
// "let" itself (a variable named t matches inside the keyword).
func goForVarIndex(expr string) int {
	i := 0
	skipSpace := func() {
		for i < len(expr) && (expr[i] == ' ' || expr[i] == '\t' || expr[i] == '\r' || expr[i] == '\n') {
			i++
		}
	}
	skipSpace()
	i += len("let")
	skipSpace()
	return i
}

// member finds a paired-struct member by exact name.
func (d *document) member(name string) *compiler.Member {
	for i := range d.members {
		if d.members[i].Name == name {
			return &d.members[i]
		}
	}
	return nil
}

// selector finds a declared selector by tag.
func (d *document) selector(sel string) *compiler.SelectorDecl {
	for i := range d.selectors {
		if d.selectors[i].Selector == sel {
			return &d.selectors[i]
		}
	}
	return nil
}

// childMember finds a child component's field for an [input] attribute name;
// HTML lowercases attribute names, so the match is case-insensitive — the
// same rule the compiler and runtime apply.
func (d *document) childMember(sel, name string) (*compiler.SelectorDecl, *compiler.Member) {
	decl := d.selector(sel)
	if decl == nil || d.facts == nil {
		return nil, nil
	}
	for _, m := range d.facts.Component(decl.Struct) {
		if !m.Method && strings.EqualFold(m.Name, name) {
			return decl, &m
		}
	}
	return decl, nil
}

// loopVarUse finds the *goFor occurrence declaring a loop variable.
func (d *document) loopVarUse(name string) *compiler.DirectiveUse {
	for i, use := range d.sa.Directives {
		if use.Name != dirGoFor {
			continue
		}
		if v, _, ok := compiler.ParseGoFor(use.Expr); ok && v == name {
			return &d.sa.Directives[i]
		}
	}
	return nil
}

// hover renders the hover content for the token at a byte offset.
func (d *document) hover(off int) *Hover {
	t := d.resolveTarget(off)
	md := ""
	switch t.kind {
	case targetMember:
		if m := d.member(t.name); m != nil {
			md = d.memberMarkdown(m)
		}
	case targetLoopVar:
		md = fmt.Sprintf("`$%s` — loop variable", t.name)
		if use := d.loopVarUse(t.name); use != nil {
			md += fmt.Sprintf(" from `*goFor=\"%s\"`", use.Expr)
		}
	case targetDirective:
		md = directiveDocs[t.name]
	case targetSelector:
		if decl := d.selector(t.name); decl != nil {
			md = fmt.Sprintf("```go\ntype %s struct\n```", decl.Struct)
			if decl.Doc != "" {
				md += "\n\n" + decl.Doc
			}
			md += fmt.Sprintf("\n\n*component `%s`*", t.name)
		}
	case targetInputAttr:
		decl, m := d.childMember(t.sel, t.name)
		if m != nil {
			md = fmt.Sprintf("```go\n%s %s\n```\n\n[input] binding — copies the parent expression into `%s.%s` at render.",
				m.Name, m.Type, decl.Struct, m.Name)
		}
	}
	if md == "" {
		return nil
	}
	rng := d.rangeOf(t.span)
	return &Hover{Contents: MarkupContent{Kind: "markdown", Value: md}, Range: &rng}
}

// memberMarkdown renders one paired-struct member as hover markdown.
func (d *document) memberMarkdown(m *compiler.Member) string {
	kind := "field"
	if m.Method {
		kind = "method"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "```go\n%s %s\n```", m.Name, m.Type)
	if m.Doc != "" {
		b.WriteString("\n\n" + m.Doc)
	}
	fmt.Fprintf(&b, "\n\n*%s of `%s`*", kind, d.structName)
	if m.Handler {
		b.WriteString(" — dispatchable event handler (D11)")
	}
	return b.String()
}

// definition resolves the token at a byte offset to its declaration.
func (d *document) definition(off int) []Location {
	t := d.resolveTarget(off)
	switch t.kind {
	case targetMember:
		if m := d.member(t.name); m != nil {
			return []Location{goLocation(m.File, m.Line, m.Col, len(m.Name))}
		}
	case targetSelector:
		if decl := d.selector(t.name); decl != nil {
			return []Location{goLocation(decl.File, decl.Line, decl.Col, len(decl.Struct))}
		}
	case targetInputAttr:
		if _, m := d.childMember(t.sel, t.name); m != nil {
			return []Location{goLocation(m.File, m.Line, m.Col, len(m.Name))}
		}
	case targetLoopVar:
		if use := d.loopVarUse(t.name); use != nil {
			exprStart := d.offsetOf(use.Line, use.Col)
			idx := goForVarIndex(use.Expr)
			sp := span{exprStart + idx, exprStart + idx + len(t.name)}
			return []Location{{URI: d.uri, Range: d.rangeOf(sp)}}
		}
	case targetDirective:
		// [hydroId] and the framework field it fills are two halves of one
		// contract; jump to the field.
		if t.name == dirHydroID {
			if m := d.member("HydroID"); m != nil {
				return []Location{goLocation(m.File, m.Line, m.Col, len(m.Name))}
			}
		}
	}
	return nil
}

// goLocation points into Go source. The compiler reports byte columns; Go
// declaration lines are overwhelmingly ASCII, so the byte column doubles as
// the UTF-16 character.
func goLocation(file string, line, col, length int) Location {
	return Location{URI: pathToURI(file), Range: Range{
		Start: Position{Line: line - 1, Character: col - 1},
		End:   Position{Line: line - 1, Character: col - 1 + length},
	}}
}

// exprOcc is one referencing occurrence for highlighting: the leading
// identifier ($-prefixed for loop variables), the byte span to highlight,
// and whether it declares a loop variable.
type exprOcc struct {
	name     string
	sp       span
	declares bool
}

// exprOccurrences lists every struct- or loop-var-referencing occurrence in
// the buffer with the span of its leading identifier.
func (d *document) exprOccurrences() []exprOcc {
	var occs []exprOcc
	add := func(expr string, start int, declares bool) {
		lead := expr
		if i := strings.IndexByte(lead, '.'); i >= 0 {
			lead = lead[:i]
		}
		if lead == "" {
			return
		}
		occs = append(occs, exprOcc{name: lead, sp: span{start, start + len(lead)}, declares: declares})
	}
	for _, in := range d.sa.Interpolations {
		add(in.Expr, d.offsetOf(in.Line, in.Col), false)
	}
	for _, use := range d.sa.Directives {
		exprStart := d.offsetOf(use.Line, use.Col)
		switch use.Name {
		case compiler.KindChildTag, dirForm, dirHydroID:
		case dirGoFor:
			loopVar, list, ok := compiler.ParseGoFor(use.Expr)
			if !ok {
				continue
			}
			// The declaration site has no $ sigil in source; record the name
			// with one so it matches $var references, spanning just the var.
			varIdx := goForVarIndex(use.Expr)
			occs = append(occs, exprOcc{
				name:     "$" + loopVar,
				sp:       span{exprStart + varIdx, exprStart + varIdx + len(loopVar)},
				declares: true,
			})
			add(list, exprStart+strings.LastIndex(use.Expr, list), false)
		default:
			add(use.Expr, exprStart, false)
		}
	}
	return occs
}

// highlights marks every occurrence of the symbol at a byte offset.
func (d *document) highlights(off int) []DocumentHighlight {
	t := d.resolveTarget(off)
	switch t.kind {
	case targetMember, targetLoopVar:
	case targetSelector:
		var hs []DocumentHighlight
		for _, use := range d.sa.Directives {
			if use.Name != compiler.KindChildTag || use.Sel != t.name {
				continue
			}
			start := d.offsetOf(use.NameLine, use.NameCol) + 1
			hs = append(hs, DocumentHighlight{Range: d.rangeOf(span{start, start + len(use.Sel)}), Kind: highlightRead})
		}
		return hs
	default:
		return nil
	}

	want := t.name
	if t.kind == targetLoopVar {
		want = "$" + t.name
	}
	var hs []DocumentHighlight
	for _, occ := range d.exprOccurrences() {
		if occ.name != want {
			continue
		}
		kind := highlightRead
		if occ.declares {
			kind = highlightWrite
		}
		hs = append(hs, DocumentHighlight{Range: d.rangeOf(occ.sp), Kind: kind})
	}
	return hs
}

// goForListStarted matches a *goFor expression prefix that is past its
// "let <var> of " head, where list completions apply.
var goForListStarted = regexp.MustCompile(`^\s*let\s+[A-Za-z_][A-Za-z0-9_]*\s+of\s`)

// completions computes completion items for a byte offset.
func (d *document) completions(off int) []CompletionItem {
	if d.insideInterpolation(off) {
		return d.memberItems(d.identSpan(off), memberAny, true)
	}
	for _, use := range d.sa.Directives {
		exprStart := d.offsetOf(use.Line, use.Col)
		if !(span{exprStart, exprStart + len(use.Expr)}).contains(off) {
			continue
		}
		switch use.Name {
		case dirClick, dirSubmit:
			return d.memberItems(d.identSpan(off), memberHandler, false)
		case dirGoIf:
			return d.memberItems(d.identSpan(off), memberAny, true)
		case compiler.KindInput:
			return d.memberItems(d.identSpan(off), memberAny, true)
		case dirGoFor:
			if goForListStarted.MatchString(d.text[exprStart:off]) {
				return d.memberItems(d.identSpan(off), memberAny, true)
			}
			sp := span{exprStart, exprStart + len(use.Expr)}
			return []CompletionItem{{
				Label:            "let item of Items",
				Kind:             kindKeyword,
				Detail:           "*goFor grammar",
				Documentation:    &MarkupContent{Kind: "markdown", Value: directiveDocs[dirGoFor]},
				InsertTextFormat: snippetFormat,
				TextEdit:         &TextEdit{Range: d.rangeOf(sp), NewText: "let ${1:item} of ${2:Items}"},
			}}
		}
		// Not a completable expression context — a child tag's own span, a
		// <form>, a [hydroId] — so let the tag/attribute contexts decide.
	}
	return d.tagContextItems(off)
}

// insideInterpolation reports whether an offset sits after a {{ with no }}
// in between — computed from raw text, so completion works mid-keystroke
// even while the interpolation is still unclosed (LSX001 territory).
func (d *document) insideInterpolation(off int) bool {
	open := strings.LastIndex(d.text[:off], "{{")
	return open >= 0 && !strings.Contains(d.text[open+2:off], "}}")
}

// identSpan is the replace range for identifier-shaped completions: the
// dotted identifier token ending at off.
func (d *document) identSpan(off int) span {
	start := off
	for start > 0 && isTokenByte(d.text[start-1]) {
		start--
	}
	return span{start, off}
}

// The member filters completion contexts apply.
const (
	memberAny     = "any"
	memberHandler = "handler"
)

// memberItems builds completion items for the paired struct's members,
// optionally with the file's loop variables.
func (d *document) memberItems(sp span, filter string, loopVars bool) []CompletionItem {
	rng := d.rangeOf(sp)
	var items []CompletionItem
	for _, m := range d.members {
		if filter == memberHandler && !m.Handler {
			continue
		}
		kind, sort := kindField, "0"+m.Name
		if m.Method {
			kind, sort = kindMethod, "1"+m.Name
		}
		item := CompletionItem{
			Label:    m.Name,
			Kind:     kind,
			Detail:   m.Type,
			SortText: sort,
			TextEdit: &TextEdit{Range: rng, NewText: m.Name},
		}
		if m.Doc != "" {
			item.Documentation = &MarkupContent{Kind: "markdown", Value: m.Doc}
		}
		items = append(items, item)
	}
	if !loopVars {
		return items
	}
	for _, use := range d.sa.Directives {
		if use.Name != dirGoFor {
			continue
		}
		v, list, ok := compiler.ParseGoFor(use.Expr)
		if !ok {
			continue
		}
		items = append(items, CompletionItem{
			Label:    "$" + v,
			Kind:     kindVariable,
			Detail:   "loop variable over " + list,
			SortText: "2" + v,
			TextEdit: &TextEdit{Range: rng, NewText: "$" + v},
		})
	}
	return items
}

// tagContextItems serves the two in-tag completion contexts: a tag name
// still being typed (child selectors) and an attribute-name position
// (directives, plus [input] fields on a child element).
func (d *document) tagContextItems(off int) []CompletionItem {
	lt := strings.LastIndexByte(d.text[:off], '<')
	if lt < 0 {
		return nil
	}
	inTag := d.text[lt:off]
	if strings.ContainsRune(inTag, '>') ||
		strings.Count(inTag, `"`)%2 == 1 || strings.Count(inTag, `'`)%2 == 1 {
		return nil
	}
	if !strings.ContainsAny(inTag, " \t\r\n") {
		return d.selectorItems(span{lt + 1, off})
	}

	start := off
	for start > lt+1 && isAttrNameByte(d.text[start-1]) {
		start--
	}
	rng := d.rangeOf(span{start, off})
	items := directiveItems(rng)
	items = append(items, d.childInputItems(lt, rng)...)
	return items
}

// selectorItems completes child-component tags from the package's declared
// selectors.
func (d *document) selectorItems(sp span) []CompletionItem {
	rng := d.rangeOf(sp)
	var items []CompletionItem
	for _, decl := range d.selectors {
		item := CompletionItem{
			Label:    decl.Selector,
			Kind:     kindClass,
			Detail:   "component " + decl.Struct,
			TextEdit: &TextEdit{Range: rng, NewText: decl.Selector},
		}
		if decl.Doc != "" {
			item.Documentation = &MarkupContent{Kind: "markdown", Value: decl.Doc}
		}
		items = append(items, item)
	}
	return items
}

// directiveItems completes the directive and binding attribute names.
func directiveItems(rng Range) []CompletionItem {
	specs := []struct {
		label   string
		newText string
		kind    int
		sort    string
	}{
		{dirGoIf, `*goIf="${1}"`, kindKeyword, "00"},
		{dirGoFor, `*goFor="let ${1:item} of ${2:Items}"`, kindKeyword, "01"},
		{dirClick, `(click)="${1}"`, kindEvent, "02"},
		{dirSubmit, `(submit)="${1}"`, kindEvent, "03"},
		{dirHydroID, dirHydroID, kindProperty, "04"},
	}
	items := make([]CompletionItem, 0, len(specs))
	for _, s := range specs {
		items = append(items, CompletionItem{
			Label:            s.label,
			Kind:             s.kind,
			Detail:           "liquid directive",
			Documentation:    &MarkupContent{Kind: "markdown", Value: directiveDocs[s.label]},
			FilterText:       s.label,
			SortText:         s.sort,
			InsertTextFormat: snippetFormat,
			TextEdit:         &TextEdit{Range: rng, NewText: s.newText},
		})
	}
	return items
}

// childInputItems completes [input] attribute names on a child-selector
// element from the child struct's exported fields. tagStart is the byte
// offset of the element's '<'.
func (d *document) childInputItems(tagStart int, rng Range) []CompletionItem {
	var sel string
	for _, use := range d.sa.Directives {
		if use.Name == compiler.KindChildTag && d.offsetOf(use.NameLine, use.NameCol) == tagStart {
			sel = use.Sel
			break
		}
	}
	if sel == "" {
		return nil
	}
	decl := d.selector(sel)
	if decl == nil || d.facts == nil {
		return nil
	}
	var items []CompletionItem
	for _, m := range d.facts.Component(decl.Struct) {
		if m.Method {
			continue
		}
		attr := strings.ToLower(m.Name[:1]) + m.Name[1:]
		items = append(items, CompletionItem{
			Label:            "[" + attr + "]",
			Kind:             kindProperty,
			Detail:           fmt.Sprintf("[input] → %s.%s %s", decl.Struct, m.Name, m.Type),
			FilterText:       "[" + attr + "]",
			SortText:         "1" + attr,
			InsertTextFormat: snippetFormat,
			TextEdit:         &TextEdit{Range: rng, NewText: "[" + attr + `]="${1}"`},
		})
	}
	return items
}

// isIdent reports whether s is a plain identifier.
func isIdent(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		b := s[i]
		if b == '_' || 'a' <= b && b <= 'z' || 'A' <= b && b <= 'Z' || i > 0 && '0' <= b && b <= '9' {
			continue
		}
		return false
	}
	return true
}

// isTokenByte reports whether b may appear in a dotted template identifier
// token ($item.Name, Form.Value).
func isTokenByte(b byte) bool {
	return b == '_' || b == '$' || b == '.' ||
		'a' <= b && b <= 'z' || 'A' <= b && b <= 'Z' || '0' <= b && b <= '9'
}

// isAttrNameByte reports whether b may appear in a directive-shaped
// attribute name being typed (*goIf, (click), [hydroId]).
func isAttrNameByte(b byte) bool {
	switch b {
	case '*', '(', ')', '[', ']', '-', '_':
		return true
	}
	return 'a' <= b && b <= 'z' || 'A' <= b && b <= 'Z' || '0' <= b && b <= '9'
}
