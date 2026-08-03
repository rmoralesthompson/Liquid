package compiler

// This file is the exported analysis surface the language server
// (liquid lsp) builds on. It re-exposes the compiler's own raw-source
// scanners and go/types checks, so editor features and liquid vet share one
// grammar and cannot drift apart.

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/tools/go/packages"
)

// ExprRef is one {{ ... }} interpolation in raw .lsx source, positioned at
// the first byte of its trimmed expression (1-based line, 1-based byte
// column).
type ExprRef struct {
	Expr string
	Line int
	Col  int
}

// The two DirectiveUse kinds the tag scanner records dynamically rather
// than from the directive registry: a child-selector element and its
// [input] bindings.
const (
	KindChildTag = kindChildTag
	KindInput    = kindInput
)

// DirectiveUse is one directive or binding occurrence in raw .lsx source:
// an attribute (*goIf, (click), [hydroId], [input]) or a tag-level kind
// (<form>, a child-selector element).
type DirectiveUse struct {
	// Name is the canonical spelling — *goIf, *goFor, (click), (submit),
	// [hydroId], <form> — or the sentinel KindChildTag / KindInput.
	Name string
	// Expr is the attribute's trimmed expression, "" when valueless.
	Expr string
	// Line and Col position the expression (1-based line, byte column).
	Line int
	Col  int
	// NameLine and NameCol position the attribute name or tag itself.
	NameLine int
	NameCol  int
	// Sel is the child selector involved: the element's own tag for a child
	// occurrence, the enclosing element's tag for an [input] binding.
	Sel string
	// Attr is an [input] binding's child field name, as the author typed it.
	Attr string
	// Tag is the ordinal of the enclosing tag, shared by directives on the
	// same element.
	Tag int
}

// SourceAnalysis is everything a source-only scan of one .lsx buffer
// yields: the positioned references, and the source-level diagnostics that
// gate the vet cross-check.
type SourceAnalysis struct {
	Interpolations []ExprRef
	Directives     []DirectiveUse
	// Diagnostics are the source-level findings (LSX001, LSX005, LSX006,
	// LSX010, LSX013, LSX014); when non-empty the compile pipeline stops
	// before the vet cross-check, and Facts.Vet mirrors that.
	Diagnostics []Diagnostic
	refs        []structRef
}

// AnalyzeSource runs the compiler's raw-source scans over one .lsx buffer.
// The buffer need not exist on disk — the language server analyzes unsaved
// editor contents; lsxPath supplies diagnostic positions and the pairing
// convention only.
func AnalyzeSource(lsxPath string, src []byte) *SourceAnalysis {
	sa := &SourceAnalysis{}
	interps, diags := scanInterpolations(lsxPath, src)
	for _, in := range interps {
		sa.Interpolations = append(sa.Interpolations, ExprRef{Expr: in.expr, Line: in.line, Col: in.col})
	}
	if len(diags) > 0 {
		// Everything after an unclosed {{ is ambiguous; stop the way Build
		// does rather than scan garbage for directives.
		sa.Diagnostics = diags
		return sa
	}
	dirs := scanDirectives(src)
	for _, d := range dirs {
		sa.Directives = append(sa.Directives, DirectiveUse{
			Name: d.name, Expr: d.expr, Line: d.line, Col: d.col,
			NameLine: d.namePos.line, NameCol: d.namePos.col,
			Sel: d.sel, Attr: d.attr, Tag: d.tag,
		})
	}
	sa.Diagnostics = checkDirectives(lsxPath, dirs)
	sa.refs = append(interps, directiveRefs(dirs)...)
	return sa
}

// PairedStructName returns the struct name the .lsx filename convention
// pairs a template with (user_card.lsx → UserCard).
func PairedStructName(lsxPath string) string {
	return pascalCase(strings.TrimSuffix(filepath.Base(lsxPath), ".lsx"))
}

// ParseGoFor splits a *goFor expression against its grammar
// "let <ident> of <FieldPath>", for language-server features that need the
// two halves separately.
func ParseGoFor(expr string) (loopVar, list string, ok bool) {
	return parseGoFor(expr)
}

// PairingDiagnostic reports the LSX002 for a .lsx file whose paired .go
// source does not exist, or nil when the pairing is in place.
func PairingDiagnostic(lsxPath string) *Diagnostic {
	base := strings.TrimSuffix(lsxPath, ".lsx")
	goPath := base + ".go"
	if _, err := os.Stat(goPath); !errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return &Diagnostic{
		File:     lsxPath,
		Line:     1,
		Col:      1,
		Severity: SeverityError,
		Code:     CodeMissingPairedSource,
		Message: fmt.Sprintf("no paired Go source file for %s: %s does not exist",
			filepath.Base(lsxPath), filepath.Base(goPath)),
		Suggestion: fmt.Sprintf("create %s defining a struct named %s",
			filepath.Base(goPath), PairedStructName(lsxPath)),
	}
}

// Facts is the go/types view of one component package, loaded once and
// shared across language-server features. A Facts is immutable after
// LoadFacts apart from its lazily built doc-comment index.
type Facts struct {
	pkg  *packages.Package
	docs map[token.Pos]string
}

// LoadFacts type-checks the Go package in dir and returns its component
// facts. The error covers mechanical load failures; a package that merely
// fails type-checking loads with Broken reporting true.
func LoadFacts(ctx context.Context, dir string) (*Facts, error) {
	pkg, err := loadPackage(ctx, dir)
	if err != nil {
		return nil, err
	}
	return &Facts{pkg: pkg}, nil
}

// Broken reports whether the package failed type-checking; a broken package
// yields LSX007 diagnostics from Vet and no member or selector facts.
func (f *Facts) Broken() bool { return len(f.pkg.Errors) > 0 }

