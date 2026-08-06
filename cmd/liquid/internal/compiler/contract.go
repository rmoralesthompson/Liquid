package compiler

// This file implements the D30 payload-contract discovery the compiler shares
// across code generation, the manifest, and vet: for each action it resolves
// the boundary guard and the closed-domain constraints its payload declares.
// Both axes anchor on the guard's payload struct — the only per-action payload
// type v0.1 exposes to the compiler — so guard presence and const-set
// enumeration are computed together here, once, over the go/types view (the
// one-grammar guarantee: seam, manifest and vet cannot disagree).

import (
	"context"
	"fmt"
	"go/constant"
	"go/types"
	"maps"
	"slices"
	"strings"
)

// ActionContract is the compiled D30 payload contract for one action: whether
// it declares a boundary guard, and the closed-domain constraints the dispatch
// seam enforces on the guard's payload fields.
type ActionContract struct {
	// Guard reports a <Name>Guard convention method — the boundary predicate
	// the seam runs before the handler.
	Guard bool
	// Domains maps a payload field (named as declared) to the enumerated
	// const-set values the seam admits, sorted and de-duplicated; empty when no
	// field is closed-domain.
	Domains map[string][]string
}

// ActionContracts returns the payload contract for each named action method on
// structName. A contract is anchored on the action's <Name>Guard method: the
// method's presence is the guard axis, and any const-set-typed field of its
// payload struct is a closed domain (D30). An action with no guard, or whose
// guard is not the pure-predicate shape, has no entry.
func (f *Facts) ActionContracts(structName string, actionNames []string) map[string]ActionContract {
	out := make(map[string]ActionContract)
	tn := f.lookupType(structName)
	if tn == nil {
		return out
	}
	named, ok := tn.Type().(*types.Named)
	if !ok {
		return out
	}
	methods := make(map[string]*types.Func, named.NumMethods())
	for i := range named.NumMethods() {
		fn := named.Method(i)
		methods[fn.Name()] = fn
	}
	for _, name := range actionNames {
		guard, ok := methods[name+"Guard"]
		if !ok {
			continue
		}
		payload := guardPayload(guard)
		if payload == nil {
			// A <Name>Guard of the wrong shape is a broken contract the runtime
			// rejects at registration; it is not a valid guard to report here.
			continue
		}
		out[name] = ActionContract{Guard: true, Domains: f.closedDomains(payload)}
	}
	return out
}

// guardPayload returns the payload struct of a D30 guard — the parameter type
// of a method shaped func(p <struct>) bool — or nil when the method is not
// that shape.
func guardPayload(guard *types.Func) *types.Struct {
	sig, ok := guard.Type().(*types.Signature)
	if !ok || sig.Params().Len() != 1 || sig.Results().Len() != 1 {
		return nil
	}
	if b, ok := sig.Results().At(0).Type().Underlying().(*types.Basic); !ok || b.Kind() != types.Bool {
		return nil
	}
	st, _ := sig.Params().At(0).Type().Underlying().(*types.Struct)
	return st
}

// closedDomains enumerates the closed-domain fields of a guard payload struct:
// each exported field whose type is a named type backed by a package-scope
// const-set (the idiomatic Go enum), mapped to that set's values. Returns nil
// when no field qualifies.
func (f *Facts) closedDomains(payload *types.Struct) map[string][]string {
	var domains map[string][]string
	for i := range payload.NumFields() {
		fld := payload.Field(i)
		if !fld.Exported() {
			continue
		}
		named, ok := fld.Type().(*types.Named)
		if !ok {
			continue
		}
		if _, ok := named.Underlying().(*types.Basic); !ok {
			continue
		}
		values := enumConsts(named)
		if len(values) == 0 {
			continue
		}
		if domains == nil {
			domains = make(map[string][]string)
		}
		domains[fld.Name()] = values
	}
	return domains
}

// enumConsts returns the wire-string forms of every package-scope constant
// whose type is exactly named — the closed const-set — sorted and de-duplicated
// for a deterministic contract. A named type with no such constant yields nil,
// so it is not treated as a closed domain.
func enumConsts(named *types.Named) []string {
	pkg := named.Obj().Pkg()
	if pkg == nil {
		return nil
	}
	scope := pkg.Scope()
	var values []string
	for _, n := range scope.Names() {
		c, ok := scope.Lookup(n).(*types.Const)
		if !ok || !types.Identical(c.Type(), named.Obj().Type()) {
			continue
		}
		values = append(values, constString(c.Val()))
	}
	slices.Sort(values)
	return slices.Compact(values)
}

// constString renders a constant as the string the seam compares a posted
// payload value against: a string constant verbatim (its unquoted value), any
// other constant its exact Go literal.
func constString(v constant.Value) string {
	if v.Kind() == constant.String {
		return constant.StringVal(v)
	}
	return v.ExactString()
}

