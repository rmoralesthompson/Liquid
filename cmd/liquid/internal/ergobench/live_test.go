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

	n := 5
	if v := os.Getenv("LIQUID_ERGO_SAMPLES"); v != "" {
		parsed, err := strconv.Atoi(v)
		if err != nil || parsed < 1 {
			t.Fatalf("LIQUID_ERGO_SAMPLES = %q, want a positive integer", v)
		}
		n = parsed
	}

	gen, err := NewLiveGenerator()
	if err != nil {
		t.Fatalf("NewLiveGenerator: %v", err)
	}
	// LiveGenerator holds no per-attempt state, so one instance serves every
	// sample; RunN drives it sequentially.
	newGen := func() Generator { return gen }

	ctx := context.Background()
	for _, task := range Corpus {
		samples, err := RunN(ctx, newGen, task, n, t.TempDir())
		if err != nil {
			t.Fatalf("RunN(%s): %v", task.Name, err)
		}
		s := Aggregate(task.Name, samples)
		t.Logf("task=%s n=%d firstPassRate=%.2f greenRate=%.2f specMatchRate=%.2f meanRepairs=%.2f varRepairs=%.2f",
			s.Task, s.N, s.FirstPassRate, s.GreenRate, s.SpecMatchRate, s.MeanRepairs, s.VarRepairs)
	}
}
