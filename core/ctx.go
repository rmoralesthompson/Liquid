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
	// session is the liquid_session ID minted during this render, set when
	// the browser has no cookie yet for the request to carry.
	session string
}

// NewCtx assembles a Ctx from a request and bound route params. The
// framework builds its own Ctx per request; this constructor exists so test
// harnesses (liquidtest) can hand one to lifecycle hooks directly. req must
// be non-nil — liquidtest.Ctx supplies a default request for callers that
// have none.
func NewCtx(req *http.Request, params map[string]string) Ctx {
	return Ctx{Context: req.Context(), params: params, req: req}
}

// Param returns the value bound to the named :param route segment, or ""
// when the route has no such segment.
func (c Ctx) Param(name string) string { return c.params[name] }

// Query returns the first value of the named URL query parameter.
func (c Ctx) Query(name string) string { return c.req.URL.Query().Get(name) }

// Header returns the named request header.
func (c Ctx) Header(name string) string { return c.req.Header.Get(name) }

// Session returns the opaque liquid_session ID the request runs under (D15,
// D18), or "" for a session-less request. On the very first render of an
// interactive page the ID was minted mid-request, so it comes from the
// framework rather than the not-yet-set cookie.
func (c Ctx) Session() string {
	if c.session != "" {
		return c.session
	}
	if ck, err := c.req.Cookie(sessionCookieName); err == nil {
		return ck.Value
	}
	return ""
}
