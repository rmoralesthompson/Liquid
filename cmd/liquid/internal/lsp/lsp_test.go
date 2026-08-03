package lsp_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rmoralesthompson/liquid/cmd/liquid/internal/lsp"
)

// client drives an in-process server over pipes, hiding the JSON-RPC
// framing from tests. The server loop is serial, so after any didOpen or
// didChange the next server-to-client message is its diagnostics publish.
type client struct {
	t     *testing.T
	w     io.Writer
	r     *bufio.Reader
	id    int
	diags map[string][]lsp.Diagnostic
}

// newClient starts a server session and completes the initialize handshake.
func newClient(t *testing.T) *client {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	c := &client{t: t, w: inW, r: bufio.NewReader(outR), diags: make(map[string][]lsp.Diagnostic)}
	done := make(chan error, 1)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	go func() { done <- lsp.Serve(context.Background(), inR, outW, logger) }()
	t.Cleanup(func() {
		c.notify("exit", nil)
		if err := <-done; err != nil {
			t.Errorf("Serve: %v", err)
		}
	})
	c.request("initialize", map[string]any{})
	c.notify("initialized", map[string]any{})
	return c
}

// send writes one framed message.
func (c *client) send(msg map[string]any) {
	c.t.Helper()
	body, err := json.Marshal(msg)
	if err != nil {
		c.t.Fatalf("marshaling message: %v", err)
	}
	if _, err := fmt.Fprintf(c.w, "Content-Length: %d\r\n\r\n%s", len(body), body); err != nil {
		c.t.Fatalf("writing message: %v", err)
	}
}

// notify sends a notification.
func (c *client) notify(method string, params any) {
	c.t.Helper()
	msg := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		msg["params"] = params
	}
	c.send(msg)
}

// inbound is one message from the server.
type inbound struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}

// read reads one framed server message.
func (c *client) read() inbound {
	c.t.Helper()
	length := 0
	for {
		line, err := c.r.ReadString('\n')
		if err != nil {
			c.t.Fatalf("reading server message: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if v, ok := strings.CutPrefix(line, "Content-Length:"); ok {
			if _, err := fmt.Sscan(strings.TrimSpace(v), &length); err != nil {
				c.t.Fatalf("parsing Content-Length: %v", err)
			}
		}
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(c.r, body); err != nil {
		c.t.Fatalf("reading body: %v", err)
	}
	var m inbound
	if err := json.Unmarshal(body, &m); err != nil {
		c.t.Fatalf("decoding server message: %v", err)
	}
	return m
}

// record stores a publishDiagnostics notification.
func (c *client) record(m inbound) {
	if m.Method != "textDocument/publishDiagnostics" {
		return
	}
	var p struct {
		URI         string           `json:"uri"`
		Diagnostics []lsp.Diagnostic `json:"diagnostics"`
	}
	if err := json.Unmarshal(m.Params, &p); err != nil {
		c.t.Fatalf("decoding publishDiagnostics: %v", err)
	}
	c.diags[p.URI] = p.Diagnostics
}

// request sends a request and returns its result, recording notifications
// that arrive first.
func (c *client) request(method string, params any) json.RawMessage {
	c.t.Helper()
	c.id++
	c.send(map[string]any{"jsonrpc": "2.0", "id": c.id, "method": method, "params": params})
	for {
		m := c.read()
		if m.Method != "" {
			c.record(m)
			continue
		}
		if len(m.Error) > 0 {
			c.t.Fatalf("%s error: %s", method, m.Error)
		}
		return m.Result
	}
}

// open opens a buffer and returns the diagnostics the server publishes.
func (c *client) open(uri, text string) []lsp.Diagnostic {
	c.t.Helper()
	c.notify("textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{"uri": uri, "languageId": "lsx", "version": 1, "text": text},
	})
	c.record(c.read())
	return c.diags[uri]
}

// change replaces a buffer (full sync) and returns the fresh diagnostics.
func (c *client) change(uri, text string) []lsp.Diagnostic {
	c.t.Helper()
	c.notify("textDocument/didChange", map[string]any{
		"textDocument":   map[string]any{"uri": uri, "version": 2},
		"contentChanges": []map[string]any{{"text": text}},
	})
	c.record(c.read())
	return c.diags[uri]
}

// fixture returns the app fixture's absolute path, the dashboard.lsx URI,
// and its on-disk text.
func fixture(t *testing.T) (dir, uri, text string) {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("testdata", "app"))
	if err != nil {
		t.Fatalf("resolving fixture: %v", err)
	}
	path := filepath.Join(dir, "dashboard.lsx")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	return dir, "file://" + path, string(src)
}

