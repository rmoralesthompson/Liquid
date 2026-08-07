package liquid_test

import (
	"net/http"
	"strings"
	"testing"
)

// TestMorphScriptIsServed proves the vendored Idiomorph library (#106) is served
// under the framework namespace as a cacheable JS file.
func TestMorphScriptIsServed(t *testing.T) {
	srv := newServer(t, "/", &counter{})

	resp, body := get(t, srv.URL+"/liquid/idiomorph.js")

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") {
		t.Errorf("Content-Type = %q, want text/javascript", ct)
	}
	if !strings.Contains(body, "Idiomorph") {
		t.Error("served idiomorph.js does not expose the Idiomorph global")
	}
}

// TestShellLoadsMorphBeforeRuntime pins the load order: the morph library must
// be defined before the runtime script runs applyPatch. Both are deferred, so
// they execute in document order — morph first.
func TestShellLoadsMorphBeforeRuntime(t *testing.T) {
	srv := newServer(t, "/", &counter{})

	_, body := get(t, srv.URL+"/")

	morphIdx := strings.Index(body, "/liquid/idiomorph.js")
	runtimeIdx := strings.Index(body, "/liquid/runtime.js")
	if morphIdx < 0 || runtimeIdx < 0 {
		t.Fatalf("shell missing a script tag: idiomorph=%d runtime=%d\n%s", morphIdx, runtimeIdx, body)
	}
	if morphIdx > runtimeIdx {
		t.Error("idiomorph.js must load before runtime.js so Idiomorph is defined when applyPatch runs")
	}
}

// TestRuntimeMorphsPatches pins that applyPatch reconciles a patch through
// Idiomorph rather than a wholesale swap (#106).
func TestRuntimeMorphsPatches(t *testing.T) {
	srv := newServer(t, "/", &counter{})

	_, body := get(t, srv.URL+"/liquid/runtime.js")

	if !strings.Contains(body, "Idiomorph.morph") {
		t.Error("runtime.js applyPatch must reconcile patches via Idiomorph.morph (#106)")
	}
}
