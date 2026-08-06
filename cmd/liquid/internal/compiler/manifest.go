package compiler

// This file implements `liquid manifest` (D26): a stable, machine-readable
// graph of the compiled component app, emitted for a non-human author to read
// as data instead of re-parsing source. It is a pure projection of facts the
// compiler already holds at build/vet time — the go/types view of each
// component package (Facts) joined with the raw-source directive scan
// (AnalyzeSource). No new analysis pass runs here, so manifest, vet and
// liquid lsp cannot disagree (the one-grammar guarantee of #41).
//
// The field names of the JSON envelope are the API (the D13 stance): richness
// is secondary to a stable, machine-matchable shape. ManifestVersion carries a
// schema code so an agent can match against changes; there is no
// backward-compat promise while v0.x (D24).

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// ManifestVersion is the schema code of the manifest envelope. It changes when
// the shape of the graph changes; agents match against it. No backward-compat
// promise holds while the framework is v0.x (D24).
const ManifestVersion = "v0"

// headProviderSig is the go/types rendering of a HeadProvider's Head method
// (D6: Head() liquid.Head). The manifest reports head only for a method whose
// signature matches, so an unrelated exported method named Head is not
// mistaken for a document-head provider.
const headProviderSig = "func() liquid.Head"

// ManifestGraph is the whole compiled component app as data: a versioned
// envelope over every resolvable component in the scanned directory, sorted by
// selector. The static graph only — not live sessions or runtime instances
// (D2).
type ManifestGraph struct {
	// Version is the envelope schema code (ManifestVersion).
	Version string `json:"version"`
	// Components are the resolvable components, sorted by selector then source
	// file. Empty (not null) when the directory declares none.
	Components []ManifestComponent `json:"components"`
}

// ManifestComponent is one component's compiled surface: its identity, the
// struct fields an author can bind, the allowlisted actions an author can
// wire, and whether it roots an interactive (hydro) region.
type ManifestComponent struct {
	// Selector is the custom-element tag the component registers under.
	Selector string `json:"selector"`
	// Struct is the paired Go struct's name.
	Struct string `json:"struct"`
	// File is the component's .lsx template path.
	File string `json:"file"`
	// Interactive reports a declared [hydroId] root — the component mints a
	// hydro session and dispatches (click)/(submit) actions.
	Interactive bool `json:"interactive"`
	// Head reports that the struct provides a Head() document head (D6).
	Head bool `json:"head"`
	// Fields are the exported struct fields, in declaration order.
	Fields []ManifestField `json:"fields"`
	// Actions are the allowlisted event handlers wired from this template,
	// sorted by name.
	Actions []ManifestAction `json:"actions"`
}

// ManifestField is one exported struct field an author can read or bind.
type ManifestField struct {
	// Name is the field identifier templates and [input] bindings reference.
	Name string `json:"name"`
	// Type renders the field's Go type with package names qualified bare
	// (liquid.Event, not the import path).
	Type string `json:"type"`
	// Input reports that some template binds this field via [input] (D4
	// nesting) — i.e. it is a composition input, not internal state.
	Input bool `json:"input"`
}

// ManifestAction is one allowlisted event handler (D10): a method dispatchable
// from client events, with its D11 signature.
type ManifestAction struct {
	// Name is the handler method's name — the allowlist key.
	Name string `json:"name"`
	// Signature renders the handler's Go signature (func() or
	// func(e liquid.Event)).
	Signature string `json:"signature"`
	// TakesEvent reports the func(liquid.Event) shape (D11); false is the
	// bare func() shape.
	TakesEvent bool `json:"takesEvent"`
	// Events are the binding kinds that wire this handler (click, submit),
	// sorted and de-duplicated.
	Events []string `json:"events"`
	// Guard reports a <Name>Guard boundary predicate (D30): a pure check the
	// dispatch seam runs over this action's payload before the handler.
	Guard bool `json:"guard"`
	// ClosedDomains maps a payload field (as declared) to the enumerated value
	// set the seam admits (D30); an empty, non-null map when the action
	// constrains no field.
	ClosedDomains map[string][]string `json:"closedDomains"`
}

// Manifest builds the component graph for dir. It first runs the same
// diagnostic gate as vet: if the directory does not compile, it returns a nil
// graph and the D13 diagnostics that explain why — no manifest is emitted for
// a broken package. On a clean gate it returns the graph and no error; the
// diagnostics slice may still carry advisory warnings (D29), which do not
// suppress the graph.
func Manifest(ctx context.Context, dir string) (*ManifestGraph, []Diagnostic, error) {
	diags, err := Vet(ctx, dir)
	if err != nil {
		return nil, nil, err
	}
	for _, d := range diags {
		if d.Severity == SeverityError {
			return nil, diags, nil
		}
	}

	dirs, err := lsxDirs(dir)
	if err != nil {
		return nil, nil, err
	}

	graph := &ManifestGraph{Version: ManifestVersion, Components: []ManifestComponent{}}
	for _, d := range dirs {
		comps, err := manifestPackage(ctx, d)
		if err != nil {
			return nil, nil, err
		}
		graph.Components = append(graph.Components, comps...)
	}
	slices.SortFunc(graph.Components, func(a, b ManifestComponent) int {
		if c := strings.Compare(a.Selector, b.Selector); c != 0 {
			return c
		}
		return strings.Compare(a.File, b.File)
	})
	return graph, diags, nil
}

