package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// scratchModule creates a temp module root so the scaffolded package is
// loadable by the vet pass (component packages need a go.mod above them).
func scratchModule(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module scratch\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatalf("writing go.mod: %v", err)
	}
	return dir
}

func TestGenerateComponentScaffoldsABuildablePair(t *testing.T) {
	root := scratchModule(t)
	uiDir := filepath.Join(root, "ui")

	var out strings.Builder
	if err := run([]string{"generate", "component", "stat-card", uiDir}, &out); err != nil {
		t.Fatalf("generate component: %v", err)
	}

	goSrc, err := os.ReadFile(filepath.Join(uiDir, "stat_card.go"))
	if err != nil {
		t.Fatalf("scaffold did not create the paired .go file: %v", err)
	}
	for _, want := range []string{"package ui", "type StatCard struct", `return "stat-card"`} {
		if !strings.Contains(string(goSrc), want) {
			t.Errorf("stat_card.go missing %q\n--- got ---\n%s", want, goSrc)
		}
	}
	if _, err := os.Stat(filepath.Join(uiDir, "stat_card.lsx")); err != nil {
		t.Fatalf("scaffold did not create the paired .lsx file: %v", err)
	}

	// The skeleton must survive a from-scratch `liquid build` — the first-build
	// bootstrap requirement recorded on the ticket.
	if err := run([]string{"build", uiDir}, io.Discard); err != nil {
		t.Fatalf("liquid build over the fresh scaffold failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(uiDir, "stat_card_gen.go")); err != nil {
		t.Fatalf("build over the scaffold emitted no generated template: %v", err)
	}
}

func TestGenerateComponentRejectsAnUnhyphenatedName(t *testing.T) {
	root := scratchModule(t)
	err := run([]string{"generate", "component", "counter", filepath.Join(root, "ui")}, io.Discard)
	if err == nil {
		t.Fatal("generate accepted a selector with no hyphen; custom-element tags need one")
	}
	if !strings.Contains(err.Error(), "hyphen") {
		t.Errorf("error should explain the hyphen requirement, got: %v", err)
	}
}

func TestGenerateComponentRefusesToOverwrite(t *testing.T) {
	root := scratchModule(t)
	uiDir := filepath.Join(root, "ui")
	if err := run([]string{"generate", "component", "stat-card", uiDir}, io.Discard); err != nil {
		t.Fatalf("first generate: %v", err)
	}
	if err := run([]string{"generate", "component", "stat-card", uiDir}, io.Discard); err == nil {
		t.Fatal("second generate over the same name must refuse, not overwrite")
	}
}

func TestGenerateComponentAdoptsTheExistingPackageName(t *testing.T) {
	root := scratchModule(t)
	uiDir := filepath.Join(root, "widgets")
	if err := os.MkdirAll(uiDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	existing := "// Package cards holds the app's components.\npackage cards\n"
	if err := os.WriteFile(filepath.Join(uiDir, "doc.go"), []byte(existing), 0o644); err != nil {
		t.Fatalf("seeding package: %v", err)
	}

	if err := run([]string{"generate", "component", "app-badge", uiDir}, io.Discard); err != nil {
		t.Fatalf("generate component: %v", err)
	}
	goSrc, err := os.ReadFile(filepath.Join(uiDir, "app_badge.go"))
	if err != nil {
		t.Fatalf("reading scaffold: %v", err)
	}
	if !strings.Contains(string(goSrc), "package cards") {
		t.Errorf("scaffold should adopt the directory's existing package clause, got:\n%s", goSrc)
	}
}
