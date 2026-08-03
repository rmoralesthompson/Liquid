//go:build !liquiddev

package liquid

import (
	"html/template"
	"net/http"
)

// This file is the production counterpart of dev_on.go: compile-time
// constants and inert stubs, so a binary built without the liquiddev tag
// carries no dev-mode behavior at all (#12's acceptance criterion). The
// devMode branches in shared code are dead and eliminated here.

// devMode reports at compile time whether this binary carries the dev
// surface.
const devMode = false

// devScriptPath exists for the shared routing branch; it is never served in
// a production build.
const devScriptPath = "/liquid/dev.js"

// devShellScript is empty in production: the shell references no dev script.
const devShellScript template.HTML = ""

// devState is empty in production; the App field costs nothing.
type devState struct{}

// initDev is inert in production; reading the dev field keeps the shared
// App layout identical across builds without tripping unused-checks.
func (a *App) initDev()                           { _ = a.dev }
func (a *App) devAttachStream(*sseStream)         {}
func (a *App) devDetachStream(*sseStream)         {}
func (a *App) serveDevScript(http.ResponseWriter) {}

// errorPageHTML is the framework error page: clean by design — the
// underlying error goes to the log, never to the client (D18; the dev build
// renders the detail instead).
const errorPageHTML = `<!doctype html>
<html><head><title>500 · Liquid</title></head>
<body><h1>Something went wrong</h1><p>The server hit an error handling this request.</p></body></html>
`

// errorPageBody ignores the diagnostic detail: the production error page is
// clean by design — the error goes to the log, never to the client (D18).
func errorPageBody(string) string { return errorPageHTML }