// pos computes the LSP position of the needle's first occurrence in text,
// shifted delta bytes into it. The fixtures are ASCII, so byte offsets and
// UTF-16 characters coincide.
func pos(t *testing.T, text, needle string, delta int) map[string]any {
	t.Helper()
	i := strings.Index(text, needle)
	if i < 0 {
		t.Fatalf("needle %q not in text", needle)
	}
	i += delta
	line := strings.Count(text[:i], "\n")
	col := i - (strings.LastIndex(text[:i], "\n") + 1)
	return map[string]any{"line": line, "character": col}
}

// positionParams builds the common hover/definition/completion parameters.
func positionParams(uri string, p map[string]any) map[string]any {
	return map[string]any{"textDocument": map[string]any{"uri": uri}, "position": p}
}

func TestInitializeDeclaresCapabilities(t *testing.T) {
	c := newClient(t)

	res := c.request("initialize", map[string]any{})

	var got struct {
		Capabilities map[string]json.RawMessage `json:"capabilities"`
	}
	if err := json.Unmarshal(res, &got); err != nil {
		t.Fatalf("decoding initialize result: %v", err)
	}
	for _, cap := range []string{"textDocumentSync", "hoverProvider", "definitionProvider", "documentHighlightProvider", "completionProvider"} {
		if _, ok := got.Capabilities[cap]; !ok {
			t.Errorf("capabilities missing %s: %s", cap, res)
		}
	}
}

func TestPublishesVetDiagnosticsOnOpenAndClearsOnFix(t *testing.T) {
	c := newClient(t)
	_, uri, text := fixture(t)
	broken := strings.Replace(text, "{{ Title }}", "{{ Titel }}", 1)

	diags := c.open(uri, broken)

	if len(diags) != 1 || diags[0].Code != "LSX004" {
		t.Fatalf("diagnostics = %+v, want exactly one LSX004", diags)
	}
	if !strings.Contains(diags[0].Message, "did you mean Title?") {
		t.Errorf("message %q should carry the suggestion", diags[0].Message)
	}
	wantPos := pos(t, broken, "Titel", 0)
	if diags[0].Range.Start.Line != wantPos["line"] || diags[0].Range.Start.Character != wantPos["character"] {
		t.Errorf("diagnostic at %+v, want %v", diags[0].Range.Start, wantPos)
	}

	if diags := c.change(uri, text); len(diags) != 0 {
		t.Errorf("diagnostics after fix = %+v, want none", diags)
	}
}

func TestHoverOnFieldShowsTypeAndDoc(t *testing.T) {
	c := newClient(t)
	_, uri, text := fixture(t)
	c.open(uri, text)

	res := c.request("textDocument/hover", positionParams(uri, pos(t, text, "{{ Title }}", 4)))

	var h lsp.Hover
	if err := json.Unmarshal(res, &h); err != nil {
		t.Fatalf("decoding hover: %v (%s)", err, res)
	}
	for _, want := range []string{"Title string", "Title is the dashboard heading.", "field of `Dashboard`"} {
		if !strings.Contains(h.Contents.Value, want) {
			t.Errorf("hover %q missing %q", h.Contents.Value, want)
		}
	}
}

func TestHoverOnDirectiveNameShowsDocs(t *testing.T) {
	c := newClient(t)
	_, uri, text := fixture(t)
	c.open(uri, text)

	res := c.request("textDocument/hover", positionParams(uri, pos(t, text, "*goIf", 1)))

	var h lsp.Hover
	if err := json.Unmarshal(res, &h); err != nil {
		t.Fatalf("decoding hover: %v (%s)", err, res)
	}
	if !strings.Contains(h.Contents.Value, "Renders the element only when") {
		t.Errorf("hover %q is not the *goIf documentation", h.Contents.Value)
	}
}

