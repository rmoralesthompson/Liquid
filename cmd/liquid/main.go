// Command liquid is the Liquid CLI: the AOT template compiler and, in later
// slices, the scaffolder and dev server.
package main

import (
	"fmt"
	"os"

	"github.com/rmoralesthompson/liquid/cmd/liquid/internal/compiler"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "liquid:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: liquid build [dir]")
	}
	switch args[0] {
	case "build":
		dir := "."
		if len(args) > 1 {
			dir = args[1]
		}
		if err := compiler.Build(dir); err != nil {
			return fmt.Errorf("build: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("unknown command %q (usage: liquid build [dir])", args[0])
	}
}
