// Package liquid is the Liquid runtime: a server-driven UI framework with an
// Angular-style component model and LiveView-style interactivity. Components
// live and execute on the server; the browser receives rendered HTML and a
// small runtime script that swaps server-computed patches into the DOM.
//
// # Component model
//
// A [Component] is a plain Go type that reports a Selector (its custom element
// tag) and a Template (compiled .lsx markup, usually generated from a paired
// .lsx file by liquid build). Exported struct fields are template-visible
// state. All interpolation flows through html/template's contextual escaping.
//
// Register components on an [App] and bind them to URLs:
//
//	app := liquid.New()
//	_ = app.Route("/", &HomePage{})
//	http.ListenAndServe(":8080", app)
//
// Templates are parsed once, at registration; every request renders a fresh
// component instance. Registered components act only as prototypes — they are
// never shared as mutable state across requests.
//
// # Hydro and sessions
//
// "Hydro" is Liquid's interactivity layer. An interactive render mints an
// opaque session token and a hydro boundary; the runtime script posts user
// events back to the server, which dispatches them against the live component
// instance and returns an [Envelope] — an HTML patch to swap at the boundary,
// or a redirect. Events for one hydro session are serialized: they are never
// dispatched concurrently against a single live instance. The in-memory
// session registry is bounded (see [Limits], [DefaultMaxSessions]); eviction
// keeps unauthenticated traffic from growing it without limit.
//
// # Invariants a caller must respect
//
//   - Component instances are per-request (or per interactive session) —
//     never shared mutable singletons across requests. App-lifetime state
//     belongs in a service registered with App.Provide and read through an
//     observable such as [BehaviorSubject].
//   - Session tokens are opaque random strings — treat them as such; never
//     derive meaning from their bytes.
//   - Event handlers are reached through a compile-time action allowlist, not
//     by reflecting method names off client input.
//   - All template output is escaped by html/template. Do not assemble HTML
//     by string concatenation around user data.
//
// # Reactive state
//
// [BehaviorSubject] is mutex-guarded observable state that always holds a
// current value ([BehaviorSubject.Value]) and notifies subscribers on
// [BehaviorSubject.Next]. The [Observable] combinators — [Map], [Throttle],
// [CombineLatest], [Interval], and [Observe] — derive and consume streams.
// Only interactive sessions hold subscriptions, and the framework cancels
// them when the session ends.
//
// # Loading and routing
//
// [Load] and [Loader] describe asynchronous data a component needs; [Ctx.Fanout]
// runs several loaders together under the request's context. [Guard] functions
// decide whether a request may activate a route (returning [Allow], [Deny], or
// [Redirect]) and run before the component is instantiated. [Head] carries the
// document title and meta tags for a rendered page.
//
// # Primary entry points
//
//   - [New] constructs an [App]; [App.Route], [App.Register], [App.Provide],
//     and [App.Static] wire it up, and [App.ServeHTTP] serves it.
//   - [Ctx] is the per-request context handed to guards and lifecycle hooks;
//     [Event] is the payload handed to an interactive handler.
//   - [Head], [BehaviorSubject], [Guard], [Load], and the [Observable]
//     combinators cover state, guarding, and data loading.
//
// Configure the app with [Option] values such as [WithLimits] and [WithLogger].
// Logging goes through log/slog with a pluggable handler.
package liquid
