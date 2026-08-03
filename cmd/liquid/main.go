// Command liquid is the Liquid CLI: the AOT template compiler (build/vet),
// the component scaffolder (generate), and the dev server (dev).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/rmoralesthompson/liquid/cmd/liquid/internal/compiler"
)

const usage = "usage: liquid <build|vet|generate|dev> [args]"

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "liquid:", err)
		os.Exit(1)
	}
}

// run executes one CLI invocation, writing diagnostics to stdout — as the
// D13 JSON array with --json, one human-readable line each otherwise. Any
// diagnostics make the invocation fail so build pipelines and agents get a
// non-zero exit.
func run(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	verb := args[0]
	if verb == "generate" {
		return runGenerate(args[1:], stdout)
	}
	if verb == "dev" {
		return runDev(args[1:], stdout)
	}

	dir, jsonOut, err := parseArgs(args[1:])
	if err != nil {
		return err
	}

	var diags []compiler.Diagnostic
	switch verb {
	case "build":
		diags, err = compiler.Build(context.Background(), dir)
	case "vet":
		diags, err = compiler.Vet(context.Background(), dir)
	default:
		return fmt.Errorf("unknown command %q (%s)", verb, usage)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", verb, err)
	}

	if err := printDiagnostics(stdout, diags, jsonOut); err != nil {
		return err
	}
	if len(diags) == 0 {
		return nil
	}
	return fmt.Errorf("%s: %d problem(s) found", verb, len(diags))
}

// parseArgs splits the arguments after the verb into the target directory
// (default ".") and the --json toggle, rejecting anything else.
func parseArgs(args []string) (dir string, jsonOut bool, err error) {
	for _, arg := range args {
		switch {
		case arg == "--json":
			jsonOut = true
		case strings.HasPrefix(arg, "-"):
			return "", false, fmt.Errorf("unknown flag %q (%s)", arg, usage)
		case dir != "":
			return "", false, fmt.Errorf("unexpected argument %q (%s)", arg, usage)
		default:
			dir = arg
		}
	}
	if dir == "" {
		dir = "."
	}
	return dir, jsonOut, nil
}

// printDiagnostics writes diagnostics to stdout — the D13 JSON array with
// --json (always an array, even when empty, so agents can parse the success
// path), one human-readable line each otherwise.
func printDiagnostics(stdout io.Writer, diags []compiler.Diagnostic, jsonOut bool) error {
	if !jsonOut {
		for _, d := range diags {
			line := fmt.Sprintf("%s:%d:%d: %s[%s]: %s", d.File, d.Line, d.Col, d.Severity, d.Code, d.Message)
			if d.Suggestion != "" {
				line += fmt.Sprintf(" (suggestion: %s)", d.Suggestion)
			}
			if _, err := fmt.Fprintln(stdout, line); err != nil {
				return fmt.Errorf("writing diagnostics: %w", err)
			}
		}
		return nil
	}
	if diags == nil {
		diags = []compiler.Diagnostic{}
	}
	if err := json.NewEncoder(stdout).Encode(diags); err != nil {
		return fmt.Errorf("encoding diagnostics: %w", err)
	}
	return nil
}
