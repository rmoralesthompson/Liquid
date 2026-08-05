package compiler

import (
	"go/ast"
	"go/token"
	"go/types"
	"slices"
	"strings"
)

// The D29 reactivity-leak diagnostic text. A direct Subscribe call in
// component code is not owned by the framework's session lifecycle (D20), so
// it leaks unless the author hand-rolls the teardown the framework exists to
// own. The managed path is liquid.Observe inside Subscriptions() (D25). A call
// that discards its cancel provably leaks (error); a call that captures it may
// still be released somewhere, so it is a warning.
const (
	subscriptionLeakMessage    = "this Subscribe call discards its cancel, so the subscription is never released when the session ends"
	subscriptionRiskMessage    = "this Subscribe call is not tied to the session lifecycle, so the subscription risks leaking when the session ends"
	subscriptionLeakSuggestion = "declare the subscription with liquid.Observe inside Subscriptions() so the framework cancels it on session GC (D25)"
)

// VetSubscriptions statically flags direct Subscribe calls on a Liquid
// observable (BehaviorSubject, Derived, or the Observable interface) in the
// package's own source — the D29 reactivity-leak check. Such a subscription is
// not tied to the session's unsubscribe-on-GC hook (D20), so it leaks; the
// managed path is liquid.Observe within Subscriptions() (D25). Best-effort: it
// flags the detectable call pattern, not a soundness proof, and a broken
// package (no usable type information) yields nothing.
func (f *Facts) VetSubscriptions() []Diagnostic {
	if f.Broken() {
		return nil
	}
	var diags []Diagnostic
	for _, file := range f.pkg.Syntax {
		discarded := discardedCalls(file)
		suppressed := suppressedLines(f.pkg.Fset, file)
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Subscribe" || !f.isCoreSubscribe(sel) {
				return true
			}
			pos := f.pkg.Fset.Position(sel.Sel.Pos())
			// A directive on the call's line, or the line just above it,
			// silences the finding — the rare deliberate direct subscription.
			if suppressed[pos.Line] || suppressed[pos.Line-1] {
				return true
			}
			severity, message := SeverityWarning, subscriptionRiskMessage
			if discarded[call] {
				// The cancel is thrown away, so nothing can ever release the
				// subscription: a provable leak, escalated to an error (D29).
				severity, message = SeverityError, subscriptionLeakMessage
			}
			diags = append(diags, Diagnostic{
				File:       pos.Filename,
				Line:       pos.Line,
				Col:        pos.Column,
				Severity:   severity,
				Code:       CodeUnmanagedSubscription,
				Message:    message,
				Suggestion: subscriptionLeakSuggestion,
			})
			return true
		})
	}
	slices.SortFunc(diags, func(a, b Diagnostic) int {
		if a.File != b.File {
			return strings.Compare(a.File, b.File)
		}
		if a.Line != b.Line {
			return a.Line - b.Line
		}
		return a.Col - b.Col
	})
	return diags
}

// suppressDirective is the comment marker that silences the D29 leak check for
// a deliberate direct subscription. It is matched anywhere in a comment so it
// works both trailing the call and on its own line, with or without a space
// after the slashes.
const suppressDirective = "liquid:allow-subscribe"

// suppressedLines is the set of source lines carrying an allow-subscribe
// directive comment, so a finding on that line or the line below it can be
// suppressed.
func suppressedLines(fset *token.FileSet, file *ast.File) map[int]bool {
	lines := make(map[int]bool)
	for _, group := range file.Comments {
		for _, c := range group.List {
			if strings.Contains(c.Text, suppressDirective) {
				lines[fset.Position(c.Slash).Line] = true
			}
		}
	}
	return lines
}

// discardedCalls collects the call expressions in a file whose result is
// thrown away outright: a bare expression statement, the call in a go/defer
// statement, or an assignment entirely to blanks. A Subscribe among these can
// never have its cancel invoked, which is the provable-leak case.
func discardedCalls(file *ast.File) map[*ast.CallExpr]bool {
	discarded := make(map[*ast.CallExpr]bool)
	mark := func(e ast.Expr) {
		if c, ok := e.(*ast.CallExpr); ok {
			discarded[c] = true
		}
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.ExprStmt:
			mark(s.X)
		case *ast.GoStmt:
			discarded[s.Call] = true
		case *ast.DeferStmt:
			discarded[s.Call] = true
		case *ast.AssignStmt:
			if allBlank(s.Lhs) {
				for _, rhs := range s.Rhs {
					mark(rhs)
				}
			}
		}
		return true
	})
	return discarded
}

// allBlank reports whether every expression is the blank identifier — an
// assignment target that discards its value.
func allBlank(exprs []ast.Expr) bool {
	for _, e := range exprs {
		id, ok := e.(*ast.Ident)
		if !ok || id.Name != "_" {
			return false
		}
	}
	return len(exprs) > 0
}

// isCoreSubscribe reports whether a selector call resolves to the Subscribe
// method declared on a type in the Liquid core package — an observable's
// Subscribe, not an unrelated same-named method on an application type.
func (f *Facts) isCoreSubscribe(sel *ast.SelectorExpr) bool {
	info := f.pkg.TypesInfo
	obj := info.Uses[sel.Sel]
	if s := info.Selections[sel]; s != nil {
		obj = s.Obj()
	}
	fn, ok := obj.(*types.Func)
	if !ok || fn.Pkg() == nil {
		return false
	}
	return fn.Pkg().Path() == liquidCorePath
}
