package ergobench

// This file is the nightly regression gate's persistence and comparison layer
// (ADR-0001, #71). The live Tier B run produces a distribution per task; this
// turns that distribution into a stored, diffable reference and gates a later
// run against it with a tolerance band, so the agent-ergonomics claim is
// protected by a regression test the way the performance claim is by a
// benchmark. It is deliberately untagged: the load/save/compare logic is pure
// and deterministic, so it is exercised on every PR — only the live model that
// produces the numbers lives behind the ergolive tag.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// BaselineSet is the committed reference for the whole corpus: one [Baseline]
// per task, plus the provenance a later run needs to know it is comparing like
// with like. It is persisted as indented JSON alongside the corpus so a
// regression shows up as a diff against a reviewed file, and a re-baseline is an
// explicit commit.
type BaselineSet struct {
	// Model is the pinned model that produced these figures. A nightly run on a
	// different model is comparing against the wrong reference; the gate warns.
	Model string `json:"model"`
	// Samples is the per-task sample count the figures were aggregated from —
	// context for how much sampling noise the bands must absorb.
	Samples int `json:"samples"`
	// Tasks maps task name to its recorded baseline figures.
	Tasks map[string]Baseline `json:"tasks"`
}

// LoadBaselineSet reads a [BaselineSet] from path. A missing file returns
// (nil, nil) so a caller can tell "no baseline recorded yet" (the bootstrap
// case, before the first real run) apart from a genuine read or parse failure.
func LoadBaselineSet(path string) (*BaselineSet, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading baseline %s: %w", path, err)
	}
	var set BaselineSet
	if err := json.Unmarshal(data, &set); err != nil {
		return nil, fmt.Errorf("parsing baseline %s: %w", path, err)
	}
	return &set, nil
}

// Save writes the set to path as indented JSON with a trailing newline, so a
// recorded baseline is a readable, reviewable, diff-friendly committed file.
func (s *BaselineSet) Save(path string) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding baseline: %w", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating baseline dir: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing baseline %s: %w", path, err)
	}
	return nil
}

// BaselineFromStats projects a run's aggregated [Stats] into a storable
// [Baseline] — the gated subset of the figures. Variance is reported by the run
// but not stored, because the gate absorbs sampling spread through its tolerance
// band rather than by tracking variance directly.
func BaselineFromStats(s Stats) Baseline {
	return Baseline{
		Task:          s.Task,
		N:             s.N,
		FirstPassRate: s.FirstPassRate,
		GreenRate:     s.GreenRate,
		SpecMatchRate: s.SpecMatchRate,
		MeanRepairs:   s.MeanRepairs,
	}
}

// GateResult is the outcome of checking one corpus run against a [BaselineSet]:
// the metrics that regressed per task, and any task that ran without a baseline
// to gate against. A run passes only when both are empty.
type GateResult struct {
	// Regressions maps a task name to the metrics that fell outside the band.
	// A task that held is absent, not present with an empty slice.
	Regressions map[string][]Regression
	// Missing lists tasks that ran but have no baseline entry — a new corpus
	// task whose baseline has not been recorded yet. Sorted for stable output.
	Missing []string
}

// OK reports the run passed the gate: nothing regressed and every task that ran
// had a baseline to gate against.
func (r GateResult) OK() bool {
	return len(r.Regressions) == 0 && len(r.Missing) == 0
}

// Gate checks each task's stats against its baseline with the given tolerance
// bands (see [Baseline.CheckRegression]). A task present in stats but absent
// from the set is reported in Missing — adding a corpus task must be a
// deliberate re-baseline, not a silent pass. A baseline for a task not in stats
// is ignored: the live run is the source of truth for what actually ran.
func Gate(set *BaselineSet, stats []Stats, rateBand, repairBand float64) GateResult {
	res := GateResult{Regressions: map[string][]Regression{}}
	for _, s := range stats {
		base, ok := set.Tasks[s.Task]
		if !ok {
			res.Missing = append(res.Missing, s.Task)
			continue
		}
		if regs := base.CheckRegression(s, rateBand, repairBand); len(regs) > 0 {
			res.Regressions[s.Task] = regs
		}
	}
	sort.Strings(res.Missing)
	return res
}
