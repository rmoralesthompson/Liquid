package compiler

import (
	"context"
	"fmt"
	"go/types"
	"path/filepath"
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
func vetReferences(ctx context.Context, dir, lsxPath, structName string, interps []interpolation) ([]Diagnostic, error) {
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
	pkg := pkgs[0]
	if len(pkg.Errors) > 0 {
		return brokenPackageDiags(lsxPath, pkg.Errors), nil
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
		}}, nil
	}
	pairedType := tn.Type()

	var diags []Diagnostic
	for _, in := range interps {
		name := strings.TrimPrefix(in.expr, ".")
		if !isSimpleIdent(name) {
			continue
		}
		if member, _, _ := types.LookupFieldOrMethod(pairedType, true, pkg.Types, name); member != nil {
			continue
		}
		var suggestion string
		if near := nearestMember(pairedType, name); near != "" {
			suggestion = fmt.Sprintf("did you mean %s?", near)
		}
		diags = append(diags, Diagnostic{
			File:       lsxPath,
			Line:       in.line,
			Col:        in.col,
			Severity:   SeverityError,
			Code:       CodeUnknownReference,
			Message:    fmt.Sprintf("%s has no field or method named %s", structName, name),
			Suggestion: suggestion,
		})
	}
	return diags, nil
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
