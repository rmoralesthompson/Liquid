package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeDevFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestSnapshotTreeSeesSourceEditsButNotGeneratedFiles(t *testing.T) {
	dir := t.TempDir()
	writeDevFile(t, filepath.Join(dir, "main.go"), "package main")
	writeDevFile(t, filepath.Join(dir, "ui", "card.lsx"), "<p>v1</p>")
	writeDevFile(t, filepath.Join(dir, "ui", "card.go"), "package ui")
	writeDevFile(t, filepath.Join(dir, "ui", "card_gen.go"), "package ui // v1")
	writeDevFile(t, filepath.Join(dir, "README.md"), "prose")

	before, err := snapshotTree(dir)
	if err != nil {
		t.Fatalf("snapshotTree: %v", err)
	}

	// Regenerated output and non-source files must not look like edits — the
	// build cycle itself rewrites *_gen.go and must not retrigger the watch.
	writeDevFile(t, filepath.Join(dir, "ui", "card_gen.go"), "package ui // v2 regenerated")
	writeDevFile(t, filepath.Join(dir, "README.md"), "prose v2")
	unchanged, err := snapshotTree(dir)
	if err != nil {
		t.Fatalf("snapshotTree: %v", err)
	}
	if !before.equal(unchanged) {
		t.Error("*_gen.go / non-source rewrites must not register as changes")
	}

	// A real template edit must. The mtime is bumped explicitly so the test
	// doesn't depend on filesystem timestamp granularity.
	lsx := filepath.Join(dir, "ui", "card.lsx")
	writeDevFile(t, lsx, "<p>v2!</p>")
	future := time.Now().Add(2 * time.Second)
	if chErr := os.Chtimes(lsx, future, future); chErr != nil {
		t.Fatalf("chtimes: %v", chErr)
	}
	after, err := snapshotTree(dir)
	if err != nil {
		t.Fatalf("snapshotTree: %v", err)
	}
	if before.equal(after) {
		t.Error("an .lsx edit must register as a change")
	}
}

func TestGoBuildDiagnosticsTranslateToD13(t *testing.T) {
	stderr := `# scratch/devapp
main.go:12:6: undefined: NoSuchThing
main.go:20:2: declared and not used: x
`
	diags := goBuildDiagnostics("devapp", stderr)
	if len(diags) != 2 {
		t.Fatalf("got %d diagnostics, want 2: %v", len(diags), diags)
	}
	d := diags[0]
	if d.File != filepath.Join("devapp", "main.go") || d.Line != 12 || d.Col != 6 {
		t.Errorf("position = %s:%d:%d, want devapp/main.go:12:6", d.File, d.Line, d.Col)
	}
	if string(d.Code) != "GO001" || string(d.Severity) != "error" {
		t.Errorf("code/severity = %s/%s, want GO001/error", d.Code, d.Severity)
	}
	if d.Message != "undefined: NoSuchThing" {
		t.Errorf("message = %q", d.Message)
	}
}

func TestGoBuildDiagnosticsFallBackToOneCatchAll(t *testing.T) {
	stderr := "go: cannot find main module\n"
	diags := goBuildDiagnostics("devapp", stderr)
	if len(diags) != 1 {
		t.Fatalf("got %d diagnostics, want the catch-all: %v", len(diags), diags)
	}
	if diags[0].Message != "go: cannot find main module" {
		t.Errorf("catch-all message = %q", diags[0].Message)
	}
}
