package liquid

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// hydroSSEPath is the fixed endpoint the runtime script opens its
// EventSource against (D3). One stream carries every push for the browser
// session it belongs to.
const hydroSSEPath = "/hydro-sse"

// sseMsg is one pushed patch: a component re-render addressed to its
// [data-hydro-id] boundary, mirroring the fetch envelope's patch half (D19).
// A `patch` frame also carries a freshly minted CSRF token (#52) so a page
// kept alive purely by server push tracks the session's sliding idle deadline
// instead of the render's fixed horizon — the SSE analog of the event
// envelope's re-mint (#46, D15). It is empty on `swap` frames, which deliver
// deferred content that carries its own render's token (#26).
type sseMsg struct {
	HydroID string `json:"hydroId"`
	Patch   string `json:"patch"`
	CSRF    string `json:"csrf,omitempty"`
}

// sseFrame is one typed event on a stream: `patch` and `swap` in every
// build, plus the dev loop's `reload` and `diagnostics` in dev builds (D16 —
// dev events ride the same stream as patches). data must be a single line;
// JSON encoding guarantees that.
type sseFrame struct {
	event string
	data  string
}

// The two push-frame event names carried in every build: a patch re-renders
// a boundary's inner HTML, a swap replaces a deferred component's fallback
// slot element wholesale (#26).
const (
	frameEventPatch = "patch"
	frameEventSwap  = "swap"
)

// sseStream is one open SSE connection. Senders never block on it and never
// see it closed mid-send: frames go through the buffered channel, and
// disconnecting means closing done — the channel itself is never closed.
type sseStream struct {
	ch   chan sseFrame
	done chan struct{}
	once sync.Once
}

// sseBufferSize is how many pushed patches may queue for a client before the
// server gives up on it. Each patch is a full component render, so dropping
// the connection loses nothing a reconnect's full re-render won't restore
// (D20).
const sseBufferSize = 16

func newSSEStream() *sseStream {
	return &sseStream{ch: make(chan sseFrame, sseBufferSize), done: make(chan struct{})}
}

// close disconnects the stream's reader. Idempotent — eviction, expiry, and
// slow-consumer handling may race to it.
func (s *sseStream) close() {
	s.once.Do(func() { close(s.done) })
}

// send queues one frame without ever blocking. A full buffer means the
// client is not keeping up; the stream is closed so the browser reconnects
// into a full re-render of current state rather than reading stale frames
// (D20).
func (s *sseStream) send(f sseFrame) {
	select {
	case s.ch <- f:
	case <-s.done:
	default:
		s.close()
	}
}

// tokenPrefix is what a log line may carry of a hydro token: enough of a
// prefix to correlate log entries with a page's [data-hydro-id], never the
// dispatchable credential itself — tokens must stay out of the log sink
// (THREAT-MODEL.md boundary 5).
func tokenPrefix(token string) string {
	const keep = 8
	if len(token) <= keep {
		return token
	}
	return token[:keep] + "…"
}

// startPump activates a live instance's subscriptions, returning the stop
// that undoes them and the prime that nudges a current-state push. One pump
// goroutine owns the instance's push loop: an emission (or a prime) marks
// it dirty — a coalescing signal, never a blocking send, so a handler may
// emit mid-dispatch without deadlocking — and the pump then re-renders
// under the session's dispatch mutex (D20.1) and fans the patch out to the
// session's open streams. The goroutine lives exactly as long as the
// instance's registry entry: stop is wired to the entry and runs on every
// eviction and expiry path, so GC is what unsubscribes (D20).
func (a *App) startPump(sess *hydroSession, st *hydroState, hydroID string) (stop, prime func()) {
	dirty := make(chan struct{}, 1)
	notify := func() {
		select {
		case dirty <- struct{}{}:
		default:
		}
	}
	cancels := make([]func(), len(st.subs))
	for i, sub := range st.subs {
		cancels[i] = sub.subscribe(notify)
	}
	stopCh := make(chan struct{})

	go func() {
		for {
			select {
			case <-stopCh:
				return
			case <-dirty:
			}
			sess.dispatch.Lock()
			patch, err := a.renderStateLocked(st, sess.id)
			// Re-mint in lockstep with the render (D15/D2, #52): a page kept
			// alive only by server push must track the session's sliding idle
			// deadline the way the event path does (#46), or its token outlives
			// the render's fixed horizon and the next user event 403s with no
			// self-heal. The runtime restamps this into the liquid-csrf meta
			// tag and the patched subtree's csrf_token inputs (refreshCSRF).
			csrf := mintCSRF(a.csrfSecret, sess.id, a.limits.SessionIdleTimeout, a.now())
			sess.dispatch.Unlock()
			if err != nil {
				a.logger.Error("rendering pushed patch", "hydroIdPrefix", tokenPrefix(hydroID), "error", err)
				continue
			}
			frame, err := patchFrame(sseMsg{HydroID: hydroID, Patch: patch, CSRF: csrf})
			if err != nil {
				a.logger.Error("encoding sse patch", "hydroIdPrefix", tokenPrefix(hydroID), "error", err)
				continue
			}
			for _, stream := range a.hydro.sessionStreams(sess) {
				stream.send(frame)
			}
		}
	}()

	var once sync.Once
	stop = func() {
		once.Do(func() {
			close(stopCh)
			for _, cancel := range cancels {
				cancel()
			}
		})
	}
	return stop, notify
}

