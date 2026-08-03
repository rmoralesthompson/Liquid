package compiler

import (
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/tools/go/packages"
)

// maxSuggestionDistance bounds how far a "did you mean" candidate may be
// from the misspelled name, in edit operations.
const maxSuggestionDistance = 2

// vetReferences type-checks the Go package in dir with go/types (D1) and
// verifies that every template interpolation naming a simple identifier
// resolves to a field or method on the paired struct. Expressions that are
// not plain identifiers are left for html/template to judge.
func vetReferences(ctx context.Context, dir, lsxPath, structName string, interps []structRef) ([]Diagnostic, error) {
	facts, err := LoadFacts(ctx, dir)
	if err != nil {
		return nil, err
	}
	return facts.vetRefs(lsxPath, structName, interps), nil
}

// loadPackage loads and type-checks the single Go package in dir.
func loadPackage(ctx context.Context, dir string) (*packages.Package, error) {
	cfg := &packages.Config{
		Context: ctx,
		// NeedSyntax and NeedTypesInfo make packages type-check from source
		// in-process, so type errors carry file:line:col positions instead
		// of arriving as one opaque driver error.
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedImports | packages.NeedDeps | packages.NeedTypes |
			packages.NeedSyntax | packages.NeedTypesInfo,
		Dir: dir,
	}
	pkgs, err := packages.Load(cfg, ".")
	if err != nil {
		return nil, fmt.Errorf("loading package in %s: %w", dir, err)
	}
	if len(pkgs) != 1 {
		return nil, fmt.Errorf("loading package in %s: got %d packages, want exactly 1", dir, len(pkgs))
	}
	return pkgs[0], nil
}

// vetRefs runs the reference cross-check for one template against the
// loaded package: broken package (LSX007), missing paired struct (LSX003),
// then each reference's kind-specific rule.
func (f *Facts) vetRefs(lsxPath, structName string, interps []structRef) []Diagnostic {
	pkg := f.pkg
	if len(pkg.Errors) > 0 {
		return brokenPackageDiags(lsxPath, pkg.Errors)
	}

	base := filepath.Base(lsxPath)
	tn, ok := pkg.Types.Scope().Lookup(structName).(*types.TypeName)
	if !ok {
		return []Diagnostic{{
			File:     lsxPath,
			Line:     1,
			Col:      1,
			Severity: SeverityError,
			Code:     CodeMissingPairedStruct,
			Message: fmt.Sprintf("package %s does not define a struct named %s to pair with %s",
				pkg.Types.Name(), structName, base),
			Suggestion: fmt.Sprintf("define type %s struct in %s, or rename the files to match an existing struct",
				structName, strings.TrimSuffix(base, ".lsx")+".go"),
		}}
	}

	v := &vetter{lsxPath: lsxPath, structName: structName, pkg: pkg, pairedType: tn.Type()}
	var diags []Diagnostic
	for _, in := range interps {
		diags = append(diags, v.check(in)...)
	}
	return diags
}

// vetter cross-checks one template's references against its paired struct
// and, for child refs, the package's other components.
type vetter struct {
	lsxPath    string
	structName string
	pkg        *packages.Package
	pairedType types.Type
	selectors  map[string]string // built on first child ref
}

// selectorTable lazily maps the package's declared selectors.
func (v *vetter) selectorTable() map[string]string {
	if v.selectors == nil {
		v.selectors = packageSelectors(v.pkg)
	}
	return v.selectors
}

// check applies the reference kind's rule.
func (v *vetter) check(in structRef) []Diagnostic {
	switch in.kind {
	case refChildTag:
		return oneDiag(checkChildSelector(v.lsxPath, in, v.selectorTable(), v.pkg.Types.Name()))
	case refInput:
		return v.checkInputBinding(in)
	case refHydroRoot:
		return oneDiag(checkHydroField(v.lsxPath, v.structName, in, v.pairedType, v.pkg.Types))
	case refCSRFRoot:
		return oneDiag(checkCSRFField(v.lsxPath, v.structName, in, v.pairedType, v.pkg.Types))
	case refDefer:
		return v.checkDeferTarget(in)
	default:
		return v.checkFieldRef(in)
	}
}

