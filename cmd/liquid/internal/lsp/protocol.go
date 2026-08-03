// Package lsp implements the liquid language server: a minimal Language
// Server Protocol implementation over a byte stream (liquid lsp serves
// stdio) giving editors live LSX diagnostics, hover, completion,
// go-to-definition, and document highlights for .lsx templates. All
// template analysis is delegated to the compiler package, so editor
// features and liquid vet share one grammar and cannot drift apart.
package lsp

// Position is an LSP text position: 0-based line and 0-based UTF-16 code
// unit offset within the line.
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// Range is a half-open [Start, End) span of document text.
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Location is a range inside a document, addressed by URI.
type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

// Diagnostic is one published finding, carrying the compiler's LSX code in
// Code and "liquid" in Source.
type Diagnostic struct {
	Range    Range  `json:"range"`
	Severity int    `json:"severity"`
	Code     string `json:"code,omitempty"`
	Source   string `json:"source"`
	Message  string `json:"message"`
}

// The LSP DiagnosticSeverity values the server publishes.
const (
	severityError   = 1
	severityWarning = 2
)

// MarkupContent is LSP markup, always markdown here.
type MarkupContent struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// Hover is the response to textDocument/hover.
type Hover struct {
	Contents MarkupContent `json:"contents"`
	Range    *Range        `json:"range,omitempty"`
}

// TextEdit is one replacement a completion item applies.
type TextEdit struct {
	Range   Range  `json:"range"`
	NewText string `json:"newText"`
}

// CompletionItem is one completion suggestion.
type CompletionItem struct {
	Label            string         `json:"label"`
	Kind             int            `json:"kind,omitempty"`
	Detail           string         `json:"detail,omitempty"`
	Documentation    *MarkupContent `json:"documentation,omitempty"`
	FilterText       string         `json:"filterText,omitempty"`
	SortText         string         `json:"sortText,omitempty"`
	InsertTextFormat int            `json:"insertTextFormat,omitempty"`
	TextEdit         *TextEdit      `json:"textEdit,omitempty"`
}

// The LSP CompletionItemKind values the server uses.
const (
	kindMethod   = 2
	kindField    = 5
	kindVariable = 6
	kindClass    = 7
	kindProperty = 10
	kindKeyword  = 14
	kindEvent    = 23
)

// snippetFormat marks a completion's newText as an LSP snippet.
const snippetFormat = 2

// DocumentHighlight is one occurrence highlighted for the symbol under the
// cursor.
type DocumentHighlight struct {
	Range Range `json:"range"`
	Kind  int   `json:"kind,omitempty"`
}

// The LSP DocumentHighlightKind values the server uses.
const (
	highlightRead  = 2
	highlightWrite = 3
)

// textDocumentIdentifier names a document in requests.
type textDocumentIdentifier struct {
	URI string `json:"uri"`
}

// textDocumentItem is the full document a didOpen carries.
type textDocumentItem struct {
	URI  string `json:"uri"`
	Text string `json:"text"`
}

// didOpenParams are the textDocument/didOpen parameters.
type didOpenParams struct {
	TextDocument textDocumentItem `json:"textDocument"`
}

// didChangeParams are the textDocument/didChange parameters; the server
// negotiates full-document sync, so only the last change's text matters.
type didChangeParams struct {
	TextDocument   textDocumentIdentifier `json:"textDocument"`
	ContentChanges []struct {
		Text string `json:"text"`
	} `json:"contentChanges"`
}

// didCloseParams are the textDocument/didClose parameters.
type didCloseParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
}

// positionParams is the shared shape of hover, completion, definition, and
// documentHighlight parameters: a document plus a position.
type positionParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

// publishDiagnosticsParams is the textDocument/publishDiagnostics payload.
type publishDiagnosticsParams struct {
	URI         string       `json:"uri"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}
