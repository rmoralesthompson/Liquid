// Package compiler implements the Liquid AOT template compiler. It pairs
// .lsx templates with their component source files by filename convention
// (user_card.lsx ↔ user_card.go defining UserCard) and emits html/template
// markup as *_gen.go files beside the source.
package compiler

import (
	"context"
	"fmt"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Build compiles every .lsx file under dir, writing a <name>_gen.go file
// beside each one containing the paired component's Template method. Problems
// in the input the user can fix come back as diagnostics (no generated file is
// written for a .lsx with an error diagnostic); the error covers mechanical
// failures such as unreadable directories.
func Build(ctx context.Context, dir string) ([]Diagnostic, error) {
	diags, err := eachLSX(dir, func(path string) ([]Diagnostic, error) {
		return compileFile(ctx, path)
	})
	if err != nil {
		return nil, err
	}
	leaks, err := vetSubscriptions(ctx, dir)
	if err != nil {
		return nil, err
	}
	unguarded, err := vetActionGuards(ctx, dir)
	if err != nil {
		return nil, err
	}
	return append(append(diags, leaks...), unguarded...), nil
}

// Vet runs the same diagnostic checks as Build on every .lsx file under dir
// without writing any generated files (D13).
func Vet(ctx context.Context, dir string) ([]Diagnostic, error) {
	diags, err := eachLSX(dir, func(path string) ([]Diagnostic, error) {
		diags, _, err := analyzeFile(ctx, path)
		return diags, err
	})
	if err != nil {
		return nil, err
	}
	leaks, err := vetSubscriptions(ctx, dir)
	if err != nil {
		return nil, err
	}
	unguarded, err := vetActionGuards(ctx, dir)
	if err != nil {
		return nil, err
	}
	return append(append(diags, leaks...), unguarded...), nil
}

// vetSubscriptions runs the D29 reactivity-leak check (VetSubscriptions) once
// per package that has a template under dir, so a leaky Subscribe is reported
// exactly once regardless of how many .lsx files sit beside it.
func vetSubscriptions(ctx context.Context, dir string) ([]Diagnostic, error) {
	dirs, err := lsxDirs(dir)
	if err != nil {
		return nil, err
	}
	var diags []Diagnostic
	for _, d := range dirs {
		facts, err := LoadFacts(ctx, d)
		if err != nil {
			// Best-effort (D29): a directory that will not even load — a bare
			// template with no Go module — has no checkable subscriptions, and
			// its template gating already reports the real problem.
			continue
		}
		diags = append(diags, facts.VetSubscriptions()...)
	}
	return diags, nil
}

// lsxDirs returns the distinct directories under dir that contain at least one
// .lsx file, sorted for deterministic output — one per component package.
func lsxDirs(dir string) ([]string, error) {
	seen := make(map[string]bool)
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".lsx") {
			seen[filepath.Dir(path)] = true
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scanning %s for .lsx files: %w", dir, err)
	}
	dirs := slices.Collect(maps.Keys(seen))
	slices.Sort(dirs)
	return dirs, nil
}

// eachLSX applies fn to every .lsx file under dir, collecting diagnostics.
func eachLSX(dir string, fn func(path string) ([]Diagnostic, error)) ([]Diagnostic, error) {
	var lsxFiles []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".lsx") {
			lsxFiles = append(lsxFiles, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scanning %s for .lsx files: %w", dir, err)
	}

	var diags []Diagnostic
	for _, path := range lsxFiles {
		ds, err := fn(path)
		if err != nil {
			return nil, fmt.Errorf("checking %s: %w", path, err)
		}
		diags = append(diags, ds...)
	}
	return diags, nil
}

// analyzeFile runs the diagnostic checks for one .lsx file: the pairing
// convention (LSX002), interpolation syntax (LSX001), and the go/types
// reference cross-check (LSX003, LSX004). It returns the raw template source
// so compileFile can reuse it without a second read.
func analyzeFile(ctx context.Context, lsxPath string) ([]Diagnostic, []byte, error) {
	structName := PairedStructName(lsxPath)

	if d := PairingDiagnostic(lsxPath); d != nil {
		return []Diagnostic{*d}, nil, nil
	}

	src, err := os.ReadFile(lsxPath)
	if err != nil {
		return nil, nil, fmt.Errorf("reading template: %w", err)
	}

	interps, diags := scanInterpolations(lsxPath, src)
	if len(diags) > 0 {
		return diags, nil, nil
	}

	dirs := scanDirectives(src)
	if diags := checkDirectives(lsxPath, dirs); len(diags) > 0 {
		return diags, nil, nil
	}
	interps = append(interps, directiveRefs(dirs)...)

	vetDiags, err := vetReferences(ctx, filepath.Dir(lsxPath), lsxPath, structName, interps)
	if err != nil {
		return nil, nil, fmt.Errorf("vetting: %w", err)
	}
	return vetDiags, src, nil
}