func TestHoverOnLoopVarNamesItsGoFor(t *testing.T) {
	c := newClient(t)
	_, uri, text := fixture(t)
	c.open(uri, text)

	res := c.request("textDocument/hover", positionParams(uri, pos(t, text, "{{ $log }}", 4)))

	var h lsp.Hover
	if err := json.Unmarshal(res, &h); err != nil {
		t.Fatalf("decoding hover: %v (%s)", err, res)
	}
	for _, want := range []string{"loop variable", "let log of Logs"} {
		if !strings.Contains(h.Contents.Value, want) {
			t.Errorf("hover %q missing %q", h.Contents.Value, want)
		}
	}
}

func TestDefinitionOfFieldPointsIntoPairedGo(t *testing.T) {
	c := newClient(t)
	dir, uri, text := fixture(t)
	c.open(uri, text)

	res := c.request("textDocument/definition", positionParams(uri, pos(t, text, "{{ Title }}", 4)))

	var locs []lsp.Location
	if err := json.Unmarshal(res, &locs); err != nil {
		t.Fatalf("decoding definition: %v (%s)", err, res)
	}
	if len(locs) != 1 {
		t.Fatalf("locations = %+v, want exactly one", locs)
	}
	if want := "file://" + filepath.Join(dir, "dashboard.go"); locs[0].URI != want {
		t.Fatalf("definition URI = %q, want %q", locs[0].URI, want)
	}
	goSrc, err := os.ReadFile(filepath.Join(dir, "dashboard.go"))
	if err != nil {
		t.Fatalf("reading dashboard.go: %v", err)
	}
	want := pos(t, string(goSrc), "Title   string", 0)
	if locs[0].Range.Start.Line != want["line"] || locs[0].Range.Start.Character != want["character"] {
		t.Errorf("definition at %+v, want %v", locs[0].Range.Start, want)
	}
}

func TestDefinitionOfChildSelectorPointsAtComponentStruct(t *testing.T) {
	c := newClient(t)
	dir, uri, text := fixture(t)
	c.open(uri, text)

	res := c.request("textDocument/definition", positionParams(uri, pos(t, text, "app-stat-card", 3)))

	var locs []lsp.Location
	if err := json.Unmarshal(res, &locs); err != nil {
		t.Fatalf("decoding definition: %v (%s)", err, res)
	}
	if len(locs) != 1 {
		t.Fatalf("locations = %+v, want exactly one", locs)
	}
	if want := "file://" + filepath.Join(dir, "stat_card.go"); locs[0].URI != want {
		t.Fatalf("definition URI = %q, want %q", locs[0].URI, want)
	}
	goSrc, err := os.ReadFile(filepath.Join(dir, "stat_card.go"))
	if err != nil {
		t.Fatalf("reading stat_card.go: %v", err)
	}
	want := pos(t, string(goSrc), "StatCard struct", 0)
	if locs[0].Range.Start.Line != want["line"] || locs[0].Range.Start.Character != want["character"] {
		t.Errorf("definition at %+v, want %v", locs[0].Range.Start, want)
	}
}

// completionLabels runs a completion request and returns the item labels.
func completionLabels(t *testing.T, c *client, uri string, p map[string]any) ([]string, []lsp.CompletionItem) {
	t.Helper()
	res := c.request("textDocument/completion", positionParams(uri, p))
	var items []lsp.CompletionItem
	if err := json.Unmarshal(res, &items); err != nil {
		t.Fatalf("decoding completion: %v (%s)", err, res)
	}
	labels := make([]string, len(items))
	for i, it := range items {
		labels[i] = it.Label
	}
	return labels, items
}

func TestCompletionInsideInterpolationOffersMembersAndLoopVars(t *testing.T) {
	c := newClient(t)
	_, uri, text := fixture(t)
	c.open(uri, text)

	labels, _ := completionLabels(t, c, uri, pos(t, text, "{{ Title }}", 5))

	for _, want := range []string{"Title", "Logs", "IsAdmin", "Refresh", "$log"} {
		if !slicesContains(labels, want) {
			t.Errorf("completion labels %v missing %q", labels, want)
		}
	}
}