// Vet cross-checks one analyzed template against its paired struct exactly
// as liquid vet would: source-level diagnostics short-circuit the reference
// check, mirroring the Build/Vet pipeline.
func (f *Facts) Vet(lsxPath, structName string, sa *SourceAnalysis) []Diagnostic {
	if len(sa.Diagnostics) > 0 {
		return sa.Diagnostics
	}
	return f.vetRefs(lsxPath, structName, sa.refs)
}

// Member is one exported field or method of a component struct, as the
// language server presents it.
type Member struct {
	// Name is the identifier templates reference.
	Name string
	// Method reports a method; false means a struct field.
	Method bool
	// Type renders the member's type — a field type, or a method signature —
	// with package names qualified bare (liquid.Event, not the import path).
	Type string
	// Doc is the declaration's doc comment, "" when undocumented.
	Doc string
	// File, Line and Col (1-based line and byte column) locate the
	// declaration in Go source.
	File string
	Line int
	Col  int
	// Handler reports a method dispatchable as an event handler (D11).
	Handler bool
}

// Component lists structName's exported fields and methods in declaration
// order, or nil when the package is broken or does not declare the struct.
func (f *Facts) Component(structName string) []Member {
	tn := f.lookupType(structName)
	if tn == nil {
		return nil
	}
	qual := func(p *types.Package) string { return p.Name() }
	var members []Member
	if s, ok := tn.Type().Underlying().(*types.Struct); ok {
		for i := range s.NumFields() {
			fld := s.Field(i)
			if !fld.Exported() {
				continue
			}
			m := Member{Name: fld.Name(), Type: types.TypeString(fld.Type(), qual), Doc: f.docFor(fld.Pos())}
			m.File, m.Line, m.Col = f.position(fld.Pos())
			members = append(members, m)
		}
	}
	if n, ok := tn.Type().(*types.Named); ok {
		for i := range n.NumMethods() {
			fn := n.Method(i)
			if !fn.Exported() {
				continue
			}
			sig := fn.Type().(*types.Signature)
			m := Member{Name: fn.Name(), Method: true, Type: types.TypeString(sig, qual),
				Doc: f.docFor(fn.Pos()), Handler: isHandlerSig(sig)}
			m.File, m.Line, m.Col = f.position(fn.Pos())
			members = append(members, m)
		}
	}
	return members
}

// Decl locates one Go declaration, as a go-to-definition target.
type Decl struct {
	// File, Line and Col (1-based line and byte column) locate the
	// declaration in Go source.
	File string
	Line int
	Col  int
	// Doc is the declaration's doc comment, "" when undocumented.
	Doc string
}

// StructDecl locates structName's type declaration; ok is false when the
// package is broken or does not declare it.
func (f *Facts) StructDecl(structName string) (Decl, bool) {
	tn := f.lookupType(structName)
	if tn == nil {
		return Decl{}, false
	}
	file, line, col := f.position(tn.Pos())
	return Decl{File: file, Line: line, Col: col, Doc: f.docFor(tn.Pos())}, true
}

// SelectorDecl records one component selector a package declares.
type SelectorDecl struct {
	// Selector is the custom-element tag (app-user-card).
	Selector string
	// Struct is the component struct declaring the selector.
	Struct string
	// Decl locates the component struct's type declaration.
	Decl
}

// SelectorDecls lists the selectors the package declares, sorted by
// selector, each located at its component struct's declaration.
func (f *Facts) SelectorDecls() []SelectorDecl {
	if f.Broken() {
		return nil
	}
	table := packageSelectors(f.pkg)
	var decls []SelectorDecl
	for _, sel := range slices.Sorted(maps.Keys(table)) {
		decl, ok := f.StructDecl(table[sel])
		if !ok {
			continue
		}
		decls = append(decls, SelectorDecl{Selector: sel, Struct: table[sel], Decl: decl})
	}
	return decls
}

// lookupType resolves structName in the package scope, nil when the package
// is broken or the name is not a declared type.
func (f *Facts) lookupType(structName string) *types.TypeName {
	if f.Broken() {
		return nil
	}
	tn, _ := f.pkg.Types.Scope().Lookup(structName).(*types.TypeName)
	return tn
}

// docFor returns the doc comment recorded for a declared-name position.
func (f *Facts) docFor(p token.Pos) string {
	if f.docs == nil {
		f.docs = collectDocs(f.pkg.Syntax)
	}
	return strings.TrimSpace(f.docs[p])
}

// position resolves a token.Pos to its file, line, and 1-based byte column.
func (f *Facts) position(p token.Pos) (string, int, int) {
	pos := f.pkg.Fset.Position(p)
	return pos.Filename, pos.Line, pos.Column
}

// collectDocs indexes doc comments by declared-name position: struct fields
// (doc comment, or the trailing line comment), functions and methods, and
// type declarations (spec doc, or the decl doc of a single-spec decl).
func collectDocs(files []*ast.File) map[token.Pos]string {
	docs := make(map[token.Pos]string)
	for _, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			switch d := n.(type) {
			case *ast.FuncDecl:
				if d.Doc != nil {
					docs[d.Name.Pos()] = d.Doc.Text()
				}
			case *ast.Field:
				text := ""
				switch {
				case d.Doc != nil:
					text = d.Doc.Text()
				case d.Comment != nil:
					text = d.Comment.Text()
				}
				for _, name := range d.Names {
					if text != "" {
						docs[name.Pos()] = text
					}
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					switch {
					case ts.Doc != nil:
						docs[ts.Name.Pos()] = ts.Doc.Text()
					case d.Doc != nil && len(d.Specs) == 1:
						docs[ts.Name.Pos()] = d.Doc.Text()
					}
				}
			}
			return true
		})
	}
	return docs
}
