//go:build liquiddev

package liquid

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// These tests run only under the liquiddev build tag — the dev surface they
// exercise does not exist in a production build (dev_off_test.go asserts
// that). They are white-box for the broadcaster and control-client internals;
// everything wire-visible goes through httptest against the real endpoints.

// devPage is a minimal static component; dev streams must work on pages with
// no hydro session at all (a fresh scaffold is static).
type devPage struct{}

func (p *devPage) Selector() string { return "app-dev-page" }
func (p *devPage) Template() string { return `<p>dev page</p>` }

func newDevApp(t *testing.T) *App {
	t.Helper()
	app := New()
	if err := app.Route("/", &devPage{}); err != nil {
		t.Fatalf("Route: %v", err)
	}
	return app
}

func TestDevBuildServesTheOverlayScriptAndInjectsIt(t *testing.T) {
	app := newDevApp(t)

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if !strings.Contains(rec.Body.String(), `<script src="/liquid/dev.js" defer></script>`) {
		t.Errorf("dev-build shell must inject the dev script, got:\n%s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/liquid/dev.js", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /liquid/dev.js = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Errorf("dev.js Content-Type = %q", ct)
	}
	for _, want := range []string{"/hydro-sse?dev=1", "diagnostics", "reload"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("dev.js should reference %q", want)
		}
	}
}

// openDevStream connects a ?dev=1 EventSource-style reader with no session
// cookie, returning a line scanner over the live response body.
func openDevStream(t *testing.T, srv *httptest.Server) (*bufio.Scanner, func()) {
	t.Helper()
	resp, err := http.Get(srv.URL + "/hydro-sse?dev=1")
	if err != nil {
		t.Fatalf("opening dev stream: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		t.Fatalf("sessionless ?dev=1 stream = %d, want 200 in a dev build", resp.StatusCode)
	}
	return bufio.NewScanner(resp.Body), func() { _ = resp.Body.Close() }
}

// nextFrame reads one "event:"/"data:" pair off the stream.
func nextFrame(t *testing.T, sc *bufio.Scanner) (event, data string) {
	t.Helper()
	for sc.Scan() {
		line := sc.Text()
		if after, ok := strings.CutPrefix(line, "event: "); ok {
			event = after
		}
		if after, ok := strings.CutPrefix(line, "data: "); ok {
			return event, after
		}
	}
	t.Fatalf("stream ended before a frame arrived: %v", sc.Err())
	return "", ""
}

func TestDevBroadcastReachesSessionlessDevStreams(t *testing.T) {
	app := newDevApp(t)
	srv := httptest.NewServer(app)
	defer srv.Close()

	sc, closeStream := openDevStream(t, srv)
	defer closeStream()

	go func() {
		// The stream attaches before the body is readable; a short settle
		// keeps the broadcast from racing the attach.
		time.Sleep(50 * time.Millisecond)
		app.devBroadcast(sseFrame{event: devEventDiagnostics, data: `[{"code":"LSX001"}]`})
	}()

	event, data := nextFrame(t, sc)
	if event != devEventDiagnostics {
		t.Errorf("event = %q, want diagnostics", event)
	}
	if !strings.Contains(data, "LSX001") {
		t.Errorf("data = %q, want the diagnostics payload", data)
	}
}

// failingInit errors during OnInit so the framework error page renders.
type failingInit struct{}

func (f *failingInit) Selector() string { return "app-failing" }
func (f *failingInit) Template() string { return `<p>never rendered</p>` }
func (f *failingInit) OnInit(Ctx) error { return errors.New("boom: the database is on fire") }

func TestDevBuildErrorPageShowsTheDiagnostic(t *testing.T) {
	app := New()
	if err := app.Route("/", &failingInit{}); err != nil {
		t.Fatalf("Route: %v", err)
	}
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "the database is on fire") {
		t.Errorf("dev error page must carry the error detail (D18 dev/prod split), got:\n%s", rec.Body.String())
	}
	// The detail is data, not markup: it must arrive escaped.
	app2 := New()
	if err := app2.Route("/", &failingInitHTML{}); err != nil {
		t.Fatalf("Route: %v", err)
	}
	rec = httptest.NewRecorder()
	app2.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if strings.Contains(rec.Body.String(), "<script>alert") {
		t.Errorf("error detail must be HTML-escaped, got:\n%s", rec.Body.String())
	}
}

