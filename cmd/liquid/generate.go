package main

import (
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// generateUsage documents the generate verb's argument shape.
const generateUsage = "usage: liquid generate component <name> [dir]"

// runGenerate handles `liquid generate component <name> [dir]`: it scaffolds a
// paired struct + template following the filename convention (D16), in a
// directory meant to hold only components — the layout that keeps a
// from-scratch `liquid build` clean (the first-build bootstrap note on the
// ticket). dir defaults to "ui" for exactly that reason.
func runGenerate(args []string, stdout io.Writer) error {
	if len(args) == 0 || args[0] != "component" {
		return errors.New(generateUsage)
	}
	rest := args[1:]
	if len(rest) == 0 || len(rest) > 2 {
		return errors.New(generateUsage)
	}
	name := rest[0]
	dir := "ui"
	if len(rest) == 2 {
		dir = rest[1]
	}
	if err := validateSelector(name); err != nil {
		return err
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	pkg, err := packageNameFor(dir)
	if err != nil {
		return err
	}

	base := strings.ReplaceAll(name, "-", "_")
	goPath := filepath.Join(dir, base+".go")
	lsxPath := filepath.Join(dir, base+".lsx")
	for _, p := range []string{goPath, lsxPath} {
		if _, err := os.Stat(p); err == nil {
			return fmt.Errorf("%s already exists; refusing to overwrite", p)
		}
	}

	structName := pascalCase(name)
	goSrc := fmt.Sprintf(`package %[1]s

// %[2]s is the %[3]s component. Exported fields are template state; add an
// OnInit(ctx liquid.Ctx) error method to load them per request.
type %[2]s struct {
	// Title renders as {{ Title }} in %[4]s.lsx.
	Title string
}

// Selector returns the custom-element tag this component renders as.
func (c *%[2]s) Selector() string { return "%[3]s" }
`, pkg, structName, name, base)
	lsxSrc := fmt.Sprintf(`<section>
  <h2>{{ Title }}</h2>
  <p>%[1]s is ready — edit %[2]s.lsx and %[2]s.go, then run: liquid build %[3]s</p>
</section>
`, name, base, dir)

	if err := os.WriteFile(goPath, []byte(goSrc), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", goPath, err)
	}
	if err := os.WriteFile(lsxPath, []byte(lsxSrc), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", lsxPath, err)
	}
	if _, err := fmt.Fprintf(stdout, "created %s\ncreated %s\nnext: liquid build %s\n", goPath, lsxPath, dir); err != nil {
		return fmt.Errorf("reporting created files: %w", err)
	}
	return nil
}

// validateSelector enforces the custom-element naming convention: lowercase
// kebab-case with at least one hyphen. Hyphenless names are rejected because
// a hyphenless element can never be nested as a child occurrence (D14) —
// better to refuse at scaffold time than to surprise later.
func validateSelector(name string) error {
	valid := name != "" && !strings.HasPrefix(name, "-") && !strings.HasSuffix(name, "-") &&
		!strings.Contains(name, "--") && unicode.IsLower(rune(name[0]))
	for _, r := range name {
		if r != '-' && !unicode.IsLower(r) && !unicode.IsDigit(r) {
			valid = false
			break
		}
	}
	if !valid || !strings.Contains(name, "-") {
		return fmt.Errorf("component name %q must be a lowercase name with a hyphen, like \"stat-card\" or \"app-counter\" (custom-element tags need one to nest as children)", name)
	}
	return nil
}

// packageNameFor picks the scaffold's package clause: the package already
// declared by the directory's Go files when there is one, else the directory
// name reduced to a valid identifier.
func packageNameFor(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", dir, err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, parser.PackageClauseOnly)
		if err != nil {
			continue // an unparsable neighbor doesn't block scaffolding
		}
		return f.Name.Name, nil
	}
	pkg := strings.Map(func(r rune) rune {
		if unicode.IsLower(r) || unicode.IsDigit(r) {
			return r
		}
		return -1
	}, strings.ToLower(filepath.Base(dir)))
	if pkg == "" || unicode.IsDigit(rune(pkg[0])) {
		pkg = "ui"
	}
	return pkg, nil
}

// pascalCase turns a kebab-case selector into an exported Go identifier:
// "stat-card" → "StatCard".
func pascalCase(kebab string) string {
	var b strings.Builder
	for _, part := range strings.Split(kebab, "-") {
		if part == "" {
			continue
		}
		b.WriteString(strings.ToUpper(part[:1]) + part[1:])
	}
	return b.String()
}
