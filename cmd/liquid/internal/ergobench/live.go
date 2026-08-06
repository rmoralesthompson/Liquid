//go:build ergolive

// This file is the Tier B Layer 2 live generator (ADR-0001, #71): a [Generator]
// backed by a real Anthropic model, so the harness can measure the ergonomics an
// actual agent experiences rather than a scripted stand-in.
//
// It is deliberately compiled only under the `ergolive` build tag and needs
// ANTHROPIC_API_KEY, so it never runs on the PR critical path (a real model is
// stochastic and costly — ADR-0001, "Two tiers"). The scripted generator guards
// the loop deterministically per-PR; this generator runs nightly/on-demand:
//
//	ANTHROPIC_API_KEY=… go test -tags ergolive -run TestLiveErgonomics ./cmd/liquid/internal/ergobench
//
// It talks to the Messages API over stdlib net/http — no SDK — to honour the
// project's prefer-stdlib rule, and constrains the reply with a structured-output
// schema so the file set parses deterministically instead of being scraped from
// prose. Model and token cap are pinned (LIQUID_ERGO_MODEL overrides the model)
// so a run is reproducible up to the model's own sampling variance, which the
// harness already reports as variance across samples.

package ergobench

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/rmoralesthompson/liquid/cmd/liquid/internal/compiler"
)

const (
	// defaultModel is pinned so nightly runs are comparable across days; override
	// with LIQUID_ERGO_MODEL to benchmark a different model.
	defaultModel = "claude-opus-4-8"
	// defaultMaxTokens is generous for a single small component pair.
	defaultMaxTokens = 8192
	// messagesURL is the Anthropic Messages endpoint.
	messagesURL = "https://api.anthropic.com/v1/messages"
	// apiVersion is the required anthropic-version header value.
	apiVersion = "2023-06-01"
)

// LiveGenerator is a [Generator] that asks an Anthropic model to emit a Liquid
// component's source, feeding build diagnostics back on repair attempts. It is
// safe for sequential use across a task's attempts (the harness never calls one
// generator concurrently); it holds no per-attempt state, so a single instance
// can also be reused across samples if a caller wants to.
type LiveGenerator struct {
	client    *http.Client
	apiKey    string
	model     string
	maxTokens int
	// baseURL is the Messages endpoint; overridable so the request/response
	// marshaling can be exercised against a stub in unit tests.
	baseURL string
}

// NewLiveGenerator builds a generator from the environment: ANTHROPIC_API_KEY is
// required (returns an error if unset so a nightly job fails loudly rather than
// silently benchmarking nothing), and LIQUID_ERGO_MODEL optionally overrides the
// pinned model.
func NewLiveGenerator() (*LiveGenerator, error) {
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		return nil, fmt.Errorf("ANTHROPIC_API_KEY is not set")
	}
	model := os.Getenv("LIQUID_ERGO_MODEL")
	if model == "" {
		model = defaultModel
	}
	return &LiveGenerator{
		client:    &http.Client{Timeout: 2 * time.Minute},
		apiKey:    key,
		model:     model,
		maxTokens: defaultMaxTokens,
		baseURL:   messagesURL,
	}, nil
}

// Model returns the pinned model these figures are attributed to, so the
// nightly gate can record it as baseline provenance and warn if a later run is
// comparing against a baseline captured on a different model.
func (g *LiveGenerator) Model() string { return g.model }

// Generate asks the model for the component's files. On the first attempt prior
// and diags are nil and the model sees only the task; on a repair attempt the
// prior files and their diagnostics are folded into the message so the model
// fixes exactly what the build reported.
func (g *LiveGenerator) Generate(ctx context.Context, prompt string, prior []File, diags []compiler.Diagnostic) ([]File, error) {
	raw, err := json.Marshal(g.buildRequest(prompt, prior, diags))
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, g.baseURL, bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("x-api-key", g.apiKey)
	httpReq.Header.Set("anthropic-version", apiVersion)

	resp, err := g.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("calling Messages API: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("messages API status %d: %s", resp.StatusCode, body)
	}
	return parseFiles(body)
}

// --- request/response shapes (the subset of the Messages API this uses) ---

