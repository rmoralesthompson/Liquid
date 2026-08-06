package ergobench

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rmoralesthompson/liquid/cmd/liquid/internal/compiler"
)

// Run drives one task once through the generate → build → feed-diagnostics-back
// → repair loop and returns the resulting Sample. dir must be an existing,
// writable directory the caller owns; Run writes each attempt into its own
// attempt-N subdirectory so a stale generated file from one attempt can never
// leak into the next. gen is used across the task's attempts, so callers
// running multiple samples must supply a fresh generator per sample (see RunN).
func Run(ctx context.Context, gen Generator, task Task, dir string) (Sample, error) {
	var (
		sample     Sample
		prior      []File
		priorDiags []compiler.Diagnostic
	)
	for i := 0; i <= task.CapN; i++ {
		files, err := gen.Generate(ctx, task.Prompt, prior, priorDiags)
		if err != nil {
			return sample, fmt.Errorf("generate (attempt %d): %w", i, err)
		}

		attemptDir := filepath.Join(dir, fmt.Sprintf("attempt-%d", i))
		if err = writeModule(attemptDir, task, files); err != nil {
			return sample, err
		}

		diags, err := compile(ctx, attemptDir)
		if err != nil {
			return sample, fmt.Errorf("compile (attempt %d): %w", i, err)
		}

		clean := !hasError(diags)
		if i == 0 {
			sample.FirstPassClean = clean
		}
		if clean {
			sample.ReachedGreen = true
			sample.Repairs = i
			sample.SpecMatch, _ = specMatch(ctx, attemptDir, task.Expect)
			return sample, nil
		}
		prior, priorDiags = files, diags
	}

	// Exhausted CapN repairs without a clean build.
	sample.Repairs = task.CapN
	return sample, nil
}

// RunN runs a task n times, building a fresh generator per sample from newGen
// so scripted or seeded generators do not bleed state across samples, and
// isolating each sample in its own subdirectory under dir. It returns one
// Sample per run.
func RunN(ctx context.Context, newGen func() Generator, task Task, n int, dir string) ([]Sample, error) {
	samples := make([]Sample, 0, n)
	for i := 0; i < n; i++ {
		sampleDir := filepath.Join(dir, fmt.Sprintf("sample-%d", i))
		s, err := Run(ctx, newGen(), task, sampleDir)
		if err != nil {
			return nil, fmt.Errorf("sample %d: %w", i, err)
		}
		samples = append(samples, s)
	}
	return samples, nil
}

// writeModule scaffolds a component module in dir: a go.mod plus the
// generator's emitted files. The module path is derived from the task name so
// each attempt type-checks as its own package. When the task imports liquid
// core (NeedsCore), the go.mod replaces core with a local stub written
// alongside it, so the import resolves without a published core package.
func writeModule(dir string, task Task, files []File) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating module dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(moduleGoMod(task)), 0o644); err != nil {
		return fmt.Errorf("writing go.mod: %w", err)
	}
	if task.NeedsCore {
		if err := writeLiquidStub(dir); err != nil {
			return err
		}
	}
	for _, f := range files {
		p := filepath.Join(dir, f.Name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return fmt.Errorf("creating dir for %s: %w", f.Name, err)
		}
		if err := os.WriteFile(p, []byte(f.Content), 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", f.Name, err)
		}
	}
	return nil
}

// compile runs the same build and vet stages the CLI does and returns their
// combined diagnostics. A clean directory yields none; the returned error is a
// mechanical failure (unreadable directory, etc.), never a diagnostic — those
// are carried in the slice.
func compile(ctx context.Context, dir string) ([]compiler.Diagnostic, error) {
	build, err := compiler.Build(ctx, dir)
	if err != nil {
		return nil, fmt.Errorf("build: %w", err)
	}
	vet, err := compiler.Vet(ctx, dir)
	if err != nil {
		return nil, fmt.Errorf("vet: %w", err)
	}
	return append(build, vet...), nil
}

// hasError reports whether any diagnostic is error-severity — the ones that
// keep a build from being clean. Warnings do not.
func hasError(diags []compiler.Diagnostic) bool {
	for _, d := range diags {
		if d.Severity == compiler.SeverityError {
			return true
		}
	}
	return false
}

// specMatch scores a green component against want using the manifest oracle. It
// returns whether every asserted structural fact holds and, on a mismatch, a
// short reason for reporting. Only fields set in want are asserted.
func specMatch(ctx context.Context, dir string, want Expectation) (bool, string) {
	graph, _, err := compiler.Manifest(ctx, dir)
	if err != nil {
		return false, fmt.Sprintf("manifest: %v", err)
	}
	if graph == nil {
		return false, "manifest produced no graph (component does not compile)"
	}

	comp := findComponent(graph.Components, want.Selector)
	if comp == nil {
		return false, fmt.Sprintf("no component with selector %q", want.Selector)
	}
	if want.Interactive != nil && comp.Interactive != *want.Interactive {
		return false, fmt.Sprintf("interactive = %v, want %v", comp.Interactive, *want.Interactive)
	}
	if ok, reason := matchFields(comp.Fields, want.Fields); !ok {
		return false, reason
	}
	if ok, reason := matchActions(comp.Actions, want.Actions); !ok {
		return false, reason
	}
	return true, ""
}

// matchFields checks that every expected field is present with the asserted
// type and input-ness, returning the first mismatch reason.
func matchFields(got []compiler.ManifestField, want []FieldExpect) (bool, string) {
	for _, fw := range want {
		f := findField(got, fw.Name)
		if f == nil {
			return false, fmt.Sprintf("missing field %q", fw.Name)
		}
		if fw.Type != "" && f.Type != fw.Type {
			return false, fmt.Sprintf("field %q type = %q, want %q", fw.Name, f.Type, fw.Type)
		}
		if fw.Input != nil && f.Input != *fw.Input {
			return false, fmt.Sprintf("field %q input = %v, want %v", fw.Name, f.Input, *fw.Input)
		}
	}
	return true, ""
}

// matchActions checks that every expected action is present with the asserted
// event shape, returning the first mismatch reason.
func matchActions(got []compiler.ManifestAction, want []ActionExpect) (bool, string) {
	for _, aw := range want {
		a := findAction(got, aw.Name)
		if a == nil {
			return false, fmt.Sprintf("missing action %q", aw.Name)
		}
		if aw.TakesEvent != nil && a.TakesEvent != *aw.TakesEvent {
			return false, fmt.Sprintf("action %q takesEvent = %v, want %v", aw.Name, a.TakesEvent, *aw.TakesEvent)
		}
		for _, ev := range aw.Events {
			if !contains(a.Events, ev) {
				return false, fmt.Sprintf("action %q missing event %q", aw.Name, ev)
			}
		}
	}
	return true, ""
}

// findComponent returns the component registering selector, or nil.
func findComponent(comps []compiler.ManifestComponent, selector string) *compiler.ManifestComponent {
	for i := range comps {
		if comps[i].Selector == selector {
			return &comps[i]
		}
	}
	return nil
}

func findField(fields []compiler.ManifestField, name string) *compiler.ManifestField {
	for i := range fields {
		if fields[i].Name == name {
			return &fields[i]
		}
	}
	return nil
}

func findAction(actions []compiler.ManifestAction, name string) *compiler.ManifestAction {
	for i := range actions {
		if actions[i].Name == name {
			return &actions[i]
		}
	}
	return nil
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
