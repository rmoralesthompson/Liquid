package ergobench

import (
	"context"
	"path/filepath"
	"testing"
)

// These tests exercise the nightly gate's deterministic surface — baseline
// round-trip, RunCorpus over a scripted generator, and Gate's regression /
// missing-baseline verdicts — with zero LLM, so the gate's decision logic is
// guarded on every PR even though the numbers it gates come from a live run.

func TestBaselineSetRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "baseline.json")
	want := &BaselineSet{
		Model:   "claude-opus-4-8",
		Samples: 5,
		Tasks: map[string]Baseline{
			"greenfield-counter": {Task: "greenfield-counter", N: 5, FirstPassRate: 0.8, GreenRate: 1.0, SpecMatchRate: 1.0, MeanRepairs: 0.4},
		},
	}
	if err := want.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := LoadBaselineSet(path)
	if err != nil {
		t.Fatalf("LoadBaselineSet: %v", err)
	}
	if got == nil {
		t.Fatal("LoadBaselineSet returned nil for an existing file")
	}
	if got.Model != want.Model || got.Samples != want.Samples {
		t.Errorf("provenance = {%q, %d}, want {%q, %d}", got.Model, got.Samples, want.Model, want.Samples)
	}
	if got.Tasks["greenfield-counter"] != want.Tasks["greenfield-counter"] {
		t.Errorf("task baseline = %+v, want %+v", got.Tasks["greenfield-counter"], want.Tasks["greenfield-counter"])
	}
}

func TestLoadBaselineSetMissingFileIsNilNotError(t *testing.T) {
	got, err := LoadBaselineSet(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err != nil {
		t.Fatalf("LoadBaselineSet on a missing file errored: %v", err)
	}
	if got != nil {
		t.Errorf("missing file returned %+v, want nil (bootstrap case)", got)
	}
}

func TestBaselineFromStatsCopiesGatedFigures(t *testing.T) {
	s := Stats{Task: "t", N: 5, FirstPassRate: 0.6, GreenRate: 0.8, SpecMatchRate: 0.4, MeanRepairs: 1.2, VarRepairs: 0.9}
	b := BaselineFromStats(s)
	want := Baseline{Task: "t", N: 5, FirstPassRate: 0.6, GreenRate: 0.8, SpecMatchRate: 0.4, MeanRepairs: 1.2}
	if b != want {
		t.Errorf("BaselineFromStats = %+v, want %+v (variance is not stored)", b, want)
	}
}

func TestGatePassesWithinBand(t *testing.T) {
	set := &BaselineSet{Tasks: map[string]Baseline{
		"t": {Task: "t", FirstPassRate: 0.8, GreenRate: 1.0, SpecMatchRate: 0.9, MeanRepairs: 1.0},
	}}
	stats := []Stats{{Task: "t", FirstPassRate: 0.7, GreenRate: 1.0, SpecMatchRate: 0.9, MeanRepairs: 1.4}}
	res := Gate(set, stats, 0.2, 0.5)
	if !res.OK() {
		t.Errorf("within-band run failed the gate: %+v", res)
	}
}

func TestGateFlagsRegression(t *testing.T) {
	set := &BaselineSet{Tasks: map[string]Baseline{
		"t": {Task: "t", FirstPassRate: 0.8, GreenRate: 1.0, SpecMatchRate: 0.9, MeanRepairs: 1.0},
	}}
	// First-pass collapses well past the band.
	stats := []Stats{{Task: "t", FirstPassRate: 0.4, GreenRate: 1.0, SpecMatchRate: 0.9, MeanRepairs: 1.0}}
	res := Gate(set, stats, 0.2, 0.5)
	if res.OK() {
		t.Fatal("regressed run passed the gate")
	}
	regs := res.Regressions["t"]
	if len(regs) != 1 || regs[0].Metric != "firstPassRate" {
		t.Errorf("regressions = %+v, want a single firstPassRate entry", regs)
	}
}

func TestGateReportsMissingBaseline(t *testing.T) {
	set := &BaselineSet{Tasks: map[string]Baseline{}}
	stats := []Stats{{Task: "new-task", FirstPassRate: 1.0, GreenRate: 1.0, SpecMatchRate: 1.0}}
	res := Gate(set, stats, 0.2, 0.5)
	if res.OK() {
		t.Fatal("a task with no baseline passed the gate")
	}
	if len(res.Missing) != 1 || res.Missing[0] != "new-task" {
		t.Errorf("Missing = %v, want [new-task]", res.Missing)
	}
}

func TestRunCorpusAggregatesEachTask(t *testing.T) {
	// One deterministic scripted generator per task: the counter's fixed output
	// is spec-clean, so every task reaches green — but only greenfield-counter's
	// manifest matches its own expectation, so the others miss spec. That is
	// fine here: the assertion is on shape (one Stats per task, in order, N
	// samples each), not on the scripted content matching four different specs.
	newGen := func() Generator { return NewScriptedGenerator(counterFixed()) }
	corpus := []Task{GreenfieldCounter, InputWiring}
	stats, err := RunCorpus(context.Background(), newGen, corpus, 3, t.TempDir())
	if err != nil {
		t.Fatalf("RunCorpus: %v", err)
	}
	if len(stats) != len(corpus) {
		t.Fatalf("got %d task stats, want %d", len(stats), len(corpus))
	}
	for i, s := range stats {
		if s.Task != corpus[i].Name {
			t.Errorf("stats[%d].Task = %q, want %q (corpus order)", i, s.Task, corpus[i].Name)
		}
		if s.N != 3 {
			t.Errorf("stats[%d].N = %d, want 3", i, s.N)
		}
	}
}
