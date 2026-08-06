//go:build ergolive

package ergobench

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/rmoralesthompson/liquid/cmd/liquid/internal/compiler"
)

// baselinePath is the committed nightly baseline the regression gate compares
// against, relative to this package directory (where `go test` runs).
const baselinePath = "testdata/ergonomics_baseline.json"

// updateBaselineEnv, when set, switches the gate test into record mode: it
// writes the baseline from the run instead of gating against it.
const updateBaselineEnv = "LIQUID_ERGO_UPDATE_BASELINE"

// The tests below split into three groups, all compiled only under `ergolive`:
// pure request/response logic (no network), a stubbed end-to-end HTTP path
// (deterministic, no API key), and the live ergonomics run (skips without a key).

func TestUserMessageFirstAttemptIsJustThePrompt(t *testing.T) {
	got := userMessage("build a counter", nil, nil)
	if got != "build a counter" {
		t.Errorf("first-attempt message = %q, want the bare prompt", got)
	}
}

func TestUserMessageRepairIncludesPriorAndDiags(t *testing.T) {
	prior := []File{{Name: "counter.lsx", Content: "<div>{{ Kount }}</div>"}}
	diags := []compiler.Diagnostic{{
		File:       "counter.lsx",
		Line:       1,
		Col:        6,
		Severity:   compiler.SeverityError,
		Code:       compiler.Code("LSX004"),
		Message:    "unknown field Kount",
		Suggestion: "did you mean Count?",
	}}
	got := userMessage("build a counter", prior, diags)

	for _, want := range []string{
		"build a counter",        // the task is still present
		"counter.lsx",            // the prior file is quoted
		"{{ Kount }}",            // with its content
		"LSX004",                 // the diagnostic code
		"unknown field Kount",    // the message
		"did you mean Count?",    // the suggestion
		"complete corrected set", // the fix instruction
	} {
		if !strings.Contains(got, want) {
			t.Errorf("repair message missing %q\n---\n%s", want, got)
		}
	}
}

func TestBuildRequestPinsModelAndUsesStructuredOutput(t *testing.T) {
	g := &LiveGenerator{model: defaultModel, maxTokens: defaultMaxTokens}
	req := g.buildRequest("make x", nil, nil)

	if req.Model != defaultModel {
		t.Errorf("model = %q, want %q", req.Model, defaultModel)
	}
	if req.MaxTokens != defaultMaxTokens {
		t.Errorf("maxTokens = %d, want %d", req.MaxTokens, defaultMaxTokens)
	}
	if req.System == "" {
		t.Error("system primer is empty")
	}
	if req.OutputConfig == nil || req.OutputConfig.Format.Type != "json_schema" {
		t.Fatalf("output config = %+v, want a json_schema format", req.OutputConfig)
	}
	// The schema must be valid JSON so the API accepts it.
	if !json.Valid(req.OutputConfig.Format.Schema) {
		t.Error("output schema is not valid JSON")
	}
}

