package ergobench

import (
	"context"
	"testing"
)

// The counter source below is the greenfield task's known-good output: it
// builds and vets clean, and its manifest matches GreenfieldCounter.Expect
// (verified against `liquid manifest --json`). The broken variant references a
// misspelled field, so it fails vet with an LSX004 the loop must repair; the
// spec-miss variant builds clean but drops the button, so it reaches green yet
// fails the manifest oracle.

func counterGo() File {
	return File{Name: "counter.go", Content: `package counter

// Counter is a greenfield interactive component.
type Counter struct {
	HydroID string
	Count   int
}

// Selector returns the custom element tag.
func (c *Counter) Selector() string { return "app-counter" }

// Increment bumps the count.
func (c *Counter) Increment() { c.Count++ }
`}
}

func counterFixed() []File {
	return []File{counterGo(), {Name: "counter.lsx", Content: `<div [hydroId]>
  <span>{{ Count }}</span>
  <button (click)="Increment">+</button>
</div>
`}}
}

func counterBroken() []File {
	return []File{counterGo(), {Name: "counter.lsx", Content: `<div [hydroId]>
  <span>{{ Kount }}</span>
  <button (click)="Increment">+</button>
</div>
`}}
}

// counterNoAction builds clean but has no button, so no Increment action is
// wired — green, but a spec miss against the expectation.
func counterNoAction() []File {
	return []File{counterGo(), {Name: "counter.lsx", Content: `<div [hydroId]>
  <span>{{ Count }}</span>
</div>
`}}
}

func TestRunFirstPassClean(t *testing.T) {
	gen := NewScriptedGenerator(counterFixed())
	s, err := Run(context.Background(), gen, GreenfieldCounter, t.TempDir())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := Sample{FirstPassClean: true, Repairs: 0, ReachedGreen: true, SpecMatch: true}
	if s != want {
		t.Errorf("sample = %+v, want %+v", s, want)
	}
}

func TestRunRepairsToGreen(t *testing.T) {
	// Attempt 0 is broken (LSX004), attempt 1 is fixed: one repair to green.
	gen := NewScriptedGenerator(counterBroken(), counterFixed())
	s, err := Run(context.Background(), gen, GreenfieldCounter, t.TempDir())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := Sample{FirstPassClean: false, Repairs: 1, ReachedGreen: true, SpecMatch: true}
	if s != want {
		t.Errorf("sample = %+v, want %+v", s, want)
	}
}

func TestRunGivesUpAtCapN(t *testing.T) {
	task := GreenfieldCounter
	task.CapN = 2
	// Every attempt is broken: 1 initial + 2 repairs, all fail.
	gen := NewScriptedGenerator(counterBroken(), counterBroken(), counterBroken())
	s, err := Run(context.Background(), gen, task, t.TempDir())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := Sample{FirstPassClean: false, Repairs: 2, ReachedGreen: false, SpecMatch: false}
	if s != want {
		t.Errorf("sample = %+v, want %+v", s, want)
	}
}

func TestRunGreenButSpecMiss(t *testing.T) {
	gen := NewScriptedGenerator(counterNoAction())
	s, err := Run(context.Background(), gen, GreenfieldCounter, t.TempDir())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !s.ReachedGreen {
		t.Fatalf("component must build clean; sample = %+v", s)
	}
	if s.SpecMatch {
		t.Error("SpecMatch = true, want false: the manifest has no Increment action")
	}
}

func TestSpecMatchReasonsAreSpecific(t *testing.T) {
	dir := t.TempDir()
	if err := writeModule(dir, GreenfieldCounter, counterNoAction()); err != nil {
		t.Fatalf("writeModule: %v", err)
	}
	ok, reason := specMatch(context.Background(), dir, GreenfieldCounter.Expect)
	if ok {
		t.Fatal("specMatch = true, want false")
	}
	if reason != `missing action "Increment"` {
		t.Errorf("reason = %q, want %q", reason, `missing action "Increment"`)
	}
}

func TestRunNProducesOneSamplePerRun(t *testing.T) {
	newGen := func() Generator { return NewScriptedGenerator(counterFixed()) }
	samples, err := RunN(context.Background(), newGen, GreenfieldCounter, 3, t.TempDir())
	if err != nil {
		t.Fatalf("RunN: %v", err)
	}
	if len(samples) != 3 {
		t.Fatalf("got %d samples, want 3", len(samples))
	}
	for i, s := range samples {
		if !s.ReachedGreen || !s.SpecMatch {
			t.Errorf("sample %d = %+v, want green + spec match", i, s)
		}
	}
}

func TestAggregateComputesRatesAndVariance(t *testing.T) {
	samples := []Sample{
		{FirstPassClean: true, Repairs: 0, ReachedGreen: true, SpecMatch: true},
		{FirstPassClean: false, Repairs: 1, ReachedGreen: true, SpecMatch: true},
		{FirstPassClean: false, Repairs: 2, ReachedGreen: true, SpecMatch: false},
		{FirstPassClean: false, Repairs: 3, ReachedGreen: false, SpecMatch: false},
	}
	s := Aggregate("t", samples)
	if s.N != 4 {
		t.Fatalf("N = %d, want 4", s.N)
	}
	if s.FirstPassRate != 0.25 {
		t.Errorf("FirstPassRate = %v, want 0.25", s.FirstPassRate)
	}
	if s.GreenRate != 0.75 {
		t.Errorf("GreenRate = %v, want 0.75", s.GreenRate)
	}
	if s.SpecMatchRate != 0.5 {
		t.Errorf("SpecMatchRate = %v, want 0.5", s.SpecMatchRate)
	}
	// Repairs 0,1,2,3 → mean 1.5, population variance 1.25.
	if s.MeanRepairs != 1.5 {
		t.Errorf("MeanRepairs = %v, want 1.5", s.MeanRepairs)
	}
	if s.VarRepairs != 1.25 {
		t.Errorf("VarRepairs = %v, want 1.25", s.VarRepairs)
	}
}

func TestAggregateEmptyIsZeroNotPanic(t *testing.T) {
	s := Aggregate("t", nil)
	if s.N != 0 || s.FirstPassRate != 0 {
		t.Errorf("empty aggregate = %+v, want zero", s)
	}
}

func TestCheckRegressionFlagsDropsBeyondBand(t *testing.T) {
	base := Baseline{Task: "t", FirstPassRate: 0.8, GreenRate: 1.0, SpecMatchRate: 0.9, MeanRepairs: 1.0}

	// Within the band: a small dip and a small repair rise are tolerated.
	ok := Stats{Task: "t", FirstPassRate: 0.75, GreenRate: 1.0, SpecMatchRate: 0.9, MeanRepairs: 1.1}
	if regs := base.CheckRegression(ok, 0.1, 0.5); len(regs) != 0 {
		t.Errorf("within-band stats flagged: %+v", regs)
	}

	// Beyond the band: first-pass collapses and repairs balloon.
	bad := Stats{Task: "t", FirstPassRate: 0.5, GreenRate: 1.0, SpecMatchRate: 0.9, MeanRepairs: 2.0}
	regs := base.CheckRegression(bad, 0.1, 0.5)
	got := map[string]bool{}
	for _, r := range regs {
		got[r.Metric] = true
	}
	if !got["firstPassRate"] {
		t.Error("firstPassRate drop not flagged")
	}
	if !got["meanRepairs"] {
		t.Error("meanRepairs rise not flagged")
	}
	if got["greenRate"] || got["specMatchRate"] {
		t.Errorf("unchanged metrics flagged: %+v", regs)
	}
}