// checkFieldRef verifies a plain field-or-method reference (LSX004), holding
// event-handler targets to the dispatchable shapes (LSX008).
func (v *vetter) checkFieldRef(in structRef) []Diagnostic {
	name := strings.TrimPrefix(in.expr, ".")
	if !isSimpleIdent(name) {
		return nil
	}
	if member, _, _ := types.LookupFieldOrMethod(v.pairedType, true, v.pkg.Types, name); member != nil {
		if in.kind == refAction {
			return oneDiag(checkHandler(v.lsxPath, v.structName, in, member))
		}
		return nil
	}
	return []Diagnostic{unknownReferenceDiag(v.lsxPath, v.structName, name, in.pos, v.pairedType)}
}

// oneDiag lifts an optional diagnostic into a slice.
func oneDiag(d *Diagnostic) []Diagnostic {
	if d == nil {
		return nil
	}
	return []Diagnostic{*d}
}

// unknownReferenceDiag builds the LSX004 for a reference that resolves to
// nothing on the paired struct, with a "did you mean" when a member is close.
func unknownReferenceDiag(lsxPath, structName, name string, at pos, pairedType types.Type) Diagnostic {
	var suggestion string
	if near := nearestMember(pairedType, name); near != "" {
		suggestion = fmt.Sprintf("did you mean %s?", near)
	}
	return Diagnostic{
		File:       lsxPath,
		Line:       at.line,
		Col:        at.col,
		Severity:   SeverityError,
		Code:       CodeUnknownReference,
		Message:    fmt.Sprintf("%s has no field or method named %s", structName, name),
		Suggestion: suggestion,
	}
}

// checkDeferTarget verifies a deferred component carries the HydroID field
// its completion patch swaps at (LSX016). An unknown selector yields nothing
// here — its element already carries the LSX012.
func (v *vetter) checkDeferTarget(in structRef) []Diagnostic {
	childName, ok := v.selectorTable()[in.sel]
	if !ok {
		return nil
	}
	childTN, ok := v.pkg.Types.Scope().Lookup(childName).(*types.TypeName)
	if !ok {
		return nil
	}
	if hasStringField(childTN.Type(), v.pkg.Types, "HydroID") {
		return nil
	}
	return []Diagnostic{{
		File:     v.lsxPath,
		Line:     in.line,
		Col:      in.col,
		Severity: SeverityError,
		Code:     CodeMissingDeferHydroField,
		Message: fmt.Sprintf("%s is deferred but %s has no HydroID string field for the swap to target",
			in.sel, childName),
		Suggestion: fmt.Sprintf("add HydroID string to the %s struct", childName),
	}}
}