func TestParseFilesExtractsFileSet(t *testing.T) {
	// Structured output arrives as JSON text inside a text block.
	body := `{"stop_reason":"end_turn","content":[{"type":"text","text":"{\"files\":[{\"name\":\"counter.go\",\"content\":\"package counter\"},{\"name\":\"counter.lsx\",\"content\":\"<div></div>\"}]}"}]}`
	files, err := parseFiles([]byte(body))
	if err != nil {
		t.Fatalf("parseFiles: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("got %d files, want 2", len(files))
	}
	if files[0].Name != "counter.go" || files[0].Content != "package counter" {
		t.Errorf("file[0] = %+v", files[0])
	}
	if files[1].Name != "counter.lsx" {
		t.Errorf("file[1].Name = %q, want counter.lsx", files[1].Name)
	}
}

func TestParseFilesRefusalIsAnError(t *testing.T) {
	body := `{"stop_reason":"refusal","content":[]}`
	if _, err := parseFiles([]byte(body)); err == nil {
		t.Error("parseFiles on a refusal returned no error")
	}
}

func TestParseFilesEmptyFileSetIsAnError(t *testing.T) {
	body := `{"stop_reason":"end_turn","content":[{"type":"text","text":"{\"files\":[]}"}]}`
	if _, err := parseFiles([]byte(body)); err == nil {
		t.Error("parseFiles on an empty file set returned no error")
	}
}

// TestGenerateAgainstStub drives the full HTTP path against a local stub: it
// asserts the request carries the auth/version headers, the pinned model, and the
// structured-output config, and that the reply is decoded into files — all with
// no real API key.
func TestGenerateAgainstStub(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("x-api-key"); got != "test-key" {
			t.Errorf("x-api-key = %q, want test-key", got)
		}
		if got := r.Header.Get("anthropic-version"); got != apiVersion {
			t.Errorf("anthropic-version = %q, want %q", got, apiVersion)
		}
		var req messagesRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decoding request: %v", err)
		}
		if req.Model != defaultModel {
			t.Errorf("request model = %q, want %q", req.Model, defaultModel)
		}
		if req.OutputConfig == nil {
			t.Error("request has no output_config")
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"stop_reason":"end_turn","content":[{"type":"text","text":"{\"files\":[{\"name\":\"x.go\",\"content\":\"package x\"}]}"}]}`))
	}))
	defer srv.Close()

	g := &LiveGenerator{
		client:    srv.Client(),
		apiKey:    "test-key",
		model:     defaultModel,
		maxTokens: defaultMaxTokens,
		baseURL:   srv.URL,
	}
	files, err := g.Generate(context.Background(), "make x", nil, nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(files) != 1 || files[0].Name != "x.go" {
		t.Fatalf("files = %+v, want one x.go", files)
	}
}

func TestGenerateSurfacesNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"rate_limit_error"}}`))
	}))
	defer srv.Close()

	g := &LiveGenerator{
		client:    srv.Client(),
		apiKey:    "test-key",
		model:     defaultModel,
		maxTokens: defaultMaxTokens,
		baseURL:   srv.URL,
	}
	if _, err := g.Generate(context.Background(), "make x", nil, nil); err == nil {
		t.Error("Generate returned no error on a 429 response")
	}
}

// TestLiveErgonomics is the on-demand entry point: it runs the corpus through the
// live model and logs the ergonomics distribution per task. It skips without a
// key so a bare `go test -tags ergolive` still exercises the deterministic tests
// above. LIQUID_ERGO_SAMPLES sets the sample count (default 5).
func TestLiveErgonomics(t *testing.T) {
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		t.Skip("ANTHROPIC_API_KEY not set; skipping live ergonomics run")
	}

	gen := liveGenOrFatal(t)
	stats, err := RunCorpus(context.Background(), func() Generator { return gen }, Corpus, envSamples(t), t.TempDir())
	if err != nil {
		t.Fatalf("RunCorpus: %v", err)
	}
	logStats(t, stats)
}