func TestCompletionInClickOffersOnlyDispatchableHandlers(t *testing.T) {
	c := newClient(t)
	_, uri, text := fixture(t)
	c.open(uri, text)

	labels, _ := completionLabels(t, c, uri, pos(t, text, `(click)="Refresh"`, 12))

	if len(labels) != 1 || labels[0] != "Refresh" {
		t.Errorf("completion labels = %v, want exactly [Refresh] — Selector has a result and is not dispatchable", labels)
	}
}

func TestCompletionAtAttributeNameOffersDirectivesAndChildInputs(t *testing.T) {
	c := newClient(t)
	_, uri, text := fixture(t)
	c.open(uri, text)

	labels, _ := completionLabels(t, c, uri, pos(t, text, `(click)="Refresh"`, 0))

	for _, want := range []string{"*goIf", "*goFor", "(click)", "(submit)", "[hydroId]"} {
		if !slicesContains(labels, want) {
			t.Errorf("attribute completion %v missing %q", labels, want)
		}
	}

	labels, _ = completionLabels(t, c, uri, pos(t, text, `[label]="Title"`, 0))
	if !slicesContains(labels, "[label]") {
		t.Errorf("child-element attribute completion %v missing [label]", labels)
	}
}

func TestCompletionOnTagNameOffersSelectors(t *testing.T) {
	c := newClient(t)
	_, uri, text := fixture(t)
	c.open(uri, text)

	labels, _ := completionLabels(t, c, uri, pos(t, text, "app-stat-card", 4))

	for _, want := range []string{"app-dashboard", "app-stat-card"} {
		if !slicesContains(labels, want) {
			t.Errorf("tag completion %v missing %q", labels, want)
		}
	}
}

func TestDocumentHighlightMarksAllFieldOccurrences(t *testing.T) {
	c := newClient(t)
	_, uri, text := fixture(t)
	c.open(uri, text)

	res := c.request("textDocument/documentHighlight", positionParams(uri, pos(t, text, "{{ Title }}", 4)))

	var hs []lsp.DocumentHighlight
	if err := json.Unmarshal(res, &hs); err != nil {
		t.Fatalf("decoding highlights: %v (%s)", err, res)
	}
	// {{ Title }} and [label]="Title".
	if len(hs) != 2 {
		t.Errorf("highlights = %+v, want the interpolation and the [input] expression", hs)
	}
}

func TestMissingPairedSourceYieldsLSX002(t *testing.T) {
	c := newClient(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/lonely\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatalf("writing go.mod: %v", err)
	}
	uri := "file://" + filepath.Join(dir, "widget.lsx")

	diags := c.open(uri, "<div>{{ X }}</div>\n")

	if len(diags) != 1 || diags[0].Code != "LSX002" {
		t.Errorf("diagnostics = %+v, want exactly one LSX002", diags)
	}
}

func TestLoopVarNamedTResolvesOutsideTheLetKeyword(t *testing.T) {
	c := newClient(t)
	_, uri, text := fixture(t)
	edited := strings.ReplaceAll(text, "let log of Logs", "let t of Logs")
	edited = strings.ReplaceAll(edited, "{{ $log }}", "{{ $t }}")
	if diags := c.open(uri, edited); len(diags) != 0 {
		t.Fatalf("edited fixture has diagnostics: %+v", diags)
	}

	res := c.request("textDocument/definition", positionParams(uri, pos(t, edited, "{{ $t }}", 4)))

	var locs []lsp.Location
	if err := json.Unmarshal(res, &locs); err != nil {
		t.Fatalf("decoding definition: %v (%s)", err, res)
	}
	if len(locs) != 1 || locs[0].URI != uri {
		t.Fatalf("locations = %+v, want the declaring *goFor in this document", locs)
	}
	// The variable t is a substring of the let keyword; the definition must
	// land on the variable itself, not inside "let".
	want := pos(t, edited, "t of Logs", 0)
	if locs[0].Range.Start.Line != want["line"] || locs[0].Range.Start.Character != want["character"] {
		t.Errorf("definition at %+v, want %v", locs[0].Range.Start, want)
	}

	res = c.request("textDocument/documentHighlight", positionParams(uri, pos(t, edited, "{{ $t }}", 4)))
	var hs []lsp.DocumentHighlight
	if err := json.Unmarshal(res, &hs); err != nil {
		t.Fatalf("decoding highlights: %v (%s)", err, res)
	}
	if len(hs) != 2 {
		t.Errorf("highlights = %+v, want the let declaration and the interpolation", hs)
	}
}

