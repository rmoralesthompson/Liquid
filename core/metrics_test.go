package liquid

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// bumper is a minimal interactive component with one action, enough to drive a
// render and a /hydro-event through the metrics seam.
type bumper struct {
	HydroID string
	N       int
}

func (b *bumper) Selector() string { return "app-bumper" }
func (b *bumper) Template() string {
	return `<div data-hydro-id="{{ .HydroID }}"><span id="n">{{ .N }}</span><button data-liquid-action="Bump">+</button></div>`
}
func (b *bumper) Bump()             { b.N++ }
func (b *bumper) Actions() []string { return []string{"Bump"} }

// spyMetrics records every observability event for assertions.
type spyMetrics struct {
	mu             sync.Mutex
	pages, events  []int
	opened, closed int
}

func (s *spyMetrics) PageRendered(status int, _ time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pages = append(s.pages, status)
}

func (s *spyMetrics) EventDispatched(status int, _ time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, status)
}

func (s *spyMetrics) StreamOpened() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.opened++
}

func (s *spyMetrics) StreamClosed() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed++
}

func (s *spyMetrics) snapshot() (pages, events []int, opened, closed int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]int(nil), s.pages...), append([]int(nil), s.events...), s.opened, s.closed
}

var (
	metricHydroRe = regexp.MustCompile(`data-hydro-id="([A-Za-z0-9_-]+)"`)
	metricCSRFRe  = regexp.MustCompile(`<meta name="liquid-csrf" content="([^"]+)">`)
)

func submatch(t *testing.T, re *regexp.Regexp, s string) string {
	t.Helper()
	m := re.FindStringSubmatch(s)
	if m == nil {
		t.Fatalf("pattern %v not found in %q", re, s)
	}
	return m[1]
}

func awaitTrue(t *testing.T, cond func() bool) {
	t.Helper()
	for range 200 {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met within 2s")
}

// TestHealthAndReadinessEndpoints proves liveness stays up through shutdown
// while readiness flips to 503 once draining begins, so a load balancer stops
// routing new traffic to a draining instance.
func TestHealthAndReadinessEndpoints(t *testing.T) {
	app := New()

	for _, path := range []string{healthPath, readyPath} {
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("%s before drain = %d, want 200", path, rec.Code)
		}
	}

	app.drainSessions() // graceful shutdown began

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, healthPath, nil))
	if rec.Code != http.StatusOK {
		t.Errorf("health after drain = %d, want 200 (liveness must stay up)", rec.Code)
	}
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, readyPath, nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("ready after drain = %d, want 503", rec.Code)
	}
}

// TestMetricsHooksFire drives a render, an event, and an SSE connect/disconnect
// through a spy sink and asserts every hook fired with the right status, and
// that LiveSessions reflects the registry.
func TestMetricsHooksFire(t *testing.T) {
	spy := &spyMetrics{}
	app := New(WithMetrics(spy))
	if err := app.Route("/", &bumper{}); err != nil {
		t.Fatalf("Route: %v", err)
	}

	// Render establishes the interactive session.
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("render status = %d", rec.Code)
	}
	body := rec.Body.String()
	hydroID := submatch(t, metricHydroRe, body)
	csrf := submatch(t, metricCSRFRe, body)
	var session *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookieName {
			session = c
		}
	}
	if session == nil {
		t.Fatal("render set no session cookie")
	}
	if n := app.LiveSessions(); n != 1 {
		t.Errorf("LiveSessions = %d, want 1", n)
	}

	// Dispatch an event.
	payload := fmt.Sprintf(`{"hydroId":%q,"action":"Bump","csrfToken":%q}`, hydroID, csrf)
	ereq := httptest.NewRequest(http.MethodPost, hydroEventPath, strings.NewReader(payload))
	ereq.Header.Set("Content-Type", "application/json")
	ereq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session.Value})
	erec := httptest.NewRecorder()
	app.ServeHTTP(erec, ereq)
	if erec.Code != http.StatusOK {
		t.Fatalf("event status = %d, body = %s", erec.Code, erec.Body.String())
	}

	// Open an SSE stream, wait for the connect hook, then cancel to disconnect.
	sctx, scancel := context.WithCancel(context.Background())
	sreq := httptest.NewRequest(http.MethodGet, hydroSSEPath, nil).WithContext(sctx)
	sreq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session.Value})
	srec := httptest.NewRecorder()
	sdone := make(chan struct{})
	go func() {
		app.ServeHTTP(srec, sreq)
		close(sdone)
	}()
	awaitTrue(t, func() bool { _, _, opened, _ := spy.snapshot(); return opened == 1 })
	scancel()
	<-sdone
	awaitTrue(t, func() bool { _, _, _, closed := spy.snapshot(); return closed == 1 })

	pages, events, opened, closed := spy.snapshot()
	if len(pages) != 1 || pages[0] != http.StatusOK {
		t.Errorf("PageRendered calls = %v, want [200]", pages)
	}
	if len(events) != 1 || events[0] != http.StatusOK {
		t.Errorf("EventDispatched calls = %v, want [200]", events)
	}
	if opened != 1 || closed != 1 {
		t.Errorf("stream opened/closed = %d/%d, want 1/1", opened, closed)
	}
}

// TestMetricsEventDispatchRecordsRefusal proves a refused event still reports
// its status (403 for a bad CSRF token), so the seam's refusals are observable.
func TestMetricsEventDispatchRecordsRefusal(t *testing.T) {
	spy := &spyMetrics{}
	app := New(WithMetrics(spy))
	if err := app.Route("/", &bumper{}); err != nil {
		t.Fatalf("Route: %v", err)
	}

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	body := rec.Body.String()
	hydroID := submatch(t, metricHydroRe, body)
	var session *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookieName {
			session = c
		}
	}

	payload := fmt.Sprintf(`{"hydroId":%q,"action":"Bump","csrfToken":%q}`, hydroID, "forged-token")
	ereq := httptest.NewRequest(http.MethodPost, hydroEventPath, strings.NewReader(payload))
	ereq.Header.Set("Content-Type", "application/json")
	ereq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session.Value})
	app.ServeHTTP(httptest.NewRecorder(), ereq)

	_, events, _, _ := spy.snapshot()
	if len(events) != 1 || events[0] != http.StatusForbidden {
		t.Errorf("EventDispatched calls = %v, want [403]", events)
	}
}