type messagesRequest struct {
	Model        string        `json:"model"`
	MaxTokens    int           `json:"max_tokens"`
	System       string        `json:"system"`
	Messages     []message     `json:"messages"`
	OutputConfig *outputConfig `json:"output_config,omitempty"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type outputConfig struct {
	Format outputFormat `json:"format"`
}

type outputFormat struct {
	Type   string          `json:"type"`
	Schema json.RawMessage `json:"schema"`
}

type messagesResponse struct {
	StopReason string `json:"stop_reason"`
	Content    []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

// filesSchema constrains the reply to {files: [{name, content}]} so the output
// is machine-parseable. It obeys the structured-output schema rules: every
// object sets additionalProperties:false and lists its required keys.
const filesSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["files"],
  "properties": {
    "files": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["name", "content"],
        "properties": {
          "name": {"type": "string"},
          "content": {"type": "string"}
        }
      }
    }
  }
}`

// buildRequest assembles the Messages request: the pinned model and token cap,
// the Liquid system primer, the user message (task, plus prior files + diags on
// a repair), and the structured-output format. Temperature is deliberately
// unset — it is rejected on the pinned model, and prompting carries the intent.
func (g *LiveGenerator) buildRequest(prompt string, prior []File, diags []compiler.Diagnostic) messagesRequest {
	return messagesRequest{
		Model:     g.model,
		MaxTokens: g.maxTokens,
		System:    systemPrompt,
		Messages:  []message{{Role: "user", Content: userMessage(prompt, prior, diags)}},
		OutputConfig: &outputConfig{Format: outputFormat{
			Type:   "json_schema",
			Schema: json.RawMessage(filesSchema),
		}},
	}
}

// userMessage is the task prompt on a first attempt, and the task plus the prior
// files and their diagnostics on a repair — the exact feedback an agent editing
// its own broken output would see.
func userMessage(prompt string, prior []File, diags []compiler.Diagnostic) string {
	if len(prior) == 0 && len(diags) == 0 {
		return prompt
	}

	var b strings.Builder
	b.WriteString(prompt)
	b.WriteString("\n\nYour previous attempt produced these files:\n")
	for _, f := range prior {
		fmt.Fprintf(&b, "\n=== %s ===\n%s\n", f.Name, f.Content)
	}
	b.WriteString("\nThe Liquid build reported these diagnostics, which you must fix:\n")
	for _, d := range diags {
		fmt.Fprintf(&b, "- %s:%d:%d [%s %s] %s", d.File, d.Line, d.Col, d.Severity, d.Code, d.Message)
		if d.Suggestion != "" {
			fmt.Fprintf(&b, " (suggestion: %s)", d.Suggestion)
		}
		b.WriteByte('\n')
	}
	b.WriteString("\nReturn the complete corrected set of files.")
	return b.String()
}

// parseFiles turns a Messages response into the emitted file set. Structured
// output lands the JSON in text blocks, so it concatenates them and decodes the
// {files:[…]} payload. A refusal, an empty body (e.g. a max_tokens cutoff), or an
// empty file set is reported as an error the loop surfaces rather than a silent
// zero-file attempt.
func parseFiles(body []byte) ([]File, error) {
	var resp messagesResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	if resp.StopReason == "refusal" {
		return nil, fmt.Errorf("model refused the request")
	}

	var sb strings.Builder
	for _, c := range resp.Content {
		if c.Type == "text" {
			sb.WriteString(c.Text)
		}
	}
	text := sb.String()
	if text == "" {
		return nil, fmt.Errorf("response has no text content (stop_reason=%q)", resp.StopReason)
	}

	var payload struct {
		Files []File `json:"files"`
	}
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		return nil, fmt.Errorf("decoding files payload (stop_reason=%q): %w", resp.StopReason, err)
	}
	if len(payload.Files) == 0 {
		return nil, fmt.Errorf("model returned no files")
	}
	return payload.Files, nil
}

// systemPrompt teaches the model Liquid's component contract. It is a generic
// primer — the illustrative component below is intentionally NOT any corpus task,
// so the measurement reflects the model applying the framework's affordances, not
// reciting a memorized answer. It states the current scaffold constraint (no
// liquid-runtime import) honestly, because that is the build environment these
// tasks actually compile in.
const systemPrompt = `You are an expert Go developer writing components for Liquid, a server-driven UI framework for Go with an Angular-style component model.

A Liquid component is a pair of files sharing a base name:
- <name>.go — a Go source file declaring the component struct and its methods.
- <name>.lsx — an HTML template (".lsx") that renders the struct.

The Go file:
- Declares a package (any lowercase identifier) and a struct.
- Exported struct fields hold the template's data.
- A method "func (c *T) Selector() string" returns the component's custom-element tag (e.g. "app-counter").
- Action methods are invoked by event bindings and have the no-argument signature "func (c *T) Name()". They mutate the struct's fields.
- An interactive component (one wired to browser events or server push) must have a "HydroID string" field; the framework populates it.

The .lsx template is HTML plus these directives:
- {{ FieldName }} interpolates an exported struct field (dot-paths allowed, e.g. {{ User.Name }}).
- *goIf="Field" renders the host element only when the boolean field is truthy.
- *goFor="let item of Field" repeats the host element per slice item; reference the item as {{ $item }}.
- (click)="Method" and (submit)="Method" bind a browser event to an action method. Write the method name WITHOUT parentheses.
- [childField]="ParentField" passes a parent field into a nested child component.
- [hydroId] on an element marks the component's patchable root; it requires the "HydroID string" field. Put it on the root element of any interactive component.
- A <form> element automatically gets a hidden CSRF input; a component using <form> needs a "CSRFToken string" field.

Illustrative component (a greeting — this is NOT a task you will be asked to build, just a shape reference):

  greeting.go:
    package greeting

    type Greeting struct {
        HydroID string
        Name    string
    }

    func (g *Greeting) Selector() string { return "app-greeting" }

    func (g *Greeting) Reset() { g.Name = "" }

  greeting.lsx:
    <div [hydroId]>
      <p>Hello, {{ Name }}</p>
      <button (click)="Reset">Clear</button>
    </div>

Constraints for your output:
- Emit exactly the two files the task needs, at the module root, named <name>.go and <name>.lsx.
- The component must be self-contained Go: do NOT import the liquid runtime package or any external dependency. Use only the standard library if you need anything, and prefer no imports at all. Action handlers use the no-argument signature.
- Do NOT emit a go.mod; the harness provides one.
- Follow the task's requested selector, struct name, fields, and actions exactly.`