// compileFile compiles one .lsx file and writes its paired *_gen.go, unless
// the file has diagnostics, which it returns instead.
func compileFile(ctx context.Context, lsxPath string) ([]Diagnostic, error) {
	diags, src, err := analyzeFile(ctx, lsxPath)
	if err != nil {
		return nil, err
	}
	if len(diags) > 0 {
		return diags, nil
	}

	base := strings.TrimSuffix(lsxPath, ".lsx")
	structName := pascalCase(filepath.Base(base))

	pkg, err := packageName(base + ".go")
	if err != nil {
		return nil, fmt.Errorf("resolving paired source %s: %w", base+".go", err)
	}

	compiled, actions, err := compileLSX(src)
	if err != nil {
		return nil, err
	}

	generated := fmt.Sprintf(`// Code generated by liquid build; DO NOT EDIT.

package %s

// Template returns the compiled .lsx markup for %s.
func (c *%s) Template() string {
	return %q
}
`, pkg, structName, structName, compiled)
	if len(actions) > 0 {
		generated += fmt.Sprintf(`
// Actions returns the action allowlist compiled from %s's event
// bindings (D10); the server dispatches only these.
func (c *%s) Actions() []string {
	return []string{%s}
}
`, structName, structName, quoteAll(actions))
	}

	if domains := payloadDomainsLiteral(ctx, filepath.Dir(lsxPath), structName, actions); domains != "" {
		generated += fmt.Sprintf(`
// PayloadDomains returns the closed-domain payload constraints compiled from
// %s's action guards (D30): per action, the payload field mapped to the
// enumerated value set the dispatch seam admits. Reflection cannot see a Go
// const-set, so the seam reads it from here.
func (c *%s) PayloadDomains() map[string]map[string][]string {
	return %s
}
`, structName, structName, domains)
	}

	formatted, err := format.Source([]byte(generated))
	if err != nil {
		return nil, fmt.Errorf("formatting generated code: %w", err)
	}

	genPath := base + "_gen.go"
	if err := os.WriteFile(genPath, formatted, 0o644); err != nil {
		return nil, fmt.Errorf("writing %s: %w", genPath, err)
	}
	return nil, nil
}

// compileLSX parses .lsx markup into an HTML node tree, applies the template
// transforms, and renders the result as html/template text plus the action
// allowlist collected from event bindings (D10).
func compileLSX(src []byte) (string, []string, error) {
	ctx := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
	nodes, err := html.ParseFragment(strings.NewReader(string(src)), ctx)
	if err != nil {
		return "", nil, fmt.Errorf("parsing .lsx markup: %w", err)
	}

	// Reparent the fragment nodes under a container so structural directives
	// can insert control-flow siblings around any element, including
	// top-level ones.
	container := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
	for _, n := range nodes {
		if n.Parent != nil {
			n.Parent.RemoveChild(n)
		}
		container.AppendChild(n)
	}
	var actions []string
	if err := transform(container, &actions); err != nil {
		return "", nil, err
	}

	var b strings.Builder
	for n := container.FirstChild; n != nil; n = n.NextSibling {
		if err := html.Render(&b, n); err != nil {
			return "", nil, fmt.Errorf("rendering compiled template: %w", err)
		}
	}
	return strings.TrimRight(b.String(), "\n"), actions, nil
}

// transform rewrites Liquid template sugar on a parsed node and its children,
// appending any bound action names to actions.
func transform(n *html.Node, actions *[]string) error {
	if n.Type == html.TextNode {
		n.Data = rewriteInterpolations(n.Data)
	}
	if n.Type == html.ElementNode {
		if err := applyStructuralDirective(n); err != nil {
			return err
		}
		if isChildSelector(n) {
			if hasDeferAttr(n) {
				rewriteDeferredChild(n)
				return nil
			}
			rewriteChildSelector(n)
			return nil
		}
		if err := applyBindings(n, actions); err != nil {
			return err
		}
		if n.DataAtom == atom.Form {
			injectCSRFInput(n)
		}
	}
	for i := range n.Attr {
		n.Attr[i].Val = rewriteInterpolations(n.Attr[i].Val)
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if err := transform(c, actions); err != nil {
			return err
		}
	}
	return nil
}

