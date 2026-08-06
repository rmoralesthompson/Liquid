# ergobench testdata

## `ergonomics_baseline.json`

The committed reference the **nightly Tier B regression gate** compares against
(ADR-0001, #71). It records, per corpus task, the ergonomics distribution a real
model produced — first-pass rate, green rate, spec-match rate, and mean
repairs-to-green — plus the model and sample count that produced them.

It is **not fabricated and not hand-edited**: it is written from a real live run
and reviewed as a normal commit. A later nightly run gates against it with a
tolerance band (`Baseline.CheckRegression`), so an ordinary sampling wobble does
not flap the gate but a genuine degradation of the agent loop fails it.

### Recording / re-baselining

```sh
ANTHROPIC_API_KEY=… LIQUID_ERGO_UPDATE_BASELINE=1 \
  go test -v -tags ergolive -run TestNightlyRegressionGate \
  ./cmd/liquid/internal/ergobench
```

That writes `ergonomics_baseline.json` from the run instead of gating; commit the
result. Re-baseline deliberately (e.g. when the corpus grows, the pinned model
changes, or an intended improvement shifts the numbers) — the diff is the record
of that decision. The same run is available in CI via the **Nightly Ergonomics**
workflow's `record` dispatch input, which uploads the file as an artifact.

Until this file exists the gate skips (there is nothing to compare against yet),
so the first scheduled run is green and informational.
