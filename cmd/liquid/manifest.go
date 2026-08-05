package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/rmoralesthompson/liquid/cmd/liquid/internal/compiler"
)

// runManifest implements `liquid manifest [dir] [--json]` (D26): it emits a
// stable graph of the compiled component app. Text output is the default,
// consistent with build/vet; --json is the agent contract. A directory that
// does not compile produces no manifest — the D13 diagnostics are emitted and
// the invocation fails, so an agent gets the same non-zero exit and structured
// errors as build/vet.
func runManifest(args []string, stdout io.Writer) error {
	dir, jsonOut, err := parseArgs(args)
	if err != nil {
		return err
	}

	graph, diags, err := compiler.Manifest(context.Background(), dir)
	if err != nil {
		return fmt.Errorf("manifest: %w", err)
	}
	if graph == nil {
		// The package does not compile: surface why, exactly as vet would, and
		// fail without emitting a manifest for a broken package.
		if err := printDiagnostics(stdout, diags, jsonOut); err != nil {
			return err
		}
		return fmt.Errorf("manifest: %d problem(s) found", errorCount(diags))
	}

	if jsonOut {
		if err := json.NewEncoder(stdout).Encode(graph); err != nil {
			return fmt.Errorf("encoding manifest: %w", err)
		}
		return nil
	}
	return printManifestText(stdout, graph)
}

// printManifestText renders the graph as human-readable text: one stanza per
// component, fields and actions aligned in columns.
func printManifestText(stdout io.Writer, graph *compiler.ManifestGraph) error {
	w := &checkedWriter{w: stdout}
	if len(graph.Components) == 0 {
		w.printf("no components (manifest %s)\n", graph.Version)
		return w.err
	}
	for i, c := range graph.Components {
		if i > 0 {
			w.printf("\n")
		}
		tags := ""
		if c.Interactive {
			tags += " [interactive]"
		}
		if c.Head {
			tags += " [head]"
		}
		w.printf("%s (%s) — %s%s\n", c.Selector, c.Struct, c.File, tags)

		tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
		cols := &checkedWriter{w: tw}
		if len(c.Fields) > 0 {
			cols.printf("  fields:\n")
			for _, f := range c.Fields {
				input := ""
				if f.Input {
					input = "\t[input]"
				}
				cols.printf("    %s\t%s%s\n", f.Name, f.Type, input)
			}
		}
		if len(c.Actions) > 0 {
			cols.printf("  actions:\n")
			for _, a := range c.Actions {
				cols.printf("    %s\t%s\t(%s)\n", a.Name, a.Signature, strings.Join(a.Events, ","))
			}
		}
		if cols.err != nil {
			return cols.err
		}
		if err := tw.Flush(); err != nil {
			return fmt.Errorf("writing manifest: %w", err)
		}
	}
	return w.err
}

// checkedWriter funnels formatted writes to an io.Writer, remembering the
// first error so callers check once at the end instead of at every write.
type checkedWriter struct {
	w   io.Writer
	err error
}

func (cw *checkedWriter) printf(format string, a ...any) {
	if cw.err != nil {
		return
	}
	if _, err := fmt.Fprintf(cw.w, format, a...); err != nil {
		cw.err = fmt.Errorf("writing manifest: %w", err)
	}
}