// payloadDomainsLiteral renders the Go map literal for a component's generated
// PayloadDomains method (D30): action name → case-folded payload field →
// admitted value set. It returns "" when no action declares a closed domain,
// so the compiler emits the method only for a component that needs it. Field
// keys are lower-cased to match the seam's case-insensitive payload fold (D11);
// actions, fields and values are sorted so regeneration is byte-stable.
func payloadDomainsLiteral(ctx context.Context, dir, structName string, actionNames []string) string {
	facts, err := LoadFacts(ctx, dir)
	if err != nil || facts.Broken() {
		return ""
	}
	contracts := facts.ActionContracts(structName, actionNames)
	var names []string
	for name, c := range contracts {
		if len(c.Domains) > 0 {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return ""
	}
	slices.Sort(names)

	var b strings.Builder
	b.WriteString("map[string]map[string][]string{\n")
	for _, name := range names {
		fmt.Fprintf(&b, "%q: {\n", name)
		domains := contracts[name].Domains
		for _, field := range slices.Sorted(maps.Keys(domains)) {
			fmt.Fprintf(&b, "%q: {%s},\n", strings.ToLower(field), quoteValues(domains[field]))
		}
		b.WriteString("},\n")
	}
	b.WriteString("}")
	return b.String()
}

// quoteValues renders values as a comma-separated list of Go string literals.
func quoteValues(values []string) string {
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = fmt.Sprintf("%q", v)
	}
	return strings.Join(quoted, ", ")
}

// vetActionGuards runs the D30 unguarded-action check once per component
// package under dir: an action that takes a client payload but declares no
// guard earns an LSX018 warning (non-fatal). It joins the template directive
// scan (which actions a component wires) with the package's go/types facts
// (their signatures and guards) — the same join the manifest uses, so vet and
// manifest cannot disagree about which actions are unguarded.
func vetActionGuards(ctx context.Context, dir string) ([]Diagnostic, error) {
	dirs, err := lsxDirs(dir)
	if err != nil {
		return nil, err
	}
	var diags []Diagnostic
	for _, d := range dirs {
		facts, err := LoadFacts(ctx, d)
		if err != nil || facts.Broken() {
			// A package that will not load or type-check has no resolvable
			// signatures to judge; its real problem is reported elsewhere.
			continue
		}
		templates, _, err := scanTemplates(d)
		if err != nil {
			return nil, err
		}
		for _, decl := range facts.SelectorDecls() {
			tmpl, ok := templates[decl.Struct]
			if !ok {
				continue
			}
			diags = append(diags, unguardedActionDiags(facts, decl.Struct, tmpl.sa)...)
		}
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
	return diags, nil
}

// unguardedActionDiags flags each payload-taking action of one component that
// declares no guard (D30 / LSX018), positioned at the handler's declaration —
// where the author adds the guard. An action already reported as a malformed
// handler (LSX008) carries no payload contract to miss, so it is skipped here.
func unguardedActionDiags(facts *Facts, structName string, sa *SourceAnalysis) []Diagnostic {
	actionNames := actionMethodNames(sa)
	if len(actionNames) == 0 {
		return nil
	}
	members := make(map[string]Member)
	for _, m := range facts.Component(structName) {
		members[m.Name] = m
	}
	contracts := facts.ActionContracts(structName, actionNames)
	var diags []Diagnostic
	for _, name := range actionNames {
		m, ok := members[name]
		if !ok || !m.Handler || m.Type == "func()" {
			// Not a payload-taking handler: a bare func() click has no payload
			// to constrain, and a non-handler is LSX008's to report.
			continue
		}
		if contracts[name].Guard {
			continue
		}
		diags = append(diags, Diagnostic{
			File:     m.File,
			Line:     m.Line,
			Col:      m.Col,
			Severity: SeverityWarning,
			Code:     CodeUnguardedAction,
			Message: fmt.Sprintf("action %s takes a client payload but declares no guard, so nothing constrains its payload values at the dispatch seam (D30)",
				name),
			Suggestion: fmt.Sprintf("add func (c *%s) %sGuard(p <Payload>) bool to refuse an out-of-contract payload before the handler runs",
				structName, name),
		})
	}
	return diags
}

// actionMethodNames returns the distinct handler names a template wires through
// (click)/(submit), in first-seen order.
func actionMethodNames(sa *SourceAnalysis) []string {
	seen := make(map[string]bool)
	var names []string
	for _, use := range sa.Directives {
		if use.Name != kindClick && use.Name != kindSubmit {
			continue
		}
		if !seen[use.Expr] {
			seen[use.Expr] = true
			names = append(names, use.Expr)
		}
	}
	return names
}