type failingInitHTML struct{}

func (f *failingInitHTML) Selector() string { return "app-failing-html" }
func (f *failingInitHTML) Template() string { return `<p>never rendered</p>` }
func (f *failingInitHTML) OnInit(Ctx) error { return errors.New("<script>alert(1)</script>") }

func TestDevControlClientRelaysFramesToDevStreams(t *testing.T) {
	app := newDevApp(t)
	srv := httptest.NewServer(app)
	defer srv.Close()

	frames := make(chan string, 1)
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fl := w.(http.Flusher)
		w.WriteHeader(http.StatusOK)
		fl.Flush()
		_, _ = fmt.Fprintln(w, <-frames)
		fl.Flush()
		<-r.Context().Done()
	}))
	defer control.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		app.runDevControl(ctx, control.URL)
	}()
	defer func() {
		cancel()
		<-done
	}()

	sc, closeStream := openDevStream(t, srv)
	defer closeStream()
	time.Sleep(50 * time.Millisecond)
	frames <- `{"event":"diagnostics","data":[{"code":"LSX004"}]}`

	event, data := nextFrame(t, sc)
	if event != devEventDiagnostics {
		t.Errorf("event = %q, want diagnostics", event)
	}
	if !strings.Contains(data, "LSX004") {
		t.Errorf("data = %q, want the relayed payload", data)
	}
}

// TestDevOverlayScriptNeverUsesHTMLSinks pins dev.js's one XSS defense at
// the source-text level (#34, THREAT-MODEL.md boundaries 3-4): the overlay
// quotes untrusted .lsx/go-build text, and rendering it via textContent —
// never an HTML sink — is the entire reason a hostile diagnostic cannot
// script the dev origin. A browser-level inert-render check stays open on
// #34; this pin at least fails loudly if an HTML sink sneaks into the blob.
func TestDevOverlayScriptNeverUsesHTMLSinks(t *testing.T) {
	script := string(devScriptJS)
	if !strings.Contains(script, ".textContent") {
		t.Error("dev.js no longer assigns via textContent — the overlay's XSS defense is gone")
	}
	for _, sink := range []string{"innerHTML", "outerHTML", "insertAdjacentHTML", "document.write", "createContextualFragment", "srcdoc", "DOMParser"} {
		if strings.Contains(script, sink) {
			t.Errorf("dev.js contains HTML sink %q — diagnostics text must stay inert (textContent only)", sink)
		}
	}
}

func TestDevBroadcasterIsBoundedWithOldestEviction(t *testing.T) {
	app := New()
	first := newSSEStream()
	app.devAttachStream(first)
	for range devMaxStreams - 1 {
		app.devAttachStream(newSSEStream())
	}
	select {
	case <-first.done:
		t.Fatal("oldest stream closed before the cap was breached")
	default:
	}

	// One past the cap: the broadcaster must stay bounded (CLAUDE.md
	// invariant — every registry is bounded, dev surface included) by
	// disconnecting the oldest stream, and the newcomer must be attached.
	extra := newSSEStream()
	app.devAttachStream(extra)

	app.dev.mu.Lock()
	n := len(app.dev.streams)
	last := app.dev.streams[n-1]
	app.dev.mu.Unlock()
	if n != devMaxStreams {
		t.Errorf("broadcaster holds %d streams after breaching the cap, want %d", n, devMaxStreams)
	}
	if last != extra {
		t.Error("newest stream is not the one just attached — eviction dropped the wrong end")
	}
	select {
	case <-first.done:
	default:
		t.Error("oldest stream left open at the cap; want it disconnected (oldest-evicted)")
	}
}
