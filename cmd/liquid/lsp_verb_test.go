package main

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"
)

// frame wraps one JSON-RPC body in LSP Content-Length framing.
func frame(body string) string {
	return fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), body)
}

func TestLSPVerbRejectsArguments(t *testing.T) {
	err := run([]string{"lsp", "extra"}, io.Discard)

	if err == nil || !strings.Contains(err.Error(), "lsp takes no arguments") {
		t.Errorf("run(lsp extra) = %v, want an argument error", err)
	}
}

func TestLSPVerbSpeaksProtocolOverStdio(t *testing.T) {
	in := frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`) +
		frame(`{"jsonrpc":"2.0","method":"exit"}`)
	var out bytes.Buffer

	if err := runLSP(nil, strings.NewReader(in), &out); err != nil {
		t.Fatalf("runLSP: %v", err)
	}

	if got := out.String(); !strings.Contains(got, `"hoverProvider":true`) {
		t.Errorf("initialize response %q should declare hoverProvider", got)
	}
}
