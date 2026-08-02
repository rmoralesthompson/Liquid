// Package liquidtest is the component test harness (D23): render a component
// through a real App, query the resulting HTML, fire allowlisted actions,
// and assert on the returned patch or envelope. It drives the same HTTP
// runtime seam a browser does — no framework internals are reached into —
// so "it passes in liquidtest" means the wire behavior is right.
package liquidtest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/net/html"

	liquid "github.com/rmoralesthompson/liquid/core"
)

// Harness drives one liquid.App (or any handler) through in-process HTTP
// requests, carrying cookies across them so hydro sessions stay live.
type Harness struct {
	t       testing.TB
	handler http.Handler
	cookies map[string]*http.Cookie
}

// New wraps an app for testing. The handler is usually a *liquid.App.
func New(t testing.TB, handler http.Handler) *Harness {
	return &Harness{t: t, handler: handler, cookies: make(map[string]*http.Cookie)}
}

// do executes one request with the harness's accumulated cookies and folds
// any Set-Cookie responses back in.
func (h *Harness) do(req *http.Request) *httptest.ResponseRecorder {
	for _, ck := range h.cookies {
		req.AddCookie(ck)
	}
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)
	for _, ck := range rec.Result().Cookies() {
		h.cookies[ck.Name] = ck
	}
	return rec
}

// Page is one rendered document, queryable and able to fire actions.
type Page struct {
	h *Harness
	// Code is the response status.
	Code int
	// Body is the full response HTML.
	Body string
	doc  *html.Node
}

// Get renders the component routed at path. Transport-level failures fail
// the test; the response status is exposed as Page.Code for asserting.
func (h *Harness) Get(path string) *Page {
	h.t.Helper()
	rec := h.do(httptest.NewRequest(http.MethodGet, path, nil))
	body := rec.Body.String()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		h.t.Fatalf("liquidtest: parsing response for GET %s: %v", path, err)
	}
	return &Page{h: h, Code: rec.Code, Body: body, doc: doc}
}

// Text returns the trimmed text content of the first element matching a
// simple selector — "#id", ".class", or a tag name — failing the test when
// nothing matches.
func (p *Page) Text(selector string) string {
	p.h.t.Helper()
	return textOf(p.h.t, p.doc, selector)
}

// HydroID returns the page's data-hydro-id token, or "" for a
// non-interactive page.
func (p *Page) HydroID() string {
	var id string
	walk(p.doc, func(n *html.Node) bool {
		if v, ok := attr(n, "data-hydro-id"); ok {
			id = v
			return false
		}
		return true
	})
	return id
}

// CSRFToken returns the page's CSRF token as the runtime script reads it —
// the liquid-csrf meta tag the shell stamps on interactive renders — or ""
// for a page without one.
func (p *Page) CSRFToken() string {
	var token string
	walk(p.doc, func(n *html.Node) bool {
		if name, ok := attr(n, "name"); ok && n.Data == "meta" && name == "liquid-csrf" {
			token, _ = attr(n, "content")
			return false
		}
		return true
	})
	return token
}

// FireOption adjusts one Fire call away from the faithful-browser default.
type FireOption func(*fireConfig)

type fireConfig struct {
	csrf   *string
	fields map[string]string
}

// CSRF overrides the token Fire sends — standing in for a forged or stolen
// token. The default is the page's own CSRFToken.
func CSRF(token string) FireOption {
	return func(c *fireConfig) { c.csrf = &token }
}

// Field adds one payload field to the event, as the runtime script's form
// serialization would for a (submit).
func Field(name, value string) FireOption {
	return func(c *fireConfig) {
		if c.fields == nil {
			c.fields = make(map[string]string)
		}
		c.fields[name] = value
	}
}

// Fire posts the named action for this page's hydro session, as the runtime
// script would — carrying the page's CSRF token unless overridden — and
// returns the decoded response. A refused event (an unknown action or token
// is a 404, a bad CSRF token a 403) comes back with Code set and an empty
// envelope, so refusals are assertable; an undecodable 200 fails the test.
func (p *Page) Fire(action string, opts ...FireOption) *Patch {
	p.h.t.Helper()
	hydroID := p.HydroID()
	if hydroID == "" {
		p.h.t.Fatal("liquidtest: Fire on a page with no data-hydro-id; is the component interactive?")
	}
	cfg := fireConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}
	csrf := p.CSRFToken()
	if cfg.csrf != nil {
		csrf = *cfg.csrf
	}
	payload, err := json.Marshal(map[string]any{
		"hydroId":   hydroID,
		"action":    action,
		"payload":   cfg.fields,
		"csrfToken": csrf,
	})
	if err != nil {
		p.h.t.Fatalf("liquidtest: encoding event payload: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/hydro-event", strings.NewReader(string(payload)))
	req.Header.Set("Content-Type", "application/json")
	rec := p.h.do(req)
	patch := &Patch{h: p.h, Code: rec.Code}
	if rec.Code != http.StatusOK {
		patch.doc = &html.Node{Type: html.DocumentNode}
		return patch
	}
	if decodeErr := json.Unmarshal(rec.Body.Bytes(), &patch.Envelope); decodeErr != nil {
		p.h.t.Fatalf("liquidtest: firing %s: response is not an envelope: %v", action, decodeErr)
	}
	doc, err := html.Parse(strings.NewReader(patch.Envelope.Patch))
	if err != nil {
		p.h.t.Fatalf("liquidtest: firing %s: parsing patch: %v", action, err)
	}
	patch.doc = doc
	return patch
}

// Patch is one hydro event response: the raw envelope plus the patch HTML,
// queryable like a page.
type Patch struct {
	h *Harness
	// Code is the event response status; 404 marks a refused action or an
	// unknown hydro token.
	Code int
	// Envelope is the decoded D19 response: patch HTML or a redirect. It is
	// zero for non-200 responses.
	Envelope liquid.Envelope
	doc      *html.Node
}

// Text returns the trimmed text content of the first patch element matching
// a simple selector, failing the test when nothing matches.
func (p *Patch) Text(selector string) string {
	p.h.t.Helper()
	return textOf(p.h.t, p.doc, selector)
}

// Ctx builds a liquid.Ctx for unit-testing lifecycle hooks by hand, outside
// a running App. A nil req gets a default GET / request, so the zero-config
// call is safe; params seeds what Ctx.Param returns.
func Ctx(req *http.Request, params map[string]string) liquid.Ctx {
	if req == nil {
		req = httptest.NewRequest(http.MethodGet, "/", nil)
	}
	return liquid.NewCtx(req, params)
}