// sessionStreams snapshots a session's open SSE connections for a pump to
// fan a patch out to. Snapshotting under the registry's mu and sending
// outside it keeps the pump from holding the registry across sends.
func (h *hydroRegistry) sessionStreams(sess *hydroSession) []*sseStream {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]*sseStream, len(sess.streams))
	copy(out, sess.streams)
	return out
}

// attachStream adds an open SSE connection to its session, reporting false
// when the session is not live. Connecting counts as session use, like any
// other browser request. At the stream cap the session's oldest connection
// is disconnected — the registry stays bounded in every dimension a
// cookie-holder can grow (D20) — and every pump in the session is primed,
// so the connecting client starts from a push of current state rather than
// staying stale until the next emission.
func (h *hydroRegistry) attachStream(sessionID string, stream *sseStream, now time.Time, idle time.Duration, maxStreams int) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	sess := h.touchSession(sessionID, now, idle)
	if sess == nil {
		return false
	}
	if len(sess.streams) >= maxStreams {
		sess.streams[0].close()
		sess.streams = append(sess.streams[:0:0], sess.streams[1:]...)
	}
	sess.streams = append(sess.streams, stream)
	for elem := sess.lru.Front(); elem != nil; elem = elem.Next() {
		if e := elem.Value.(*hydroEntry); e.prime != nil {
			e.prime() // a non-blocking nudge; safe under mu
		}
	}
	return true
}

// detachStream removes a closed connection from its session, if the session
// is still around. Detaching is bookkeeping, not use — it does not touch the
// session's idle clock.
func (h *hydroRegistry) detachStream(sessionID string, stream *sseStream) {
	h.mu.Lock()
	defer h.mu.Unlock()
	elem, ok := h.sessions[sessionID]
	if !ok {
		return
	}
	sess := elem.Value.(*hydroSession)
	for i, open := range sess.streams {
		if open == stream {
			sess.streams = append(sess.streams[:i], sess.streams[i+1:]...)
			return
		}
	}
}

// patchFrame encodes a pushed patch as its typed stream frame. json.Marshal
// escapes newlines, so the payload is one line — exactly what a data: field
// carries.
func patchFrame(msg sseMsg) (sseFrame, error) {
	return encodeFrame(frameEventPatch, msg)
}

// swapFrame encodes a deferred component's completion as a `swap` frame: the
// runtime replaces the fallback slot element wholesale (outerHTML) with the
// pushed component root, so the child's own root element and its attributes
// land in the DOM — unlike a `patch`, which swaps only the boundary's inner
// HTML (#26). Later re-renders of the now-present boundary are ordinary
// patches.
func swapFrame(msg sseMsg) (sseFrame, error) {
	return encodeFrame(frameEventSwap, msg)
}

// encodeFrame marshals a pushed message as a typed stream frame.
func encodeFrame(event string, msg sseMsg) (sseFrame, error) {
	data, err := json.Marshal(msg)
	if err != nil {
		return sseFrame{}, fmt.Errorf("encoding sse %s: %w", event, err)
	}
	return sseFrame{event: event, data: string(data)}, nil
}

// serveHydroSSE holds one push stream open for the request's session: SSE
// headers, then queued frames as typed events until the client goes away or
// the server drops the stream (D3). A request without a live session has
// nothing to stream and is refused — except a dev build's ?dev=1 streams,
// which exist to carry reload/diagnostics frames for any page, static ones
// included (D16).
func (a *App) serveHydroSSE(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		a.logger.Error("sse endpoint needs a flushing response writer", "writer", fmt.Sprintf("%T", w))
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	stream := newSSEStream()
	if devMode && r.URL.Query().Get("dev") == "1" {
		// The dev script's stream: broadcaster-only, no session required, and
		// never counted against the session's stream budget.
		a.devAttachStream(stream)
		defer a.devDetachStream(stream)
	} else {
		ck, err := r.Cookie(sessionCookieName)
		if err != nil || ck.Value == "" {
			http.NotFound(w, r)
			return
		}
		if !a.hydro.attachStream(ck.Value, stream, a.now(), a.limits.SessionIdleTimeout, a.limits.MaxStreamsPerSession) {
			http.NotFound(w, r)
			return
		}
		defer a.hydro.detachStream(ck.Value, stream)
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	fl.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-stream.done:
			return
		case f := <-stream.ch:
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", f.event, f.data); err != nil {
				return
			}
			fl.Flush()
		}
	}
}