// TestNightlyRegressionGate is the nightly Tier B gate (ADR-0001, #71): it runs
// the live corpus and fails if any task regressed past the tolerance band versus
// the committed baseline. It skips without a key, so a bare `go test -tags
// ergolive` still exercises the deterministic tests above.
//
// Two modes:
//
//   - record: with LIQUID_ERGO_UPDATE_BASELINE set, it writes
//     testdata/ergonomics_baseline.json from this run and does NOT gate. This is
//     how the first baseline and any deliberate re-baseline are captured — from a
//     real run, never fabricated. Commit the resulting file.
//   - gate (default): it compares against the committed baseline. A missing file
//     skips (there is nothing to gate yet); a task with no baseline entry or a
//     metric outside the band fails.
//
// The comparison is a band, not an exact assertion, so it does not flap on
// ordinary sampling noise. Tune with LIQUID_ERGO_RATE_BAND (default 0.2, applied
// to the three 0..1 rates) and LIQUID_ERGO_REPAIR_BAND (default 1.0, applied to
// mean repairs). LIQUID_ERGO_SAMPLES sets the per-task sample count (default 5).
func TestNightlyRegressionGate(t *testing.T) {
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		t.Skip("ANTHROPIC_API_KEY not set; skipping nightly regression gate")
	}

	gen := liveGenOrFatal(t)
	n := envSamples(t)
	stats, err := RunCorpus(context.Background(), func() Generator { return gen }, Corpus, n, t.TempDir())
	if err != nil {
		t.Fatalf("RunCorpus: %v", err)
	}
	logStats(t, stats)

	if os.Getenv(updateBaselineEnv) != "" {
		set := &BaselineSet{Model: gen.Model(), Samples: n, Tasks: make(map[string]Baseline, len(stats))}
		for _, s := range stats {
			set.Tasks[s.Task] = BaselineFromStats(s)
		}
		if err = set.Save(baselinePath); err != nil {
			t.Fatalf("recording baseline: %v", err)
		}
		t.Logf("recorded baseline for %d task(s) to %s (model=%s, samples=%d); commit this file",
			len(set.Tasks), baselinePath, set.Model, n)
		return
	}

	set, err := LoadBaselineSet(baselinePath)
	if err != nil {
		t.Fatalf("loading baseline: %v", err)
	}
	if set == nil {
		t.Skipf("no baseline recorded at %s; run once with %s=1 to record", baselinePath, updateBaselineEnv)
	}
	if set.Model != "" && set.Model != gen.Model() {
		t.Logf("warning: baseline model %q != run model %q; comparison may not be like-for-like",
			set.Model, gen.Model())
	}

	rateBand := envFloat(t, "LIQUID_ERGO_RATE_BAND", 0.2)
	repairBand := envFloat(t, "LIQUID_ERGO_REPAIR_BAND", 1.0)
	res := Gate(set, stats, rateBand, repairBand)

	for _, task := range res.Missing {
		t.Errorf("task %q has no baseline entry; record one with %s=1 before gating", task, updateBaselineEnv)
	}
	for task, regs := range res.Regressions {
		for _, r := range regs {
			t.Errorf("regression in %q: %s = %.2f, baseline %.2f (band %.2f)",
				task, r.Metric, r.Got, r.Baseline, r.Band)
		}
	}
	if res.OK() {
		t.Logf("nightly gate passed: %d task(s) within band (rate=%.2f, repair=%.2f)",
			len(stats), rateBand, repairBand)
	}
}

// liveGenOrFatal builds the live generator or fails the test. LiveGenerator
// holds no per-attempt state, so one instance serves every sample.
func liveGenOrFatal(t *testing.T) *LiveGenerator {
	t.Helper()
	gen, err := NewLiveGenerator()
	if err != nil {
		t.Fatalf("NewLiveGenerator: %v", err)
	}
	return gen
}

// logStats emits one distribution line per task, the human-readable record of a
// run that lands in the CI log regardless of the gate verdict.
func logStats(t *testing.T, stats []Stats) {
	t.Helper()
	for _, s := range stats {
		t.Logf("task=%s n=%d firstPassRate=%.2f greenRate=%.2f specMatchRate=%.2f meanRepairs=%.2f varRepairs=%.2f",
			s.Task, s.N, s.FirstPassRate, s.GreenRate, s.SpecMatchRate, s.MeanRepairs, s.VarRepairs)
	}
}

// envSamples reads LIQUID_ERGO_SAMPLES (default 5), failing on a non-positive or
// unparseable value so a misconfigured nightly job is loud, not silently 5.
func envSamples(t *testing.T) int {
	t.Helper()
	v := os.Getenv("LIQUID_ERGO_SAMPLES")
	if v == "" {
		return 5
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		t.Fatalf("LIQUID_ERGO_SAMPLES = %q, want a positive integer", v)
	}
	return n
}

// envFloat reads a float env var, falling back to def when unset and failing on
// an unparseable value.
func envFloat(t *testing.T, name string, def float64) float64 {
	t.Helper()
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		t.Fatalf("%s = %q, want a float", name, v)
	}
	return f
}
