// Command devapp is the dev-loop integration fixture: the smallest app with
// the ui/ split the scaffolder produces. Its ui package carries no committed
// *_gen.go — `liquid dev`'s first cycle must bootstrap it.
package main

import (
	"log/slog"
	"net/http"
	"os"

	liquid "github.com/rmoralesthompson/liquid/core"
	"github.com/rmoralesthompson/liquid/devapp/ui"
)

func main() {
	app := liquid.New()
	if err := app.Route("/", &ui.Hello{}); err != nil {
		slog.Error("routing", "err", err)
		os.Exit(1)
	}
	addr := os.Getenv("DEVAPP_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	if err := http.ListenAndServe(addr, app); err != nil {
		slog.Error("serving", "err", err)
		os.Exit(1)
	}
}
