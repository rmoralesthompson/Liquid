package compiler_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/rmoralesthompson/liquid/cmd/liquid/internal/compiler"
)

// readFixtureLSX reads one fixture's .lsx source without copying the fixture:
// analysis is read-only.
func readFixtureLSX(t *testing.T, fixture, file string) (string, []byte) {
	t.Helper()
	path := filepath.Join("testdata", fixture, file)
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return path, src
}

// loadFacts loads a fixture package's facts, failing the test on a
// mechanical error.
func loadFacts(t *testing.T, fixture string) *compiler.Facts {
	t.Helper()
	facts, err := compiler.LoadFacts(context.Background(), filepath.Join("testdata", fixture))
	if err != nil {
		t.Fatalf("LoadFacts: %v", err)
	}
	return facts
}

func TestAnalyzeSourceExtractsRefsWithPositions(t *testing.T) {
	path, src := readFixtureLSX(t, "counter", "counter.lsx")

	sa := compiler.AnalyzeSource(path, src)

	if len(sa.Diagnostics) != 0 {
		t.Fatalf("Diagnostics = %+v, want none", sa.Diagnostics)
	}
	wantInterps := []compiler.ExprRef{{Expr: "Count", Line: 2, Col: 23}}
	if !reflect.DeepEqual(sa.Interpolations, wantInterps) {
		t.Errorf("Interpolations = %+v, want %+v", sa.Interpolations, wantInterps)
	}
	wantDirs := []compiler.DirectiveUse{
		{Name: "[hydroId]", Line: 1, Col: 16, NameLine: 1, NameCol: 6, Tag: 1},
		{Name: "(click)", Expr: "Increment", Line: 3, Col: 20, NameLine: 3, NameCol: 11, Tag: 4},
	}
	if !reflect.DeepEqual(sa.Directives, wantDirs) {
		t.Errorf("Directives = %+v, want %+v", sa.Directives, wantDirs)
	}
}

func TestAnalyzeSourceStopsAtUnclosedInterpolation(t *testing.T) {
	path, src := readFixtureLSX(t, "unclosed", "unclosed.lsx")

	sa := compiler.AnalyzeSource(path, src)

	if len(sa.Diagnostics) != 1 || sa.Diagnostics[0].Code != "LSX001" {
		t.Fatalf("Diagnostics = %+v, want exactly one LSX001", sa.Diagnostics)
	}
	if sa.Directives != nil {
		t.Errorf("Directives = %+v, want none after a malformed template", sa.Directives)
	}
}

func TestComponentListsMembersWithDocsAndPositions(t *testing.T) {
	facts := loadFacts(t, "counter")

	members := facts.Component("Counter")

	byName := make(map[string]compiler.Member, len(members))
	for _, m := range members {
		m.File = filepath.Base(m.File)
		byName[m.Name] = m
	}
	want := map[string]compiler.Member{
		"HydroID":   {Name: "HydroID", Type: "string", File: "counter.go", Line: 5, Col: 2},
		"Count":     {Name: "Count", Type: "int", File: "counter.go", Line: 6, Col: 2},
		"Selector":  {Name: "Selector", Method: true, Type: "func() string", Doc: "Selector returns the custom element tag for the component.", File: "counter.go", Line: 10, Col: 19},
		"Increment": {Name: "Increment", Method: true, Type: "func()", Doc: "Increment handles the +1 button.", Handler: true, File: "counter.go", Line: 13, Col: 19},
	}
	if !reflect.DeepEqual(byName, want) {
		t.Errorf("members = %+v, want %+v", byName, want)
	}
}

func TestComponentMarksEventShapedHandlers(t *testing.T) {
	facts := loadFacts(t, "renamer")

	members := facts.Component("Renamer")

	var rename *compiler.Member
	for i, m := range members {
		if m.Name == "Rename" {
			rename = &members[i]
		}
	}
	if rename == nil {
		t.Fatalf("members = %+v, want a Rename entry", members)
	}
	if !rename.Handler {
		t.Errorf("Rename.Handler = false, want true for func(e liquid.Event)")
	}
	if rename.Type != "func(e liquid.Event)" {
		t.Errorf("Rename.Type = %q, want %q", rename.Type, "func(e liquid.Event)")
	}
}

func TestFactsVetMatchesVetPipeline(t *testing.T) {
	for _, fixture := range []string{"counter", "typo", "clicktypo", "brokengo"} {
		t.Run(fixture, func(t *testing.T) {
			dir := filepath.Join("testdata", fixture)
			path, src := readFixtureLSX(t, fixture, fixture+".lsx")

			sa := compiler.AnalyzeSource(path, src)
			facts := loadFacts(t, fixture)
			got := facts.Vet(path, compiler.PairedStructName(path), sa)

			want, err := compiler.Vet(context.Background(), dir)
			if err != nil {
				t.Fatalf("Vet: %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("Facts.Vet = %+v, want the liquid vet output %+v", got, want)
			}
		})
	}
}

func TestSelectorDeclsLocateComponentStructs(t *testing.T) {
	facts := loadFacts(t, "nested")

	decls := facts.SelectorDecls()

	for i := range decls {
		decls[i].File = filepath.Base(decls[i].File)
	}
	want := []compiler.SelectorDecl{
		{Selector: "app-avatar", Struct: "Avatar", Decl: compiler.Decl{File: "avatar.go", Line: 4, Col: 6, Doc: "Avatar is a fixture grandchild, nested by UserCard."}},
		{Selector: "app-dashboard", Struct: "Dashboard", Decl: compiler.Decl{File: "dashboard.go", Line: 4, Col: 6, Doc: "Dashboard is a fixture parent component nesting a user card."}},
		{Selector: "app-user-card", Struct: "UserCard", Decl: compiler.Decl{File: "user_card.go", Line: 4, Col: 6, Doc: "UserCard is a fixture child component receiving its name by [input]."}},
	}
	if !reflect.DeepEqual(decls, want) {
		t.Errorf("SelectorDecls = %+v, want %+v", decls, want)
	}
}

func TestPairingDiagnosticReportsMissingGoSource(t *testing.T) {
	dir := t.TempDir()
	lsxPath := filepath.Join(dir, "widget.lsx")
	if err := os.WriteFile(lsxPath, []byte("<div>hi</div>\n"), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	d := compiler.PairingDiagnostic(lsxPath)

	if d == nil || d.Code != "LSX002" {
		t.Fatalf("PairingDiagnostic = %+v, want an LSX002", d)
	}

	if d := compiler.PairingDiagnostic(filepath.Join("testdata", "counter", "counter.lsx")); d != nil {
		t.Errorf("PairingDiagnostic on a paired template = %+v, want nil", d)
	}
}