// applyBindings rewrites non-structural binding sugar on one element —
// (click), [hydroId] — via each kind's rewrite, collecting bound action
// names. Binding rewrites mutate attributes in place, so the loop stays
// valid.
func applyBindings(n *html.Node, actions *[]string) error {
	for i, a := range n.Attr {
		k := kindByLowered(a.Key)
		if k == nil || k.structural {
			continue
		}
		if err := k.rewrite(n, i, actions); err != nil {
			return err
		}
	}
	return nil
}

// isChildSelector reports whether an element is a nested component
// occurrence: a custom-element tag (hyphenated, unknown to HTML itself)
// that the renderer resolves from the component registry (D14).
func isChildSelector(n *html.Node) bool {
	return n.DataAtom == 0 && strings.Contains(n.Data, "-")
}

// rewriteChildSelector replaces a child-selector element with the liquidChild
// template call the runtime renders it through: the selector plus one
// (field, value) pair per [input] binding, in attribute order. The element's
// other attributes and its content have no meaning in v0.1 and are dropped.
func rewriteChildSelector(n *html.Node) {
	var call strings.Builder
	fmt.Fprintf(&call, "{{liquidChild %q", n.Data)
	for _, a := range n.Attr {
		name, ok := inputBindingName(a.Key)
		if !ok {
			continue
		}
		fmt.Fprintf(&call, " %q %s", name, fieldRef(a.Val))
	}
	call.WriteString("}}")

	n.Type = html.RawNode
	n.Data = call.String()
	n.Attr = nil
	for n.FirstChild != nil {
		n.RemoveChild(n.FirstChild)
	}
}

// deferAttrKey is the HTML-parser spelling of *liquidDefer.
const deferAttrKey = "*liquiddefer"

// hasDeferAttr reports whether a child-selector element is marked
// *liquidDefer.
func hasDeferAttr(n *html.Node) bool {
	for _, a := range n.Attr {
		if a.Key == deferAttrKey {
			return true
		}
	}
	return false
}

// rewriteDeferredChild replaces a deferred child occurrence with its slot:
// a div whose data-hydro-id the liquidDefer call mints at render, holding
// the element's body as the fallback until the deferred render is patched
// in (#26). The element itself becomes the slot's opening RawNode (the call
// carries quotes an attribute value would entity-escape) and the fallback
// children move up to siblings before the closing RawNode — still ahead of
// the transform walk, so fallback content is rewritten like any template
// content.
func rewriteDeferredChild(n *html.Node) {
	var call strings.Builder
	fmt.Fprintf(&call, "{{liquidDefer %q", n.Data)
	for _, a := range n.Attr {
		name, ok := inputBindingName(a.Key)
		if !ok {
			continue
		}
		fmt.Fprintf(&call, " %q %s", name, fieldRef(a.Val))
	}
	call.WriteString("}}")

	var fallback []*html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		fallback = append(fallback, c)
	}
	for _, c := range fallback {
		n.RemoveChild(c)
	}
	n.Type = html.RawNode
	n.Data = `<div data-hydro-id="` + call.String() + `">`
	n.DataAtom = 0
	n.Attr = nil
	ref := n.NextSibling
	insert := func(node *html.Node) {
		if ref != nil {
			n.Parent.InsertBefore(node, ref)
		} else {
			n.Parent.AppendChild(node)
		}
	}
	for _, c := range fallback {
		insert(c)
	}
	insert(&html.Node{Type: html.RawNode, Data: "</div>"})
}

// inputBindingName extracts the child field name from an [input] binding
// attribute key ("[userid]" → "userid"). Bracketed framework bindings such as
// [hydroId] are not inputs.
func inputBindingName(key string) (string, bool) {
	if len(key) < 3 || key[0] != '[' || key[len(key)-1] != ']' {
		return "", false
	}
	if kindByLowered(key) != nil {
		return "", false
	}
	return key[1 : len(key)-1], true
}

