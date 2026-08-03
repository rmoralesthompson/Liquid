package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/rmoralesthompson/liquid/cmd/liquid/internal/compiler"
)

// Server is one language-server session over a JSON-RPC connection. It is
// single-threaded: requests are handled in arrival order, so no state needs
// locking.
type Server struct {
	in     *bufio.Reader
	out    io.Writer
	logger *slog.Logger
	docs   map[string]*document   // open .lsx buffers, by URI
	facts  map[string]*factsEntry // package facts, by directory
}

// document is one open .lsx buffer with its latest analysis.
type document struct {
	uri        string
	path       string
	text       string
	lines      []int
	structName string
	sa         *compiler.SourceAnalysis
	facts      *compiler.Facts // nil when the package cannot load at all
	members    []compiler.Member
	selectors  []compiler.SelectorDecl
}

// factsEntry caches one directory's package facts against a fingerprint of
// its Go-side inputs.
type factsEntry struct {
	fingerprint string
	facts       *compiler.Facts
}

// Serve runs one session over r and w until the client sends exit or closes
// the stream. Protocol errors on a single message end the session with an
// error; a clean EOF is a normal disconnect.
func Serve(ctx context.Context, r io.Reader, w io.Writer, logger *slog.Logger) error {
	s := &Server{
		in:     bufio.NewReader(r),
		out:    w,
		logger: logger,
		docs:   make(map[string]*document),
		facts:  make(map[string]*factsEntry),
	}
	for {
		req, err := readMessage(s.in)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if req.Method == "exit" {
			return nil
		}
		if err := s.dispatch(ctx, req); err != nil {
			return err
		}
	}
}

// dispatch routes one message to its handler. Unknown notifications are
// ignored per the protocol; unknown requests get a MethodNotFound reply.
func (s *Server) dispatch(ctx context.Context, req *request) error {
	switch req.Method {
	case "initialize":
		return s.reply(req.ID, initializeResult())
	case "initialized", "textDocument/didSave", "$/cancelRequest",
		"workspace/didChangeConfiguration", "$/setTrace":
		return nil
	case "shutdown":
		return s.reply(req.ID, nil)
	case "textDocument/didOpen":
		return s.handleDidOpen(ctx, req)
	case "textDocument/didChange":
		return s.handleDidChange(ctx, req)
	case "textDocument/didClose":
		return s.handleDidClose(req)
	case "textDocument/hover":
		return s.handlePosition(req, func(d *document, off int) any { return d.hover(off) })
	case "textDocument/completion":
		return s.handlePosition(req, func(d *document, off int) any { return d.completions(off) })
	case "textDocument/definition":
		return s.handlePosition(req, func(d *document, off int) any { return d.definition(off) })
	case "textDocument/documentHighlight":
		return s.handlePosition(req, func(d *document, off int) any { return d.highlights(off) })
	default:
		if len(req.ID) > 0 {
			return s.replyError(req.ID, codeMethodNotFound, "method not supported: "+req.Method)
		}
		return nil
	}
}

// initializeResult declares the server's capabilities.
func initializeResult() any {
	return map[string]any{
		"capabilities": map[string]any{
			"textDocumentSync":          map[string]any{"openClose": true, "change": 1},
			"hoverProvider":             true,
			"definitionProvider":        true,
			"documentHighlightProvider": true,
			"completionProvider": map[string]any{
				"triggerCharacters": []string{"{", `"`, "'", "<", "*", "(", "[", "$", "."},
			},
		},
		"serverInfo": map[string]any{"name": "liquid lsp"},
	}
}

// handleDidOpen registers a buffer and publishes its diagnostics.
func (s *Server) handleDidOpen(ctx context.Context, req *request) error {
	var p didOpenParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		s.logger.Warn("bad didOpen params", "err", err)
		return nil
	}
	path, err := uriToPath(p.TextDocument.URI)
	if err != nil {
		s.logger.Warn("unusable document URI", "uri", p.TextDocument.URI, "err", err)
		return nil
	}
	doc := &document{uri: p.TextDocument.URI, path: path, text: p.TextDocument.Text}
	s.docs[p.TextDocument.URI] = doc
	s.analyze(ctx, doc)
	return s.publishDiagnostics(doc)
}

// handleDidChange replaces a buffer (full sync) and republishes.
func (s *Server) handleDidChange(ctx context.Context, req *request) error {
	var p didChangeParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		s.logger.Warn("bad didChange params", "err", err)
		return nil
	}
	doc, ok := s.docs[p.TextDocument.URI]
	if !ok || len(p.ContentChanges) == 0 {
		return nil
	}
	doc.text = p.ContentChanges[len(p.ContentChanges)-1].Text
	s.analyze(ctx, doc)
	return s.publishDiagnostics(doc)
}

// handleDidClose drops a buffer and clears its diagnostics.
func (s *Server) handleDidClose(req *request) error {
	var p didCloseParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return nil
	}
	if _, ok := s.docs[p.TextDocument.URI]; !ok {
		return nil
	}
	delete(s.docs, p.TextDocument.URI)
	return s.notify("textDocument/publishDiagnostics",
		publishDiagnosticsParams{URI: p.TextDocument.URI, Diagnostics: []Diagnostic{}})
}