func TestDefinitionOfHandlerPointsAtMethod(t *testing.T) {
	c := newClient(t)
	dir, uri, text := fixture(t)
	c.open(uri, text)

	res := c.request("textDocument/definition", positionParams(uri, pos(t, text, `(click)="Refresh"`, 10)))

	var locs []lsp.Location
	if err := json.Unmarshal(res, &locs); err != nil {
		t.Fatalf("decoding definition: %v (%s)", err, res)
	}
	if len(locs) != 1 {
		t.Fatalf("locations = %+v, want exactly one", locs)
	}
	if want := "file://" + filepath.Join(dir, "dashboard.go"); locs[0].URI != want {
		t.Fatalf("definition URI = %q, want %q", locs[0].URI, want)
	}
	goSrc, err := os.ReadFile(filepath.Join(dir, "dashboard.go"))
	if err != nil {
		t.Fatalf("reading dashboard.go: %v", err)
	}
	want := pos(t, string(goSrc), "Refresh() {", 0)
	if locs[0].Range.Start.Line != want["line"] || locs[0].Range.Start.Character != want["character"] {
		t.Errorf("definition at %+v, want the method declaration %v", locs[0].Range.Start, want)
	}
}

func TestHoverOnEventBindingAndChildSelector(t *testing.T) {
	c := newClient(t)
	_, uri, text := fixture(t)
	c.open(uri, text)

	res := c.request("textDocument/hover", positionParams(uri, pos(t, text, "(click)", 1)))
	var h lsp.Hover
	if err := json.Unmarshal(res, &h); err != nil {
		t.Fatalf("decoding hover: %v (%s)", err, res)
	}
	if !strings.Contains(h.Contents.Value, "Binds a click") {
		t.Errorf("(click) hover %q is not the binding documentation", h.Contents.Value)
	}

	res = c.request("textDocument/hover", positionParams(uri, pos(t, text, "app-stat-card", 3)))
	if err := json.Unmarshal(res, &h); err != nil {
		t.Fatalf("decoding hover: %v (%s)", err, res)
	}
	for _, want := range []string{"StatCard", "component", "fixture child component"} {
		if !strings.Contains(h.Contents.Value, want) {
			t.Errorf("selector hover %q missing %q", h.Contents.Value, want)
		}
	}
}

func TestDiagnosticRangeSpansTheOffendingToken(t *testing.T) {
	c := newClient(t)
	_, uri, text := fixture(t)
	broken := strings.Replace(text, "{{ Title }}", "{{ Titel }}", 1)

	diags := c.open(uri, broken)

	if len(diags) != 1 {
		t.Fatalf("diagnostics = %+v, want one", diags)
	}
	width := diags[0].Range.End.Character - diags[0].Range.Start.Character
	if width != len("Titel") {
		t.Errorf("diagnostic spans %d characters, want %d covering the identifier", width, len("Titel"))
	}
}

func TestShutdownRepliesAndDidCloseClearsDiagnostics(t *testing.T) {
	c := newClient(t)
	_, uri, text := fixture(t)
	broken := strings.Replace(text, "{{ Title }}", "{{ Titel }}", 1)
	if diags := c.open(uri, broken); len(diags) != 1 {
		t.Fatalf("expected one diagnostic to start from, got %+v", diags)
	}

	c.notify("textDocument/didClose", map[string]any{
		"textDocument": map[string]any{"uri": uri},
	})
	c.record(c.read())
	if diags := c.diags[uri]; len(diags) != 0 {
		t.Errorf("diagnostics after didClose = %+v, want cleared", diags)
	}

	if res := c.request("shutdown", nil); string(res) != "null" {
		t.Errorf("shutdown result = %s, want null", res)
	}
}

// slicesContains reports whether list has the exact string.
func slicesContains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
