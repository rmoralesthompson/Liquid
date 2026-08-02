package liquid

import (
	"context"
	"net/http"
)

// Ctx is the per-request context handed to guards and lifecycle hooks. It
// embeds the request's context.Context (D18), so cancellation and deadlines
// flow into any work a component fans out, and exposes request accessors.
type Ctx struct {
	context.Context
	params map[string]string
	req    *http.Request
}

// Param returns the value bound to the named :param route segment, or ""
// when the route has no such segment.
func (c Ctx) Param(name string) string { return c.params[name] }

// Query returns the first value of the named URL query parameter.
func (c Ctx) Query(name string) string { return c.req.URL.Query().Get(name) }

// Header returns the named request header.
func (c Ctx) Header(name string) string { return c.req.Header.Get(name) }