// checkInputBinding verifies one [input] binding end to end: the expression
// resolves on the parent struct (LSX004), the bound name is a field on the
// child struct, and the parent value is assignable to it (both LSX013,
// mirroring the runtime copy's rules). An unknown child selector yields
// nothing here — its element already carries the LSX012.
func (v *vetter) checkInputBinding(in structRef) []Diagnostic {
	childName, ok := v.selectorTable()[in.sel]
	if !ok {
		return nil
	}
	childTN, ok := v.pkg.Types.Scope().Lookup(childName).(*types.TypeName)
	if !ok {
		return nil
	}
	childStruct, ok := childTN.Type().Underlying().(*types.Struct)
	if !ok {
		return nil
	}

	childField := inputField(childStruct, in.binding)
	if childField == nil {
		var candidates []string
		for i := range childStruct.NumFields() {
			candidates = append(candidates, childStruct.Field(i).Name())
		}
		var suggestion string
		if near := nearestFold(candidates, in.binding); near != "" {
			suggestion = fmt.Sprintf("did you mean %s?", near)
		}
		return []Diagnostic{{
			File:       v.lsxPath,
			Line:       in.namePos.line,
			Col:        in.namePos.col,
			Severity:   SeverityError,
			Code:       CodeBadInputBinding,
			Message:    fmt.Sprintf("%s has no field named %s for the [input] binding", childName, in.binding),
			Suggestion: suggestion,
		}}
	}

	// Loop-variable expressions ($item.Name) and dotted paths resolve at
	// render time; only a simple identifier is checked against the parent.
	if strings.HasPrefix(in.expr, "$") || !isSimpleIdent(in.expr) {
		return nil
	}
	member, _, _ := types.LookupFieldOrMethod(v.pairedType, true, v.pkg.Types, in.expr)
	if member == nil {
		return []Diagnostic{unknownReferenceDiag(v.lsxPath, v.structName, in.expr, in.pos, v.pairedType)}
	}
	parentFieldType := memberValueType(member)
	if parentFieldType == nil || types.AssignableTo(parentFieldType, childField.Type()) {
		return nil
	}
	qualifier := func(p *types.Package) string { return p.Name() }
	return []Diagnostic{{
		File:     v.lsxPath,
		Line:     in.line,
		Col:      in.col,
		Severity: SeverityError,
		Code:     CodeBadInputBinding,
		Message: fmt.Sprintf("[input] %s: cannot assign %s.%s (%s) to %s.%s (%s)",
			in.binding, v.structName, in.expr, types.TypeString(parentFieldType, qualifier),
			childName, childField.Name(), types.TypeString(childField.Type(), qualifier)),
		Suggestion: fmt.Sprintf("bind a %s field assignable to %s", v.structName, types.TypeString(childField.Type(), qualifier)),
	}}
}

// inputField finds the child struct field an [input] binding names. HTML
// lowercases attribute names, so the match is case-insensitive — the same
// rule the runtime copy applies.
func inputField(s *types.Struct, name string) *types.Var {
	for i := range s.NumFields() {
		if f := s.Field(i); f.Exported() && strings.EqualFold(f.Name(), name) {
			return f
		}
	}
	return nil
}

// memberValueType is the type an [input] expression produces: a field's own
// type, or a nullary single-result method's result. Anything else has no
// statically checkable type here.
func memberValueType(member types.Object) types.Type {
	switch m := member.(type) {
	case *types.Var:
		return m.Type()
	case *types.Func:
		sig := m.Type().(*types.Signature)
		if sig.Params().Len() == 0 && sig.Results().Len() == 1 {
			return sig.Results().At(0).Type()
		}
	}
	return nil
}

// nearestFold is nearestString under case folding, for names that reach the
// compiler lowercased (HTML attribute names).
func nearestFold(candidates []string, name string) string {
	lowered := make([]string, len(candidates))
	for i, c := range candidates {
		lowered[i] = strings.ToLower(c)
	}
	best := nearestString(lowered, strings.ToLower(name))
	for i, l := range lowered {
		if l == best {
			return candidates[i]
		}
	}
	return ""
}

// packageSelectors maps each selector declared in the package to its
// component struct name, by reading Selector methods that return a string
// literal. A Selector built from anything but a literal is invisible to the
// build-time registry — its selector cannot resolve.
func packageSelectors(pkg *packages.Package) map[string]string {
	selectors := make(map[string]string)
	for _, file := range pkg.Syntax {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name.Name != "Selector" || fn.Recv == nil || len(fn.Recv.List) != 1 || fn.Body == nil || len(fn.Body.List) != 1 {
				continue
			}
			ret, ok := fn.Body.List[0].(*ast.ReturnStmt)
			if !ok || len(ret.Results) != 1 {
				continue
			}
			lit, ok := ret.Results[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			sel, err := strconv.Unquote(lit.Value)
			if err != nil {
				continue
			}
			selectors[sel] = receiverTypeName(fn.Recv.List[0].Type)
		}
	}
	return selectors
}

// receiverTypeName unwraps a method receiver's type expression to its named
// type ("*UserCard" → "UserCard").
func receiverTypeName(expr ast.Expr) string {
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	if ident, ok := expr.(*ast.Ident); ok {
		return ident.Name
	}
	return ""
}

