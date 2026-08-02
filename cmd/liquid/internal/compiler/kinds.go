package compiler

import (
	"fmt"
	"strings"

	"golang.org/x/net/html"
)

// directiveKind describes one directive or binding the compiler knows. All
// kind-specific behavior hangs off this table — the raw-source scanner, the
// expression grammar check, the vet reference extraction, and the node-tree
// rewrite each consult it — so adding a kind means adding one entry here.
type directiveKind struct {
	canonical  string // author-facing spelling: *goIf
	lowered    string // scanner/HTML-parser spelling: *goif
	structural bool   // wraps its element in a control block; at most one per element
	action     bool   // event binding dispatching to a handler; needs a [hydroId] boundary
	// check validates the directive's expression grammar, returning a
	// diagnostic for a malformed one; nil means any expression is accepted.
	check func(file string, d directive) *Diagnostic
	// ref extracts the struct reference the expression makes for the vet
	// cross-check; ok is false when there is nothing to check.
	ref func(d directive) (structRef, bool)
	// rewrite applies the kind's node-tree transform to attribute i of n,
	// appending any bound action names to actions.
	rewrite func(n *html.Node, i int, actions *[]string) error
}

// directiveKinds is the directive/binding registry.
var directiveKinds = []directiveKind{
	{
		canonical:  "*goIf",
		lowered:    "*goif",
		structural: true,
		check:      checkGoIfExpr,
		ref:        fieldExprRef,
		rewrite:    rewriteGoIf,
	},
	{
		canonical:  "*goFor",
		lowered:    "*gofor",
		structural: true,
		check:      checkGoForExpr,
		ref:        goForListRef,
		rewrite:    rewriteGoFor,
	},
	{
		canonical: "(click)",
		lowered:   "(click)",
		action:    true,
		ref:       actionRef,
		rewrite:   rewriteClick,
	},
	{
		canonical: "(submit)",
		lowered:   "(submit)",
		action:    true,
		ref:       actionRef,
		rewrite:   rewriteSubmit,
	},
	{
		canonical: "[hydroId]",
		lowered:   "[hydroid]",
		ref:       hydroRootRef,
		rewrite:   rewriteHydroID,
	},
	{
		// <form> is a tag-level kind, not an attribute: the tag scanner
		// records it (never matchKind), and its only role is asking vet to
		// verify the CSRFToken plumbing (D15). The CSRF input injection is a
		// node-tree transform keyed on the form element itself.
		canonical: "<form>",
		lowered:   "<form>",
		ref:       csrfRootRef,
	},
}

// kindByCanonical resolves a directive's author-facing name to its kind.
func kindByCanonical(name string) *directiveKind {
	for i := range directiveKinds {
		if directiveKinds[i].canonical == name {
			return &directiveKinds[i]
		}
	}
	return nil
}

// kindByLowered resolves an HTML-parser attribute key to its kind.
func kindByLowered(key string) *directiveKind {
	for i := range directiveKinds {
		if directiveKinds[i].lowered == key {
			return &directiveKinds[i]
		}
	}
	return nil
}

// malformedDirective builds the LSX005 diagnostic shared by expression
// grammar checks.
func malformedDirective(file string, d directive, message, suggestion string) *Diagnostic {
	return &Diagnostic{
		File:       file,
		Line:       d.line,
		Col:        d.col,
		Severity:   SeverityError,
		Code:       CodeMalformedDirective,
		Message:    message,
		Suggestion: suggestion,
	}
}

// checkGoIfExpr validates a *goIf condition: a bare field path.
func checkGoIfExpr(file string, d directive) *Diagnostic {
	if isFieldPath(d.expr) {
		return nil
	}
	return malformedDirective(file, d,
		fmt.Sprintf("malformed *goIf expression %q: want a field path such as %q", d.expr, "IsActive"),
		"bind *goIf to a boolean field or method on the component struct")
}

// checkGoForExpr validates a *goFor expression against its
// "let <var> of <FieldPath>" grammar.
func checkGoForExpr(file string, d directive) *Diagnostic {
	if loopVar, list, ok := parseGoFor(d.expr); ok && isSimpleIdent(loopVar) && isFieldPath(list) {
		return nil
	}
	suggestion := `use the form *goFor="let item of Items"`
	if isSimpleIdent(d.expr) {
		suggestion = fmt.Sprintf(`write *goFor="let item of %s"`, d.expr)
	}
	return malformedDirective(file, d,
		fmt.Sprintf("malformed *goFor expression %q: want %q", d.expr, "let <var> of <FieldPath>"),
		suggestion)
}

