package ergobench

// Stats aggregates the samples of one task into the figures ADR-0001 reports:
// rates for the 0/1 metrics and a mean+variance for repairs-to-green, so a
// result is always a distribution, never a single point.
type Stats struct {
	// Task is the task name these figures summarize.
	Task string
	// N is the number of samples aggregated.
	N int
	// FirstPassRate is the fraction of samples whose first attempt built clean.
	FirstPassRate float64
	// GreenRate is the fraction that reached a clean build within CapN repairs.
	GreenRate float64
	// SpecMatchRate is the fraction that reached green and matched the spec.
	SpecMatchRate float64
	// MeanRepairs is the mean repair iterations across samples; a gave-up sample
	// contributes CapN.
	MeanRepairs float64
	// VarRepairs is the population variance of repairs across samples.
	VarRepairs float64
}

// Aggregate summarizes samples for the named task. It returns a zero-N Stats
// for an empty slice rather than dividing by zero.
func Aggregate(task string, samples []Sample) Stats {
	s := Stats{Task: task, N: len(samples)}
	if len(samples) == 0 {
		return s
	}

	var firstPass, green, spec, repairSum int
	for _, sm := range samples {
		if sm.FirstPassClean {
			firstPass++
		}
		if sm.ReachedGreen {
			green++
		}
		if sm.ReachedGreen && sm.SpecMatch {
			spec++
		}
		repairSum += sm.Repairs
	}

	n := float64(len(samples))
	s.FirstPassRate = float64(firstPass) / n
	s.GreenRate = float64(green) / n
	s.SpecMatchRate = float64(spec) / n
	s.MeanRepairs = float64(repairSum) / n

	var sq float64
	for _, sm := range samples {
		d := float64(sm.Repairs) - s.MeanRepairs
		sq += d * d
	}
	s.VarRepairs = sq / n
	return s
}

// Baseline is a stored reference result for a task: the figures a prior run
// established, which a later run is gated against. It is persisted as JSON
// alongside the corpus so a regression is a diff against a committed file.
type Baseline struct {
	Task          string  `json:"task"`
	N             int     `json:"n"`
	FirstPassRate float64 `json:"firstPassRate"`
	GreenRate     float64 `json:"greenRate"`
	SpecMatchRate float64 `json:"specMatchRate"`
	MeanRepairs   float64 `json:"meanRepairs"`
}

// Regression is one metric that fell outside the tolerance band versus the
// baseline: the metric name, the baseline and observed values, and the band
// applied. A rate regressed when it dropped below baseline − band; mean repairs
// regressed when it rose above baseline + repairBand.
type Regression struct {
	Metric   string
	Baseline float64
	Got      float64
	Band     float64
}

// CheckRegression compares stats against the baseline with tolerance bands and
// returns one Regression per metric that degraded. Because a real model is
// stochastic, the comparison is a band, not an exact assertion, so the gate
// does not flap on ordinary sampling noise (ADR-0001). rateBand applies to the
// three 0..1 rates (higher is better); repairBand applies to mean repairs
// (lower is better). An empty result means no regression.
func (b Baseline) CheckRegression(s Stats, rateBand, repairBand float64) []Regression {
	var out []Regression
	rate := func(name string, base, got float64) {
		if got < base-rateBand {
			out = append(out, Regression{Metric: name, Baseline: base, Got: got, Band: rateBand})
		}
	}
	rate("firstPassRate", b.FirstPassRate, s.FirstPassRate)
	rate("greenRate", b.GreenRate, s.GreenRate)
	rate("specMatchRate", b.SpecMatchRate, s.SpecMatchRate)
	if s.MeanRepairs > b.MeanRepairs+repairBand {
		out = append(out, Regression{Metric: "meanRepairs", Baseline: b.MeanRepairs, Got: s.MeanRepairs, Band: repairBand})
	}
	return out
}