// checkChildSelector verifies a child-selector element names a component the
// package declares, or reports an LSX012 (D14).
func checkChildSelector(lsxPath string, in structRef, selectors map[string]string, pkgName string) *Diagnostic {
	if _, ok := selectors[in.expr]; ok {
		return nil
	}
	var suggestion string
	if near := nearestString(slices.Collect(maps.Keys(selectors)), in.expr); near != "" {
		suggestion = fmt.Sprintf("did you mean %s?", near)
	}
	return &Diagnostic{
		File:       lsxPath,
		Line:       in.line,
		Col:        in.col,
		Severity:   SeverityError,
		Code:       CodeUnknownChildSelector,
		Message:    fmt.Sprintf("no component in package %s declares the selector %s", pkgName, in.expr),
		Suggestion: suggestion,
	}
}

// checkHydroField verifies that a component declaring [hydroId] carries the
// HydroID field the framework fills at render: a field (not a method) whose
// underlying type is string. Anything else is an LSX009, or nil when the
// plumbing is in place.
func checkHydroField(lsxPath, structName string, in structRef, pairedType types.Type, pkg *types.Package) *Diagnostic {
	if hasStringField(pairedType, pkg, "HydroID") {
		return nil
	}
	return &Diagnostic{
		File:     lsxPath,
		Line:     in.line,
		Col:      in.col,
		Severity: SeverityError,
		Code:     CodeMissingHydroField,
		Message: fmt.Sprintf("%s uses [hydroId] but has no HydroID string field for the framework to fill",
			structName),
		Suggestion: fmt.Sprintf("add HydroID string to the %s struct", structName),
	}
}

// checkCSRFField verifies that a component whose template has a <form>
// carries the CSRFToken field the framework fills at render (D15): a field
// (not a method) whose underlying type is string. Anything else is an
// LSX011, or nil when the plumbing is in place.
func checkCSRFField(lsxPath, structName string, in structRef, pairedType types.Type, pkg *types.Package) *Diagnostic {
	if hasStringField(pairedType, pkg, "CSRFToken") {
		return nil
	}
	return &Diagnostic{
		File:     lsxPath,
		Line:     in.line,
		Col:      in.col,
		Severity: SeverityError,
		Code:     CodeMissingCSRFField,
		Message: fmt.Sprintf("%s has a <form> but no CSRFToken string field for the framework to fill",
			structName),
		Suggestion: fmt.Sprintf("add CSRFToken string to the %s struct", structName),
	}
}

// hasStringField reports whether t carries a field (not a method) named name
// whose underlying type is string.
func hasStringField(t types.Type, pkg *types.Package, name string) bool {
	member, _, _ := types.LookupFieldOrMethod(t, true, pkg, name)
	v, ok := member.(*types.Var)
	if !ok {
		return false
	}
	basic, ok := v.Type().Underlying().(*types.Basic)
	return ok && basic.Kind() == types.String
}

// checkHandler verifies that an event-binding target is a dispatchable
// handler: a method shaped func() or func(e liquid.Event), the two shapes
// D11 allows. Anything else is an LSX008, or nil when the handler is fine.
func checkHandler(lsxPath, structName string, in structRef, member types.Object) *Diagnostic {
	d := &Diagnostic{
		File:     lsxPath,
		Line:     in.line,
		Col:      in.col,
		Severity: SeverityError,
		Code:     CodeInvalidHandler,
		Suggestion: fmt.Sprintf("change the method to func (c *%s) %s() or func (c *%s) %s(e liquid.Event)",
			structName, in.expr, structName, in.expr),
	}
	fn, ok := member.(*types.Func)
	if !ok {
		d.Message = fmt.Sprintf("%s handler %s is a field, not a method", in.binding, in.expr)
		d.Suggestion = fmt.Sprintf("add a method func (c *%s) %s() and bind that instead", structName, in.expr)
		return d
	}
	sig := fn.Type().(*types.Signature)
	if isHandlerSig(sig) {
		return nil
	}
	// Qualify types by bare package name so the signature reads as the
	// author wrote it (liquid.Event), not as a full import path.
	d.Message = fmt.Sprintf("%s handler %s has signature %s; a handler is func() or func(e liquid.Event) (D11)",
		in.binding, in.expr, types.TypeString(fn.Type(), func(p *types.Package) string { return p.Name() }))
	return d
}

