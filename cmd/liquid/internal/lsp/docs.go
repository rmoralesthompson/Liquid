package lsp

// directiveDocs is the hover and completion documentation for each
// directive and binding, distilled from docs/template-syntax.md.
var directiveDocs = map[string]string{
	dirGoIf: "```html\n<div *goIf=\"IsAdmin\">Administrator</div>\n```\n\n" +
		"Renders the element only when the boolean field is truthy. " +
		"Compiles to `{{if .IsAdmin}}…{{end}}`.\n\n" +
		"Structural directive — at most one per element.",
	dirGoFor: "```html\n<li *goFor=\"let log of RecentLogs\">{{ $log }}</li>\n```\n\n" +
		"Repeats the element for each item of a slice field. Grammar: " +
		"`let <var> of <FieldPath>`; inside the loop the item is `{{ $log }}`. " +
		"Compiles to `{{range $log := .RecentLogs}}…{{end}}`.\n\n" +
		"Structural directive — at most one per element.",
	dirClick: "```html\n<button (click)=\"TriggerReboot\">Reset</button>\n```\n\n" +
		"Binds a click to an exported method on the component struct — " +
		"`func()` or `func(e liquid.Event)` (D11). The compiler verifies the " +
		"method, adds it to the action allowlist (D10), and the runtime posts " +
		"the event to the server. Needs a `[hydroId]` patch boundary in the file.",
	dirSubmit: "```html\n<form (submit)=\"Rename\">…</form>\n```\n\n" +
		"Binds a form submit to an exported method — `func()` or " +
		"`func(e liquid.Event)` (D11); form fields arrive as the event payload. " +
		"Every `<form>` also gets the hidden CSRF token input auto-injected " +
		"(the struct needs a `CSRFToken string` field). Needs a `[hydroId]` " +
		"patch boundary in the file.",
	dirHydroID: "```html\n<div [hydroId]>…</div>\n```\n\n" +
		"Marks the component's patchable root element — the boundary the " +
		"runtime swaps when events or server pushes patch the page. Compiles " +
		"to `data-hydro-id=\"{{ .HydroID }}\"`; the struct must carry " +
		"`HydroID string` (framework-populated). Required wherever event " +
		"bindings or server push are used.",
}
