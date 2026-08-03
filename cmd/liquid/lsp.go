package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/rmoralesthompson/liquid/cmd/liquid/internal/lsp"
)

// runLSP serves the Language Server Protocol over stdin/stdout for editor
// integration (see editors/vscode-lsx). stdout carries only protocol
// frames; logs go to stderr.
func runLSP(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) != 0 {
		return fmt.Errorf("lsp takes no arguments (%s)", usage)
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := lsp.Serve(context.Background(), stdin, stdout, logger); err != nil {
		return fmt.Errorf("lsp: %w", err)
	}
	return nil
}