// handlePosition runs one position-based feature against an open buffer.
func (s *Server) handlePosition(req *request, feature func(*document, int) any) error {
	var p positionParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return s.replyError(req.ID, codeInvalidParams, err.Error())
	}
	doc, ok := s.docs[p.TextDocument.URI]
	if !ok {
		return s.reply(req.ID, nil)
	}
	return s.reply(req.ID, feature(doc, doc.offsetAt(p.Position)))
}

// analyze refreshes a buffer's source analysis and package facts.
func (s *Server) analyze(ctx context.Context, doc *document) {
	doc.lines = lineStarts(doc.text)
	doc.structName = compiler.PairedStructName(doc.path)
	doc.sa = compiler.AnalyzeSource(doc.path, []byte(doc.text))
	doc.facts = s.loadFacts(ctx, filepath.Dir(doc.path))
	doc.members, doc.selectors = nil, nil
	if doc.facts != nil {
		doc.members = doc.facts.Component(doc.structName)
		doc.selectors = doc.facts.SelectorDecls()
	}
}

// loadFacts returns the directory's package facts, reloading only when a
// .go source, go.mod, or go.sum changed — the keystroke path stays scan+vet.
func (s *Server) loadFacts(ctx context.Context, dir string) *compiler.Facts {
	fp := fingerprint(dir)
	if e, ok := s.facts[dir]; ok && e.fingerprint == fp {
		return e.facts
	}
	facts, err := compiler.LoadFacts(ctx, dir)
	if err != nil {
		s.logger.Warn("loading package facts", "dir", dir, "err", err)
		facts = nil
	}
	s.facts[dir] = &factsEntry{fingerprint: fp, facts: facts}
	return facts
}

// fingerprint captures the Go-side inputs to a directory's package facts.
func fingerprint(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "unreadable: " + err.Error()
	}
	var b strings.Builder
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") && name != "go.mod" && name != "go.sum" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		fmt.Fprintf(&b, "%s:%d:%d;", name, info.Size(), info.ModTime().UnixNano())
	}
	return b.String()
}

// diagnostics runs the exact Build/Vet gating for one buffer: pairing
// (LSX002), then source-level findings, then the vet cross-check.
func (d *document) diagnostics() []compiler.Diagnostic {
	if diag := compiler.PairingDiagnostic(d.path); diag != nil {
		return []compiler.Diagnostic{*diag}
	}
	if d.facts == nil {
		return d.sa.Diagnostics
	}
	return d.facts.Vet(d.path, d.structName, d.sa)
}

// publishDiagnostics maps compiler diagnostics onto the buffer and sends
// them. Findings positioned in other files (LSX007 points into Go sources)
// surface at the top of the template with the original position in the
// message, since their own files are not liquid-managed documents.
func (s *Server) publishDiagnostics(doc *document) error {
	diags := doc.diagnostics()
	out := make([]Diagnostic, 0, len(diags))
	for _, d := range diags {
		msg := d.Message
		if d.Suggestion != "" {
			msg += " (suggestion: " + d.Suggestion + ")"
		}
		var rng Range
		if d.File == doc.path {
			pos := doc.positionAt(doc.offsetOf(d.Line, d.Col))
			rng = Range{Start: pos, End: pos}
		} else {
			msg = fmt.Sprintf("%s:%d:%d: %s", filepath.Base(d.File), d.Line, d.Col, msg)
		}
		sev := severityError
		if d.Severity == compiler.SeverityWarning {
			sev = severityWarning
		}
		out = append(out, Diagnostic{Range: rng, Severity: sev, Code: string(d.Code), Source: "liquid", Message: msg})
	}
	return s.notify("textDocument/publishDiagnostics",
		publishDiagnosticsParams{URI: doc.uri, Diagnostics: out})
}

// reply sends a successful response.
func (s *Server) reply(id json.RawMessage, result any) error {
	return writeMessage(s.out, response{JSONRPC: "2.0", ID: id, Result: result})
}

// replyError sends a failed response.
func (s *Server) replyError(id json.RawMessage, code int, msg string) error {
	return writeMessage(s.out, errorResponse{JSONRPC: "2.0", ID: id, Error: respError{Code: code, Message: msg}})
}

// notify sends a server-to-client notification.
func (s *Server) notify(method string, params any) error {
	return writeMessage(s.out, notification{JSONRPC: "2.0", Method: method, Params: params})
}

// uriToPath resolves a file:// document URI to a filesystem path.
func uriToPath(uri string) (string, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return "", fmt.Errorf("parsing document URI %q: %w", uri, err)
	}
	if u.Scheme != "file" {
		return "", fmt.Errorf("unsupported document URI scheme %q", u.Scheme)
	}
	return u.Path, nil
}

// pathToURI builds the file:// URI for a filesystem path.
func pathToURI(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return (&url.URL{Scheme: "file", Path: path}).String()
}
