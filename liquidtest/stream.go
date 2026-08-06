package liquidtest

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"
)

// streamTimeout bounds every blocking Stream operation: a push that has not
// arrived within it fails the test rather than hanging the run.
const streamTimeout = 5 * time.Second

// Stream is one open SSE connection to the app, reading pushed patches as
// the runtime script's EventSource would. It drives the handler in-process:
// no sockets, but the same streaming response path a browser sees.
type Stream struct {
	h *Harness
	// Code is the response status; 404 marks a refused connection (no live
	// session).
	Code   int
	cancel context.CancelFunc
	events chan Push
	done   chan struct{} // closed when the handler returns
	stop   chan struct{} // closed by Close; unblocks the reader if events backs up
	closed sync.Once
}

// Push is one server-pushed patch: the [data-hydro-id] boundary it targets
// plus the re-rendered component HTML, queryable like a page.
type Push struct {
	h *Harness
	// HydroID addresses the [data-hydro-id] boundary the patch swaps at.
	HydroID string
	// Patch is the pushed component render.
	Patch string
	// CSRF is the token the push re-minted (#52), which the runtime restamps
	// into the liquid-csrf meta tag and the patched subtree's csrf_token
	// inputs so a push-only page's token tracks the session's sliding idle
	// window instead of wedging on the next user event.
	CSRF string
	doc  *html.Node
}

// CSRFToken returns the token this push re-minted (#52) — the SSE analog of
// [Patch.CSRFToken] (#46). Fire an event with it via [CSRF] to assert a
// push-only page stays dispatchable. It is "" for a build or frame carrying
// none.
func (p *Push) CSRFToken() string { return p.CSRF }

// Text returns the trimmed text content of the first pushed element matching
// a simple selector, failing the test when nothing matches.
func (p *Push) Text(selector string) string {
	p.h.t.Helper()
	return textOf(p.h.t, p.doc, selector)
}

// streamWriter adapts the handler's streaming writes onto a pipe. It is the
// http.ResponseWriter and Flusher the SSE endpoint requires; Flush is a
// no-op because the unbuffered pipe already hands every write straight to
// the reader.
type streamWriter struct {
	header http.Header
	pw     *io.PipeWriter
	code   int
	wrote  chan struct{} // closed once the status is known
}

func (w *streamWriter) Header() http.Header { return w.header }

func (w *streamWriter) WriteHeader(code int) {
	if w.code == 0 {
		w.code = code
		close(w.wrote)
	}
}

func (w *streamWriter) Write(b []byte) (int, error) {
	w.WriteHeader(http.StatusOK)
	n, err := w.pw.Write(b)
	if err != nil {
		return n, fmt.Errorf("writing streamed response body: %w", err)
	}
	return n, nil
}

func (w *streamWriter) Flush() {}

// Stream opens the app's SSE endpoint under the harness's session cookies
// and starts reading pushed patches. Always Close it; a refused connection
// (Code set, no events) still needs its handler goroutine reaped.
func (h *Harness) Stream() *Stream {
	h.t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/hydro-sse", nil)
	ctx, cancel := context.WithCancel(req.Context())
	req = req.WithContext(ctx)
	for _, ck := range h.cookies {
		req.AddCookie(ck)
	}

	pr, pw := io.Pipe()
	w := &streamWriter{header: make(http.Header), pw: pw, wrote: make(chan struct{})}
	s := &Stream{h: h, cancel: cancel, events: make(chan Push, 64), done: make(chan struct{}), stop: make(chan struct{})}

	go func() {
		defer close(s.done)
		defer pw.Close() //nolint:errcheck // pipes only error on double close
		h.handler.ServeHTTP(w, req)
	}()
	go s.read(pr)

	select {
	case <-w.wrote:
	case <-s.done:
	case <-time.After(streamTimeout):
		cancel()
		h.t.Fatal("liquidtest: Stream() timed out waiting for response headers")
	}
	s.Code = w.code
	return s
}

// read parses SSE frames off the response body into the events channel,
// exiting when the handler's write side closes.
func (s *Stream) read(pr *io.PipeReader) {
	defer pr.Close() //nolint:errcheck // pipes only error on double close
	scanner := bufio.NewScanner(pr)
	var event string
	var data strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" { // blank line ends one frame
			if event == "patch" {
				s.dispatch(data.String())
			}
			event = ""
			data.Reset()
			continue
		}
		if v, ok := strings.CutPrefix(line, "event: "); ok {
			event = v
		}
		if v, ok := strings.CutPrefix(line, "data: "); ok {
			data.WriteString(v)
		}
	}
	close(s.events)
}

// dispatch decodes one patch frame into a Push. Decode failures are dropped
// here and surface as a Next timeout — the reader goroutine cannot fail the
// test directly.
func (s *Stream) dispatch(data string) {
	var msg struct {
		HydroID string `json:"hydroId"`
		Patch   string `json:"patch"`
		CSRF    string `json:"csrf"`
	}
	if err := json.Unmarshal([]byte(data), &msg); err != nil {
		return
	}
	doc, err := html.Parse(strings.NewReader(msg.Patch))
	if err != nil {
		return
	}
	select {
	case s.events <- Push{h: s.h, HydroID: msg.HydroID, Patch: msg.Patch, CSRF: msg.CSRF, doc: doc}:
	case <-s.stop:
		// Close was called with the buffer full: drop the push and keep
		// draining, so the handler is never wedged mid-write on the pipe.
	}
}

// Next returns the next pushed patch, failing the test when the stream
// closes or nothing arrives within the harness timeout.
func (s *Stream) Next() *Push {
	s.h.t.Helper()
	select {
	case p, ok := <-s.events:
		if !ok {
			s.h.t.Fatal("liquidtest: Next() on a closed stream")
		}
		return &p
	case <-time.After(streamTimeout):
		s.h.t.Fatal("liquidtest: Next() timed out waiting for a pushed patch")
	}
	return nil
}

// Closed reports whether the server has ended the stream, waiting up to the
// harness timeout for it — the reconnect signal a browser's EventSource
// would see as its connection dropping.
func (s *Stream) Closed() bool {
	select {
	case <-s.done:
		return true
	case <-time.After(streamTimeout):
		return false
	}
}

// Close tears the connection down from the client side and waits for the
// handler to finish. Safe to call more than once.
func (s *Stream) Close() {
	s.h.t.Helper()
	s.closed.Do(func() { close(s.stop) })
	s.cancel()
	select {
	case <-s.done:
	case <-time.After(streamTimeout):
		s.h.t.Fatal("liquidtest: Close() timed out waiting for the handler to return")
	}
}
