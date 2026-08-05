package liquidtest

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/net/html"
)

// Golden-snapshot assertions (D27): render a component (or capture a fired
// patch) and compare its [hydroId] subtree against a committed file, failing
// on any HTML change. This is the mechanical regression check a non-human
// author needs — "did the rendered HTML change?" — and it is only meaningful
// because deterministic render mode (D28, core.WithDeterminism) pins the
// tokens, clock, and ordering that would otherwise diff on every run.
//
// A snapshot keys on the [hydroId] subtree boundary (D14), so a full render
// and a patch of the same component serialize through the same path and
// assert against the same golden file.

// updateSnapshots is the explicit, local-only regeneration switch: `go test
// -update` rewrites (or creates) the committed golden files. There is no
// implicit path to it — a snapshot never regenerates in an ordinary run, so
// CI can never silently bless a drifted render (D27).
var updateSnapshots = flag.Bool("update", false,
	"regenerate liquidtest golden snapshots instead of asserting against them")

// snapshotDir is where golden files live, under the consuming test package's
// own testdata/ (which the go tool never compiles).
const snapshotDir = "testdata/snapshots"

// snapshotCode is the stable diagnostic code a mismatch reports, so an agent
// keys off it like a build finding (D13).
const snapshotCode = "SNAP001"

// snapshotDiagnostic mirrors the D13 diagnostic contract (the internal
// compiler.Diagnostic is unimportable from this module-level package, so the
// field vocabulary is reproduced here): a mismatch is emitted as this shape so
// a non-human author consumes it the same way it consumes a build diagnostic.
type snapshotDiagnostic struct {
	File       string `json:"file"`
	Line       int    `json:"line"`
	Col        int    `json:"col"`
	Severity   string `json:"severity"`
	Code       string `json:"code"`
	Message    string `json:"message"`
	Suggestion string `json:"suggestion"`
}

// MatchSnapshot asserts the page's rendered [hydroId] subtree equals the
// committed snapshot named name, failing on any difference. With `go test
// -update` it (re)writes the golden file instead. A non-interactive page
// (no [hydroId]) snapshots its <body> content.
func (p *Page) MatchSnapshot(name string) {
	p.h.t.Helper()
	matchSnapshot(p.h.t, name, snapshotContent(p.doc))
}

// MatchSnapshot asserts the fired patch's [hydroId] subtree equals the
// committed snapshot named name. Because it keys on the same subtree boundary
// as Page.MatchSnapshot, a component's initial render and its post-event patch
// can assert against the same golden file (D14).
func (p *Patch) MatchSnapshot(name string) {
	p.h.t.Helper()
	if p.Code != 200 {
		p.h.t.Fatalf("liquidtest: MatchSnapshot on a refused event (status %d); assert the render, not a 4xx", p.Code)
	}
	matchSnapshot(p.h.t, name, snapshotContent(p.doc))
}

// matchSnapshot is the shared compare-or-regenerate core: it resolves the
// golden path, and either writes it (under -update) or reads and compares it,
// reporting a D13-shaped diagnostic and a trimmed diff on mismatch.
func matchSnapshot(t testingTB, name, got string) {
	t.Helper()
	path, err := snapshotPath(name)
	if err != nil {
		t.Fatalf("liquidtest: %v", err)
	}

	if *updateSnapshots {
		if werr := writeSnapshot(path, got); werr != nil {
			t.Fatalf("liquidtest: writing snapshot %q: %v", name, werr)
		}
		t.Logf("liquidtest: wrote snapshot %s", path)
		return
	}

	data, err := os.ReadFile(path) //nolint:gosec // path is testdata/snapshots/<sanitized name>
	if err != nil {
		if os.IsNotExist(err) {
			t.Errorf("liquidtest: snapshot %q does not exist (%s); rerun with -update to create it after confirming the render is correct",
				name, path)
			return
		}
		t.Fatalf("liquidtest: reading snapshot %q: %v", name, err)
	}
	want := strings.TrimSuffix(string(data), "\n")
	if got == want {
		return
	}
	t.Errorf("liquidtest: snapshot %q does not match %s\n%s\n%s",
		name, path, mismatchDiagnostic(path, name, want, got), unifiedDiff(want, got))
}

// snapshotContent is the HTML a snapshot captures: the first [hydroId]
// subtree, serialized so a full render and a patch of the same component agree
// byte-for-byte; falling back to the <body> content for a non-interactive
// page. The doc is either a full parsed page (Page) or a parsed patch fragment
// (Patch) — html.Parse wraps both in html/head/body, so the same extraction
// serves both.
func snapshotContent(doc *html.Node) string {
	if node := firstHydroIDNode(doc); node != nil {
		return renderNode(node)
	}
	if body := elementNamed(doc, "body"); body != nil {
		return renderChildren(body)
	}
	return renderChildren(doc)
}

// firstHydroIDNode returns the first element carrying data-hydro-id on or
// under n, or nil — the node form of firstHydroID.
func firstHydroIDNode(n *html.Node) *html.Node {
	var found *html.Node
	walk(n, func(n *html.Node) bool {
		if _, ok := attr(n, "data-hydro-id"); ok {
			found = n
			return false
		}
		return true
	})
	return found
}