// injectCSRFInput appends the hidden CSRF token input every <form> carries
// (D15). The value interpolates the CSRFToken field the framework populates
// per render; vet guarantees the paired struct declares it.
func injectCSRFInput(form *html.Node) {
	form.AppendChild(&html.Node{
		Type:     html.ElementNode,
		Data:     "input",
		DataAtom: atom.Input,
		Attr: []html.Attribute{
			{Key: "type", Val: "hidden"},
			{Key: "name", Val: "csrf_token"},
			{Key: "value", Val: "{{ .CSRFToken }}"},
		},
	})
}

// quoteAll renders names as a comma-separated list of quoted Go string
// literals, deduplicated and sorted so regeneration is deterministic.
func quoteAll(names []string) string {
	quoted := make([]string, 0, len(names))
	for _, n := range slices.Sorted(slices.Values(names)) {
		q := fmt.Sprintf("%q", n)
		if len(quoted) > 0 && quoted[len(quoted)-1] == q {
			continue
		}
		quoted = append(quoted, q)
	}
	return strings.Join(quoted, ", ")
}

// applyStructuralDirective wraps an element carrying a structural directive
// attribute in the html/template control block it compiles to (D1). The
// parser has already lowercased attribute keys, so kinds are matched by
// their lowered spelling. Inserted control nodes are RawNodes: they render
// verbatim and are never re-visited as interpolation text.
func applyStructuralDirective(n *html.Node) error {
	for i, a := range n.Attr {
		k := kindByLowered(a.Key)
		if k == nil || !k.structural {
			continue
		}
		// Rewrite removes the attribute, invalidating the loop's view of
		// n.Attr — return immediately; diagnostics guarantee at most one
		// structural directive per element (LSX006).
		return k.rewrite(n, i, nil)
	}
	return nil
}

// parseGoFor splits a *goFor expression against the grammar
// "let <ident> of <FieldPath>", returning the loop variable name and the
// list field path.
func parseGoFor(expr string) (loopVar, list string, ok bool) {
	parts := strings.Fields(expr)
	if len(parts) != 4 || parts[0] != "let" || parts[2] != "of" {
		return "", "", false
	}
	return parts[1], parts[3], true
}

// wrap inserts raw template text immediately before and after n.
func wrap(n *html.Node, open, end string) {
	n.Parent.InsertBefore(&html.Node{Type: html.RawNode, Data: open}, n)
	if next := n.NextSibling; next != nil {
		n.Parent.InsertBefore(&html.Node{Type: html.RawNode, Data: end}, next)
	} else {
		n.Parent.AppendChild(&html.Node{Type: html.RawNode, Data: end})
	}
}

// fieldRef roots a template expression at the component struct: a bare field
// path gains a leading dot; expressions already rooted (.Field) or naming a
// loop variable ($var) pass through unchanged.
func fieldRef(expr string) string {
	expr = strings.TrimSpace(expr)
	if expr == "" || strings.HasPrefix(expr, ".") || strings.HasPrefix(expr, "$") {
		return expr
	}
	return "." + expr
}

// rewriteInterpolations turns each {{ Field }} occurrence into the
// html/template form {{ .Field }}. Expressions already rooted at a dot and
// loop variables ($var) are left untouched.
func rewriteInterpolations(s string) string {
	var b strings.Builder
	for {
		start := strings.Index(s, "{{")
		if start < 0 {
			b.WriteString(s)
			return b.String()
		}
		end := strings.Index(s[start:], "}}")
		if end < 0 {
			b.WriteString(s)
			return b.String()
		}
		end += start
		inner := strings.TrimSpace(s[start+2 : end])
		b.WriteString(s[:start])
		b.WriteString("{{ " + fieldRef(inner) + " }}")
		s = s[end+2:]
	}
}

// packageName reads the package clause of the paired .go source file.
func packageName(goPath string) (string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, goPath, nil, parser.PackageClauseOnly)
	if err != nil {
		return "", fmt.Errorf("parsing package clause: %w", err)
	}
	return f.Name.Name, nil
}

// pascalCase converts a snake_case file base name to the PascalCase struct
// name it pairs with (user_card → UserCard).
func pascalCase(s string) string {
	parts := strings.Split(s, "_")
	var b strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		b.WriteString(strings.ToUpper(p[:1]) + p[1:])
	}
	return b.String()
}