// manifestPackage projects one component package (a single .lsx-bearing
// directory) into its components, joining the package's go/types facts with
// the raw-source directive scan of every template beside them.
func manifestPackage(ctx context.Context, dir string) ([]ManifestComponent, error) {
	facts, err := LoadFacts(ctx, dir)
	if err != nil {
		return nil, err
	}

	templates, inputs, err := scanTemplates(dir)
	if err != nil {
		return nil, err
	}

	var comps []ManifestComponent
	for _, decl := range facts.SelectorDecls() {
		tmpl, ok := templates[decl.Struct]
		if !ok {
			// A struct with a literal selector but no paired template is not a
			// renderable component; the build registry cannot serve it either.
			continue
		}

		comp := ManifestComponent{
			Selector: decl.Selector,
			Struct:   decl.Struct,
			File:     tmpl.path,
			// Empty, non-nil so every component serializes fields/actions as a
			// JSON array — the same [] contract the top-level graph gives, so an
			// agent never has to special-case null.
			Fields:  []ManifestField{},
			Actions: []ManifestAction{},
		}
		bound := inputs[decl.Selector]
		members := facts.Component(decl.Struct)
		sigByName := make(map[string]Member, len(members))
		for _, m := range members {
			sigByName[m.Name] = m
			switch {
			case m.Method && m.Name == "Head" && m.Type == headProviderSig:
				comp.Head = true
			case !m.Method:
				comp.Fields = append(comp.Fields, ManifestField{
					Name:  m.Name,
					Type:  m.Type,
					Input: bound[strings.ToLower(m.Name)],
				})
			}
		}

		interactive, actions := projectDirectives(tmpl.sa, sigByName)
		comp.Interactive = interactive
		attachContracts(facts, decl.Struct, actions)
		if len(actions) > 0 {
			comp.Actions = actions
		}
		comps = append(comps, comp)
	}
	return comps, nil
}

// projectDirectives projects one template's directive scan into the
// component's interactive flag ([hydroId] presence) and its allowlisted
// actions, sorted by name. Signatures come from the paired struct's methods; a
// (click)/(submit) whose handler is not a struct method is dropped here — vet
// has already reported it, so the gate never lets that reach a manifest.
func projectDirectives(sa *SourceAnalysis, sigByName map[string]Member) (interactive bool, actions []ManifestAction) {
	byName := make(map[string]*ManifestAction)
	var names []string
	for _, use := range sa.Directives {
		var event string
		switch use.Name {
		case kindHydroID:
			interactive = true
			continue
		case kindClick:
			event = "click"
		case kindSubmit:
			event = "submit"
		default:
			continue
		}
		method := use.Expr
		act, ok := byName[method]
		if !ok {
			m, known := sigByName[method]
			if !known {
				continue
			}
			act = &ManifestAction{Name: method, Signature: m.Type, TakesEvent: m.Handler && m.Type != "func()"}
			byName[method] = act
			names = append(names, method)
		}
		if !slices.Contains(act.Events, event) {
			act.Events = append(act.Events, event)
		}
	}
	slices.Sort(names)
	for _, name := range names {
		act := byName[name]
		slices.Sort(act.Events)
		actions = append(actions, *act)
	}
	return interactive, actions
}

// attachContracts fills each action's D30 payload contract — guard presence
// and closed domains — from the package's go/types facts, joining the manifest
// projection to the same contract discovery the seam and vet use. Every action
// gets a non-null ClosedDomains map so an agent never special-cases null; an
// action with no closed domain simply carries an empty one.
func attachContracts(facts *Facts, structName string, actions []ManifestAction) {
	names := make([]string, len(actions))
	for i := range actions {
		names[i] = actions[i].Name
	}
	contracts := facts.ActionContracts(structName, names)
	for i := range actions {
		c := contracts[actions[i].Name]
		actions[i].Guard = c.Guard
		actions[i].ClosedDomains = c.Domains
		if actions[i].ClosedDomains == nil {
			actions[i].ClosedDomains = map[string][]string{}
		}
	}
}

// templateFacts is one paired template's path and its raw-source scan, kept
// together so manifestPackage reuses the scan it already ran rather than
// re-reading and re-analyzing the file.
type templateFacts struct {
	path string
	sa   *SourceAnalysis
}

// scanTemplates walks one package directory once, pairing each .lsx template
// to its struct name (with its scan) and collecting every [input] binding by
// target selector. The input map is keyed by child selector, then by
// lower-cased field name, so a component's fields can be marked
// input-or-not by a case-insensitive lookup (matching the assignability check
// nesting itself uses).
//
// [input] bindings are resolved within this one package directory — the same
// same-package-only scope v0.1 nesting has (a cross-package child is
// false-LSX012, recorded on #11), so the manifest's input-ness is accurate
// exactly where nesting itself resolves.
func scanTemplates(dir string) (templates map[string]templateFacts, inputs map[string]map[string]bool, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("reading %s: %w", dir, err)
	}
	templates = make(map[string]templateFacts)
	inputs = make(map[string]map[string]bool)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".lsx") {
			continue
		}
		lsxPath := filepath.Join(dir, entry.Name())
		sa := analyzeTemplate(lsxPath)
		templates[PairedStructName(lsxPath)] = templateFacts{path: lsxPath, sa: sa}
		for _, use := range sa.Directives {
			if use.Name != kindInput {
				continue
			}
			set := inputs[use.Sel]
			if set == nil {
				set = make(map[string]bool)
				inputs[use.Sel] = set
			}
			set[strings.ToLower(use.Attr)] = true
		}
	}
	return templates, inputs, nil
}

// analyzeTemplate runs the raw-source scan over one template path. A read
// failure yields an empty analysis rather than an error: the vet gate in
// Manifest has already read and checked every template, so a file that
// vanished between gate and projection simply contributes nothing.
func analyzeTemplate(lsxPath string) *SourceAnalysis {
	src, err := os.ReadFile(lsxPath)
	if err != nil {
		return &SourceAnalysis{}
	}
	return AnalyzeSource(lsxPath, src)
}
