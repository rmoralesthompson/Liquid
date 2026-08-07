package liquid

// This file is the observability seam (#102): a dependency-free Metrics
// interface a deployment maps onto Prometheus/OpenTelemetry/statsd, plus the
// liveness/readiness endpoints under the framework's /liquid/ namespace. Like
// WithLogger, the App pulls in no metrics backend — WithMetrics installs one,
// and the default records nothing.

import (
	"io"
	"net/http"
	"time"
)

// Framework health endpoints, under the reserved /liquid/ namespace (alongside
// the runtime script), apart from the user-owned /static/ mount.
const (
	healthPath = "/liquid/health"
	readyPath  = "/liquid/ready"
)

// Metrics receives observability events from a running App: page renders,
// interactive-event dispatches, and live SSE connections. It is the seam a
// deployment maps onto Prometheus, OpenTelemetry, statsd, or a log — the App
// itself pulls in no metrics dependency. The zero configuration is a no-op.
//
// Implementations must be safe for concurrent use and must not block: an event
// is delivered on the request's own goroutine, so slow work here slows the
// request. Aggregate cheaply (increment a counter, observe a histogram) and do
// any export out of band.
type Metrics interface {
	// PageRendered reports one page (GET route) response: the HTTP status
	// written — 200, or 500 on a render failure — and how long rendering took.
	PageRendered(status int, dur time.Duration)
	// EventDispatched reports one /hydro-event response: the status the dispatch
	// seam returned — 200 on a handled event, or the refusal code (400 payload,
	// 403 CSRF, 404 unknown session/action, 405, 413, 500) — and how long
	// handling took.
	EventDispatched(status int, dur time.Duration)
	// StreamOpened and StreamClosed bracket one live SSE connection, so their
	// running difference is the current open-stream count. Dev-only streams are
	// not reported.
	StreamOpened()
	StreamClosed()
}

// noopMetrics is the default sink: it records nothing, so an App with no
// WithMetrics option pays only an uncontended interface call per event.
type noopMetrics struct{}

func (noopMetrics) PageRendered(int, time.Duration)    {}
func (noopMetrics) EventDispatched(int, time.Duration) {}
func (noopMetrics) StreamOpened()                      {}
func (noopMetrics) StreamClosed()                      {}

// WithMetrics installs the observability sink for page renders, event
// dispatches, and SSE connections. Without it, the App records nothing. A nil
// argument is ignored, leaving the no-op default in place.
func WithMetrics(m Metrics) Option {
	return func(a *App) {
		if m != nil {
			a.metrics = m
		}
	}
}

// LiveSessions returns the number of interactive sessions currently held in the
// in-memory registry — a gauge for scraping. The registry is bounded (D20), so
// this cannot grow without limit. Safe for concurrent use.
func (a *App) LiveSessions() int { return a.hydro.len() }

// serveHealthEndpoints dispatches the framework health probes, returning true
// when it handled the request. Kept off ServeHTTP's hot path as one branch.
func (a *App) serveHealthEndpoints(w http.ResponseWriter, r *http.Request) bool {
	switch r.URL.Path {
	case healthPath:
		a.serveHealth(w, r, false)
	case readyPath:
		a.serveHealth(w, r, true)
	default:
		return false
	}
	return true
}

// serveHealth answers the liveness and readiness probes. Liveness (ready=false)
// is 200 whenever the process is serving. Readiness (ready=true) turns 503 once
// graceful shutdown has begun draining sessions, so a load balancer stops
// routing new traffic to a draining instance while in-flight requests finish.
func (a *App) serveHealth(w http.ResponseWriter, r *http.Request, ready bool) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if ready && a.draining.Load() {
		http.Error(w, "draining", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "ok\n")
}

// statusRecorder wraps an http.ResponseWriter to capture the status code a
// handler wrote, for the metrics seam. It deliberately forwards no optional
// interfaces (Flusher, Hijacker): it wraps only the buffered event and page
// responses, never the SSE stream, which needs its writer's Flusher.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// code returns the recorded status, defaulting to 200: a handler that wrote a
// body (or nothing) without an explicit WriteHeader left status at 0, which net/
// http reports to the client as 200 — Write is not intercepted, so it keeps the
// embedded writer's semantics for the SSE-free responses this wraps.
func (s *statusRecorder) code() int {
	if s.status == 0 {
		return http.StatusOK
	}
	return s.status
}
