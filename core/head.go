package liquid

import "html/template"

// Head is a page's document head: the <title> plus any named meta tags
// (D22). Values pass through html/template's contextual escaping like all
// component state.
type Head struct {
	Title string
	Meta  []Meta
}

// Meta is one named <meta> tag, e.g. {Name: "description", Content: "…"}.
type Meta struct {
	Name    string
	Content string
}

// HeadProvider is implemented by components that control their document head
// (D22). Head runs on the fresh per-request instance after OnInit, so
// per-request state may inform the title. Components without it fall back to
// their selector as the title.
type HeadProvider interface {
	Head() Head
}

// shellHTML is the document shell wrapped around every page render: the
// component supplies the body, Head supplies title and meta. The shell is
// what makes a route a complete document without a separate layout system.
const shellHTML = `<!doctype html>
<html><head><meta charset="utf-8"><title>{{.Head.Title}}</title>{{range .Head.Meta}}<meta name="{{.Name}}" content="{{.Content}}">{{end}}</head>
<body>{{.Body}}</body></html>
`

// shellTmpl is parsed once at package load; requests only execute it.
var shellTmpl = template.Must(template.New("shell").Parse(shellHTML))

// shellData feeds one shell execution. Body is the already-escaped component
// render — trusted HTML by construction, not raw input.
type shellData struct {
	Head Head
	Body template.HTML
}
