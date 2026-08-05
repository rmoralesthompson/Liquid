package liquidtest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// These are white-box tests for the snapshot machinery's pure and file-level
// behavior — the determinism it depends on (D28) is exercised end-to-end by
// the liquiddev-tagged snapshot_test.go. Keeping the file logic here means the
// create/compare/mismatch contract is covered by an ordinary `go test ./...`.

// fakeTB records what matchSnapshot reports, standing in for *testing.T so the
// harness can assert on its own failure behavior. It satisfies testingTB.
type fakeTB struct {
	errors []string
	fatals []string
	logs   []string
}

func (f *fakeTB) Helper()                      {}
func (f *fakeTB) Logf(format string, a ...any) { f.logs = append(f.logs, fmt.Sprintf(format, a...)) }
func (f *fakeTB) Errorf(format string, a ...any) {
	f.errors = append(f.errors, fmt.Sprintf(format, a...))
}
func (f *fakeTB) Fatalf(format string, a ...any) {
	f.fatals = append(f.fatals, fmt.Sprintf(format, a...))
}

func parse(t *testing.T, s string) *html.Node {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(s))
	if err != nil {
		t.Fatalf("parsing %q: %v", s, err)
	}
	return doc
}

func TestFirstDiff(t *testing.T) {
	cases := []struct {
		name              string
		want, got         string
		wantLine, wantCol int
	}{
		{"equal", "a\nb", "a\nb", 0, 0},
		{"first line char 3", "abc\nd", "abx\nd", 1, 3},
		{"second line", "a\nbcd", "a\nbce", 2, 3},
		{"got longer", "a", "a\nb", 2, 1},
		{"want longer", "a\nb", "a", 2, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			line, col := firstDiff(c.want, c.got)
			if line != c.wantLine || col != c.wantCol {
				t.Errorf("firstDiff = (%d,%d), want (%d,%d)", line, col, c.wantLine, c.wantCol)
			}
		})
	}
}

func TestUnifiedDiffShowsOnlyTheChangedRegion(t *testing.T) {
	want := "l1\nl2\nl3\nl4\nl5"
	got := "l1\nl2\nXX\nl4\nl5"
	diff := unifiedDiff(want, got)
	if !strings.Contains(diff, "- l3") || !strings.Contains(diff, "+ XX") {
		t.Errorf("diff missing the changed lines:\n%s", diff)
	}
	if !strings.Contains(diff, "  l2") || !strings.Contains(diff, "  l4") {
		t.Errorf("diff missing surrounding context:\n%s", diff)
	}
}

func TestSnapshotPathRejectsUnsafeNames(t *testing.T) {
	for _, bad := range []string{"", "a/b", `a\b`, "../escape", "ok/../bad"} {
		if _, err := snapshotPath(bad); err == nil {
			t.Errorf("snapshotPath(%q) = nil error, want rejection", bad)
		}
	}
	got, err := snapshotPath("counter")
	if err != nil {
		t.Fatalf("snapshotPath(counter): %v", err)
	}
	if want := filepath.Join(snapshotDir, "counter.snap.html"); got != want {
		t.Errorf("snapshotPath(counter) = %q, want %q", got, want)
	}
}

// TestSnapshotContentSharesTheHydroBoundary is the D14 symmetry the acceptance
// criteria hinge on: a full page and a bare patch fragment of the same
// component serialize to identical snapshot content, so both assert against
// one golden file.
func TestSnapshotContentSharesTheHydroBoundary(t *testing.T) {
	subtree := `<div data-hydro-id="tok"><span id="n">7</span></div>`
	page := `<!doctype html><html><head><title>x</title></head><body>` + subtree + `</body></html>`
	fromPage := snapshotContent(parse(t, page))
	fromPatch := snapshotContent(parse(t, subtree)) // a Fire patch is exactly the fragment
	if fromPage != fromPatch {
		t.Errorf("page and patch snapshot content differ:\n page:  %q\n patch: %q", fromPage, fromPatch)
	}
	if !strings.Contains(fromPage, `data-hydro-id="tok"`) {
		t.Errorf("snapshot content lost the hydro boundary: %q", fromPage)
	}
}

// TestSnapshotContentFallsBackToBody covers a non-interactive render: with no
// [hydroId], the snapshot is the <body> content.
func TestSnapshotContentFallsBackToBody(t *testing.T) {
	page := `<!doctype html><html><head><title>x</title></head><body><p>hello</p></body></html>`
	got := snapshotContent(parse(t, page))
	if got != "<p>hello</p>" {
		t.Errorf("fallback snapshot = %q, want <p>hello</p>", got)
	}
}

// TestMatchSnapshotLifecycle drives the file contract through a temp dir: a
// missing snapshot fails (and is NOT silently created), -update creates it, a
// matching render passes, and a drifted render fails with the SNAP001
// diagnostic. os.Chdir keeps the fixed snapshotDir under a throwaway root.
func TestMatchSnapshotLifecycle(t *testing.T) {
	dir := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	path := filepath.Join(dir, snapshotDir, "widget.snap.html")

	// 1. Missing snapshot, no -update: fails, and creates nothing (no CI bless).
	*updateSnapshots = false
	tb := &fakeTB{}
	matchSnapshot(tb, "widget", "<div>1</div>")
	if len(tb.errors) != 1 || !strings.Contains(tb.errors[0], "-update") {
		t.Errorf("missing snapshot did not fail with an -update hint: %v", tb.errors)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("a missing snapshot was silently created without -update")
	}

	// 2. -update: creates the golden, no failure.
	*updateSnapshots = true
	tb = &fakeTB{}
	matchSnapshot(tb, "widget", "<div>1</div>")
	*updateSnapshots = false
	if len(tb.errors) != 0 || len(tb.fatals) != 0 {
		t.Errorf("-update reported a failure: errors=%v fatals=%v", tb.errors, tb.fatals)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("-update did not create the golden file: %v", err)
	}

	// 3. Matching render: passes silently.
	tb = &fakeTB{}
	matchSnapshot(tb, "widget", "<div>1</div>")
	if len(tb.errors) != 0 {
		t.Errorf("matching render reported a failure: %v", tb.errors)
	}

	// 4. Drifted render: fails with the SNAP001 diagnostic and a diff.
	tb = &fakeTB{}
	matchSnapshot(tb, "widget", "<div>2</div>")
	if len(tb.errors) != 1 {
		t.Fatalf("drifted render errors = %v, want exactly one", tb.errors)
	}
	msg := tb.errors[0]
	for _, want := range []string{snapshotCode, `"severity":"error"`, "- <div>1</div>", "+ <div>2</div>"} {
		if !strings.Contains(msg, want) {
			t.Errorf("mismatch message missing %q:\n%s", want, msg)
		}
	}
}
