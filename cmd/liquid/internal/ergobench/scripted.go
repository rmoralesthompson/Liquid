package ergobench

import (
	"context"
	"fmt"

	"github.com/rmoralesthompson/liquid/cmd/liquid/internal/compiler"
)

// ScriptedGenerator is a deterministic [Generator] that returns a fixed
// sequence of file sets — one per Generate call — ignoring the prompt and
// diagnostics. It is what makes the harness loop testable without an LLM: a
// scripted "broken attempt, then fixed attempt" pins the exact loop behavior a
// regression must preserve, and it doubles as the fixture format the live
// generator's replay/debugging can reuse.
type ScriptedGenerator struct {
	attempts [][]File
	calls    int
}

// NewScriptedGenerator returns a generator that emits attempts[i] on its i-th
// Generate call. It errors if asked for more attempts than were scripted, which
// surfaces a test that under-provisioned its repair sequence rather than hanging.
func NewScriptedGenerator(attempts ...[]File) *ScriptedGenerator {
	return &ScriptedGenerator{attempts: attempts}
}

// Generate returns the next scripted file set. prompt, prior, and diags are
// ignored: a scripted generator is a fixed replay, not a reaction.
func (g *ScriptedGenerator) Generate(_ context.Context, _ string, _ []File, _ []compiler.Diagnostic) ([]File, error) {
	if g.calls >= len(g.attempts) {
		return nil, fmt.Errorf("scripted generator exhausted after %d attempts", len(g.attempts))
	}
	files := g.attempts[g.calls]
	g.calls++
	return files, nil
}