// isHandlerSig reports whether sig is one of the two dispatchable handler
// shapes D11 allows: func() or func(e liquid.Event).
func isHandlerSig(sig *types.Signature) bool {
	if sig.Results().Len() != 0 {
		return false
	}
	switch sig.Params().Len() {
	case 0:
		return true
	case 1:
		return isLiquidEvent(sig.Params().At(0).Type())
	}
	return false
}

// liquidCorePath is the import path liquid.Event resolves at; handler
// signatures are matched against it by path, not by loading the package here.
const liquidCorePath = "github.com/rmoralesthompson/liquid/core"

// isLiquidEvent reports whether t is the liquid.Event payload type (D11).
func isLiquidEvent(t types.Type) bool {
	n, ok := t.(*types.Named)
	if !ok {
		return false
	}
	obj := n.Obj()
	return obj.Name() == "Event" && obj.Pkg() != nil && obj.Pkg().Path() == liquidCorePath
}

// brokenPackageDiags translates go/types errors from the paired package into
// D13 diagnostics, keeping the agent-facing contract structured even when
// the Go half of a component does not compile. Errors without a usable
// position fall back to the template file at 1:1.
func brokenPackageDiags(lsxPath string, errs []packages.Error) []Diagnostic {
	var diags []Diagnostic
	for _, e := range errs {
		file, line, col := parsePos(e.Pos)
		if file == "" {
			file, line, col = lsxPath, 1, 1
		}
		diags = append(diags, Diagnostic{
			File:       file,
			Line:       line,
			Col:        col,
			Severity:   SeverityError,
			Code:       CodeBrokenPairedPackage,
			Message:    e.Msg,
			Suggestion: "fix the Go type errors in the paired package, then rerun liquid build",
		})
	}
	return diags
}

// parsePos splits a packages.Error position ("file:line:col", "file:line",
// "file", "-", or "") into its parts, defaulting line and col to 1.
func parsePos(pos string) (file string, line, col int) {
	if pos == "" || pos == "-" {
		return "", 1, 1
	}
	file, line, col = pos, 1, 1
	rest, last, ok := cutLastInt(file)
	if !ok {
		return file, line, col
	}
	if rest2, second, ok := cutLastInt(rest); ok {
		return rest2, second, last
	}
	return rest, last, 1
}

// cutLastInt splits a trailing ":<number>" off s.
func cutLastInt(s string) (rest string, n int, ok bool) {
	i := strings.LastIndex(s, ":")
	if i < 0 {
		return s, 0, false
	}
	n, err := strconv.Atoi(s[i+1:])
	if err != nil {
		return s, 0, false
	}
	return s[:i], n, true
}

// isSimpleIdent reports whether s is a plain Go identifier.
func isSimpleIdent(s string) bool {
	for i, r := range s {
		if unicode.IsLetter(r) || r == '_' || (i > 0 && unicode.IsDigit(r)) {
			continue
		}
		return false
	}
	return s != ""
}

// nearestMember returns the field or method of t closest to name within
// maxSuggestionDistance edits, or "" when nothing is close enough.
func nearestMember(t types.Type, name string) string {
	var candidates []string
	if s, ok := t.Underlying().(*types.Struct); ok {
		for i := range s.NumFields() {
			candidates = append(candidates, s.Field(i).Name())
		}
	}
	if n, ok := t.(*types.Named); ok {
		for i := range n.NumMethods() {
			candidates = append(candidates, n.Method(i).Name())
		}
	}
	return nearestString(candidates, name)
}

// nearestString returns the candidate closest to name within
// maxSuggestionDistance edits, or "" when nothing is close enough.
func nearestString(candidates []string, name string) string {
	best, bestDist := "", maxSuggestionDistance+1
	for _, c := range candidates {
		if d := editDistance(name, c); d < bestDist {
			best, bestDist = c, d
		}
	}
	return best
}

// editDistance is the Levenshtein distance between a and b.
func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}
