package liquid

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"reflect"
	"strings"
	"text/template/parse"
)

// maxNestingDepth caps recursive child rendering so a self-referencing
// component composition fails with a render error instead of unbounded
// recursion. Build-time cycle detection across templates is a later slice;
// this is the runtime backstop.
const maxNestingDepth = 32

// childFuncName is the template function child-selector elements compile to
// (D14): `<app-user-card [name]="Owner">` becomes
// `{{liquidChild "app-user-card" "name" .Owner}}`.
const childFuncName = "liquidChild"

// registrationStubFuncs makes templates referencing liquidChild parseable at
// registration. The stub is never executed: a template that uses children is
// re-parsed per render with the render scope's live child func attached.
var registrationStubFuncs = template.FuncMap{
	childFuncName: func(string, ...any) (template.HTML, error) {
		return "", errors.New("liquid: child render outside a render scope")
	},
}

// renderScope is one render pass's context for resolving child components:
// the App whose registry selectors resolve against, the session the render
// binds to (established lazily — a purely static composition never mints
// one), and the recursion depth.
type renderScope struct {
	a *App
	// ensure returns the render's session ID, establishing the session on
	// first use.
	ensure func() (string, error)
	depth  int
}

// executeComponent renders one component instance. Templates that never
// nest children execute their registration-parsed template directly; a
// child-bearing template is parsed per render so its liquidChild calls bind
// to this render's scope (the parse cannot newly fail — the same text
// already parsed at registration).
func (a *App) executeComponent(reg *registration, inst reflect.Value, sc *renderScope) (string, error) {
	tmpl := reg.tmpl
	if reg.hasChildren {
		var err error
		tmpl, err = template.New(reg.selector).Funcs(template.FuncMap{childFuncName: sc.child}).Parse(reg.tmplText)
		if err != nil {
			return "", fmt.Errorf("parsing template for %s: %w", reg.selector, err)
		}
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, inst.Interface()); err != nil {
		return "", fmt.Errorf("rendering %s: %w", reg.selector, err)
	}
	return buf.String(), nil
}

// child renders one nested component occurrence: resolve the selector from
// the registry, instantiate a fresh child, copy the [input]-bound values
// into its fields, and render it recursively (D14). args alternate field
// name and value, as compiled from the element's [input] attributes.
func (sc *renderScope) child(selector string, args ...any) (template.HTML, error) {
	if sc.depth >= maxNestingDepth {
		return "", fmt.Errorf("rendering %s: component nesting deeper than %d levels; is the composition cyclic?", selector, maxNestingDepth)
	}
	reg, ok := sc.a.components[selector]
	if !ok {
		return "", fmt.Errorf("no component registered for selector %s; App.Register it before routing its parents", selector)
	}
	if len(args)%2 != 0 {
		return "", fmt.Errorf("rendering %s: liquidChild takes (field, value) pairs, got %d arguments", selector, len(args))
	}

	inst := reg.newInstance()
	for i := 0; i < len(args); i += 2 {
		name, ok := args[i].(string)
		if !ok {
			return "", fmt.Errorf("rendering %s: liquidChild field name must be a string, got %T", selector, args[i])
		}
		if err := setChildInput(inst.Elem(), name, args[i+1]); err != nil {
			return "", fmt.Errorf("rendering %s: %w", selector, err)
		}
	}

	// A child declaring its own [hydroId] gets its own session entry and
	// patch boundary; a plain child just renders — it rides its interactive
	// ancestor's patch (D14). Either kind of plumbing binds the render to
	// the session.
	var sessionID, hydroID string
	if reg.hydroField >= 0 || reg.csrfField >= 0 {
		var err error
		if sessionID, err = sc.ensure(); err != nil {
			return "", fmt.Errorf("rendering %s: establishing session: %w", selector, err)
		}
	}
	if reg.hydroField >= 0 {
		var err error
		if hydroID, err = randomToken(); err != nil {
			return "", fmt.Errorf("rendering %s: %w", selector, err)
		}
		inst.Elem().Field(reg.hydroField).SetString(hydroID)
	}
	if reg.csrfField >= 0 {
		inst.Elem().Field(reg.csrfField).SetString(
			mintCSRF(sc.a.csrfSecret, sessionID, sc.a.limits.SessionIdleTimeout, sc.a.now()))
	}

	out, err := sc.a.executeComponent(reg, inst, &renderScope{a: sc.a, ensure: sc.ensure, depth: sc.depth + 1})
	if err != nil {
		return "", fmt.Errorf("rendering child %s: %w", selector, err)
	}
	if reg.hydroField >= 0 {
		sc.a.registerHydro(sessionID, hydroID, inst, reg)
	}
	return template.HTML(out), nil
}

// setChildInput copies one [input]-bound value into the child field matching
// name. The match is case-insensitive — HTML lowercases attribute names, so
// `[userId]` reaches the child as "userid" — and the value must be
// assignable to the field, mirroring the build-time LSX013 check.
func setChildInput(structV reflect.Value, name string, val any) error {
	sf, ok := structV.Type().FieldByNameFunc(func(fieldName string) bool {
		return strings.EqualFold(fieldName, name)
	})
	if !ok {
		return fmt.Errorf("no field matches [input] binding %q on %s", name, structV.Type().Name())
	}
	f := structV.FieldByIndex(sf.Index)
	if !f.CanSet() {
		return fmt.Errorf("[input] binding %q: field %s.%s is not settable", name, structV.Type().Name(), sf.Name)
	}
	rv := reflect.ValueOf(val)
	if !rv.IsValid() {
		return nil // a nil parent value leaves the child field zero
	}
	if !rv.Type().AssignableTo(f.Type()) {
		return fmt.Errorf("[input] binding %q: cannot assign %s to %s.%s (%s)",
			name, rv.Type(), structV.Type().Name(), sf.Name, f.Type())
	}
	f.Set(rv)
	return nil
}

// usesLiquidChild reports whether a parsed template calls liquidChild
// anywhere, deciding whether renders must re-parse against a live scope.
func usesLiquidChild(n parse.Node) bool {
	switch n := n.(type) {
	case *parse.IdentifierNode:
		return n.Ident == childFuncName
	case *parse.ListNode:
		return listUses(n)
	case *parse.ActionNode:
		return pipeUses(n.Pipe)
	case *parse.PipeNode:
		return pipeUses(n)
	case *parse.IfNode:
		return branchUses(n.BranchNode)
	case *parse.RangeNode:
		return branchUses(n.BranchNode)
	case *parse.WithNode:
		return branchUses(n.BranchNode)
	case *parse.TemplateNode:
		return pipeUses(n.Pipe)
	}
	return false
}

// pipeUses checks every command argument of a pipeline.
func pipeUses(p *parse.PipeNode) bool {
	if p == nil {
		return false
	}
	for _, cmd := range p.Cmds {
		for _, arg := range cmd.Args {
			if usesLiquidChild(arg) {
				return true
			}
		}
	}
	return false
}

// listUses checks every node of a body list.
func listUses(l *parse.ListNode) bool {
	if l == nil {
		return false
	}
	for _, n := range l.Nodes {
		if usesLiquidChild(n) {
			return true
		}
	}
	return false
}

// branchUses checks a branching node's pipe and both bodies.
func branchUses(b parse.BranchNode) bool {
	return pipeUses(b.Pipe) || listUses(b.List) || listUses(b.ElseList)
}