// elementNamed returns the first element node named tag on or under n, or nil.
func elementNamed(n *html.Node, tag string) *html.Node {
	var found *html.Node
	walk(n, func(n *html.Node) bool {
		if n.Type == html.ElementNode && n.Data == tag {
			found = n
			return false
		}
		return true
	})
	return found
}

// renderNode serializes a node and its subtree exactly as html.Render does —
// the same serialization for a Page's extracted subtree and a Patch's, so the
// two produce identical bytes for the same component.
func renderNode(n *html.Node) string {
	var b strings.Builder
	// html.Render only errors on a malformed tree, which a parsed node is not.
	_ = html.Render(&b, n)
	return b.String()
}

// renderChildren serializes each child of n in order — the <body> content of
// a non-interactive render.
func renderChildren(n *html.Node) string {
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		_ = html.Render(&b, c)
	}
	return b.String()
}

// snapshotPath resolves the golden file for name, rejecting a name that would
// escape the snapshot directory. Names are simple identifiers; a path
// separator or ".." is a test-authoring error, refused loudly.
func snapshotPath(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("MatchSnapshot: empty snapshot name")
	}
	if strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return "", fmt.Errorf("MatchSnapshot: snapshot name %q must be a simple identifier (no path separators or \"..\")", name)
	}
	return filepath.Join(snapshotDir, name+".snap.html"), nil
}

// writeSnapshot creates the snapshot directory if needed and writes content
// with a single trailing newline (so the golden file ends cleanly).
func writeSnapshot(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating snapshot directory: %w", err)
	}
	//nolint:gosec // world-readable golden fixture, not a secret
	if err := os.WriteFile(path, []byte(content+"\n"), 0o644); err != nil {
		return fmt.Errorf("writing snapshot file: %w", err)
	}
	return nil
}

// mismatchDiagnostic renders the D13-shaped finding for a mismatch as one JSON
// line an agent can parse, positioned at the first differing line/col.
func mismatchDiagnostic(path, name, want, got string) string {
	line, col := firstDiff(want, got)
	d := snapshotDiagnostic{
		File:       path,
		Line:       line,
		Col:        col,
		Severity:   "error",
		Code:       snapshotCode,
		Message:    "rendered output does not match snapshot " + name,
		Suggestion: "rerun the test with -update to regenerate the snapshot after confirming the change is intended",
	}
	encoded, err := json.Marshal(d)
	if err != nil { // a fixed struct of strings/ints never fails to marshal
		return "diagnostic: " + d.Message
	}
	return "diagnostic: " + string(encoded)
}

// firstDiff returns the 1-based line and byte column of the first difference
// between want and got, or (0, 0) when they are equal. It powers the
// diagnostic's line/col so the finding points at the divergence.
func firstDiff(want, got string) (line, col int) {
	wl, gl := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < len(wl) || i < len(gl); i++ {
		var w, g string
		if i < len(wl) {
			w = wl[i]
		}
		if i < len(gl) {
			g = gl[i]
		}
		if w == g {
			continue
		}
		c := 0
		for c < len(w) && c < len(g) && w[c] == g[c] {
			c++
		}
		return i + 1, c + 1
	}
	return 0, 0
}

// diffContext is how many unchanged lines to show around the divergent region.
const diffContext = 3

// unifiedDiff renders a compact diff of want vs got: equal leading and
// trailing lines are trimmed to a few lines of context, and the divergent
// middle is shown with '-' (expected) and '+' (actual) markers.
func unifiedDiff(want, got string) string {
	wl, gl := strings.Split(want, "\n"), strings.Split(got, "\n")
	// Common prefix and suffix, so only the changed region is printed.
	p := 0
	for p < len(wl) && p < len(gl) && wl[p] == gl[p] {
		p++
	}
	s := 0
	for s < len(wl)-p && s < len(gl)-p && wl[len(wl)-1-s] == gl[len(gl)-1-s] {
		s++
	}
	var b strings.Builder
	b.WriteString("--- expected (committed snapshot)\n+++ actual (this run)\n")
	ctxStart := p - diffContext
	if ctxStart < 0 {
		ctxStart = 0
	}
	for i := ctxStart; i < p; i++ {
		b.WriteString("  " + wl[i] + "\n")
	}
	for i := p; i < len(wl)-s; i++ {
		b.WriteString("- " + wl[i] + "\n")
	}
	for i := p; i < len(gl)-s; i++ {
		b.WriteString("+ " + gl[i] + "\n")
	}
	ctxEnd := len(wl) - s + diffContext
	if ctxEnd > len(wl) {
		ctxEnd = len(wl)
	}
	for i := len(wl) - s; i < ctxEnd; i++ {
		b.WriteString("  " + wl[i] + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// testingTB is the slice of testing.TB matchSnapshot needs, kept local so the
// snapshot core is unit-testable against a fake recorder.
type testingTB interface {
	Helper()
	Logf(format string, args ...any)
	Errorf(format string, args ...any)
	Fatalf(format string, args ...any)
}
