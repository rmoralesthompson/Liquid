package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

// exampleModuleFiles are the repo paths a temp copy of the example needs to
// form a loadable module slice: the module files, the core package the
// example imports, and the example itself.
var exampleModuleFiles = []string{"go.mod", "go.sum", "core", "examples/dashboard"}

// copyExampleModule copies the module slice rooted at repoRoot into dst.
func copyExampleModule(t *testing.T, repoRoot, dst string) {
	t.Helper()
	for _, rel := range exampleModuleFiles {
		src := filepath.Join(repoRoot, filepath.FromSlash(rel))
		info, statErr := os.Stat(src)
		if statErr != nil {
			t.Fatalf("stat %s: %v", src, statErr)
		}
		target := filepath.Join(dst, filepath.FromSlash(rel))
		if info.IsDir() {
			if err := os.CopyFS(target, os.DirFS(src)); err != nil {
				t.Fatalf("copying %s: %v", rel, err)
			}
			continue
		}
		data, readErr := os.ReadFile(src)
		if readErr != nil {
			t.Fatalf("reading %s: %v", rel, readErr)
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			t.Fatalf("writing %s: %v", rel, err)
		}
	}
}

// TestExampleDashboardBuildsCleanAndGenIsFresh is the compiler-seam half of
// the #13 integration proof: `liquid build` over the example app must
// produce zero diagnostics, and the _gen.go files committed alongside the
// sources must be exactly what the compiler generates today — a fresh clone
// runs the same code the compiler would emit.
func TestExampleDashboardBuildsCleanAndGenIsFresh(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolving repo root: %v", err)
	}
	exampleDir := filepath.Join(repoRoot, "examples", "dashboard", "ui")
	committed, err := filepath.Glob(filepath.Join(exampleDir, "*_gen.go"))
	if err != nil {
		t.Fatalf("globbing committed gen files: %v", err)
	}
	if len(committed) == 0 {
		t.Fatalf("no committed *_gen.go in %s; run: go run ./cmd/liquid build examples/dashboard/ui", exampleDir)
	}

	tmp := t.TempDir()
	copyExampleModule(t, repoRoot, tmp)
	tmpExample := filepath.Join(tmp, "examples", "dashboard", "ui")
	stale, err := filepath.Glob(filepath.Join(tmpExample, "*_gen.go"))
	if err != nil {
		t.Fatalf("globbing temp gen files: %v", err)
	}
	for _, f := range stale {
		if err := os.Remove(f); err != nil {
			t.Fatalf("removing %s: %v", f, err)
		}
	}

	if err := run([]string{"build", tmpExample}, io.Discard); err != nil {
		t.Fatalf("liquid build over the example reported diagnostics or failed: %v", err)
	}

	for _, want := range committed {
		name := filepath.Base(want)
		wantSrc, err := os.ReadFile(want)
		if err != nil {
			t.Fatalf("reading committed %s: %v", name, err)
		}
		gotSrc, err := os.ReadFile(filepath.Join(tmpExample, name))
		if err != nil {
			t.Fatalf("build did not regenerate %s: %v", name, err)
		}
		if string(gotSrc) != string(wantSrc) {
			t.Errorf("committed %s is stale; run: go run ./cmd/liquid build examples/dashboard/ui", name)
		}
	}
}
