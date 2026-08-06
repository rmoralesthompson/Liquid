// Package liquidtest is the Liquid component test harness (D23): render a
// component through a real App, query the resulting HTML, fire allowlisted
// actions, and assert on the returned patch or envelope. It drives the same
// HTTP runtime seam a browser does — no framework internals are reached into —
// so "it passes in liquidtest" means the wire behavior is right.
//
// # The seam
//
// The flow mirrors a live interactive session: render, then query, fire, and
// assert.
//
//	h := liquidtest.New(t, app)      // wrap any http.Handler
//	page := h.Get("/counter")        // render a component
//	page.Text("#count")              // query the rendered HTML by selector
//	patch := page.Fire("increment")  // fire an allowlisted action
//	patch.Text("#count")             // assert on the returned patch
//
// [New] wraps an http.Handler (typically a *liquid.App) in a [Harness].
// [Harness.Get] renders a route and returns a [Page]; [Page.Text] and
// [Page.MatchSnapshot] query the rendered HTML, and [Page.HydroID] and
// [Page.CSRFToken] expose the render's session material.
//
// # Firing actions
//
// [Page.Fire] posts an event through the action allowlist, exactly as the
// browser runtime would, and returns the resulting [Patch]. Shape the event
// with [FireOption] values: [Field] adds form fields, [From] sets the
// originating selector, and [CSRF] overrides the token (the harness supplies a
// valid one by default). [Patch.Text] and [Patch.MatchSnapshot] assert on the
// swapped HTML; [Patch.CSRFToken] exposes the re-minted token.
//
// # SSE assertions
//
// [Harness.Stream] opens the server-sent events channel and returns a [Stream].
// [Stream.Next] blocks for the next [Push] frame, [Stream.Closed] reports
// whether the server closed the channel, and [Stream.Close] tears it down —
// letting a test assert on the frames a live page would receive.
//
// [Ctx] builds a liquid.Ctx from a request and route params for tests that
// exercise guards or lifecycle hooks directly.
package liquidtest