// fieldExprRef treats the whole expression as a struct reference.
// Loop-variable references ($var) resolve at render time, not against the
// struct, so they yield nothing to check.
func fieldExprRef(d directive) (structRef, bool) {
	if strings.HasPrefix(d.expr, "$") {
		return structRef{}, false
	}
	return structRef{expr: d.expr, pos: d.pos, kind: refField}, true
}

// goForListRef extracts the list half of a *goFor expression, positioned at
// the list's own first byte.
func goForListRef(d directive) (structRef, bool) {
	_, list, ok := parseGoFor(d.expr)
	if !ok || strings.HasPrefix(list, "$") {
		return structRef{}, false
	}
	line, col := advancePos(d.line, d.col, d.expr[:strings.LastIndex(d.expr, list)])
	return structRef{expr: list, pos: pos{line: line, col: col}}, true
}

// actionRef marks the expression as an event-handler reference, which vet
// holds to the dispatchable-method rules rather than any-field-or-method.
func actionRef(d directive) (structRef, bool) {
	return structRef{expr: d.expr, pos: d.pos, kind: refAction, binding: d.name}, true
}

// hydroRootRef asks vet to verify the HydroID plumbing on the paired struct,
// anchored at the [hydroId] attribute itself.
func hydroRootRef(d directive) (structRef, bool) {
	return structRef{pos: d.namePos, kind: refHydroRoot}, true
}

// csrfRootRef asks vet to verify the CSRFToken plumbing on the paired struct,
// anchored at the <form> tag itself.
func csrfRootRef(d directive) (structRef, bool) {
	return structRef{pos: d.namePos, kind: refCSRFRoot}, true
}

// rewriteGoIf wraps the element in the {{if}} block its condition compiles
// to (D1).
func rewriteGoIf(n *html.Node, i int, _ *[]string) error {
	expr := n.Attr[i].Val
	n.Attr = append(n.Attr[:i], n.Attr[i+1:]...)
	wrap(n, "{{if "+fieldRef(expr)+"}}", "{{end}}")
	return nil
}

// rewriteGoFor wraps the element in the {{range}} block its expression
// compiles to.
func rewriteGoFor(n *html.Node, i int, _ *[]string) error {
	loopVar, list, ok := parseGoFor(n.Attr[i].Val)
	if !ok {
		// Diagnostics gate codegen, so a malformed expression here means the
		// raw-source scan missed this attribute — fail loudly rather than
		// ship the directive to the browser.
		return fmt.Errorf("malformed *goFor expression %q escaped the diagnostic scan", n.Attr[i].Val)
	}
	n.Attr = append(n.Attr[:i], n.Attr[i+1:]...)
	wrap(n, "{{range $"+loopVar+" := "+fieldRef(list)+"}}", "{{end}}")
	return nil
}

// rewriteClick turns (click)="Method" into data-liquid-action="Method" — the
// hook the fixed runtime script listens for — and adds Method to the
// compiled allowlist.
func rewriteClick(n *html.Node, i int, actions *[]string) error {
	n.Attr[i].Key = "data-liquid-action"
	*actions = append(*actions, n.Attr[i].Val)
	return nil
}

// rewriteSubmit turns (submit)="Method" into data-liquid-submit="Method" —
// the hook the runtime script's submit listener keys on — and adds Method to
// the compiled allowlist.
func rewriteSubmit(n *html.Node, i int, actions *[]string) error {
	n.Attr[i].Key = "data-liquid-submit"
	*actions = append(*actions, n.Attr[i].Val)
	return nil
}

// rewriteHydroID turns [hydroId] into data-hydro-id="{{ .HydroID }}", the
// patch boundary the framework fills at render (D14).
func rewriteHydroID(n *html.Node, i int, _ *[]string) error {
	n.Attr[i].Key = "data-hydro-id"
	n.Attr[i].Val = "{{ .HydroID }}"
	return nil
}
