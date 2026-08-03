package liquid_test

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	liquid "github.com/rmoralesthompson/liquid/core"
)

// This is the log-hygiene pin (#32, THREAT-MODEL.md boundary 5): the live
// credentials a hydro exchange establishes — session ID, hydro token, CSRF
// token — must never reach the log sink, including on the error paths that
// log with per-instance context.

// syncBuffer is a goroutine-safe log sink: the SSE pump logs from its own
// goroutine while the dispatch path logs from the request's.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, err := s.b.Write(p)
	if err != nil {
		return n, fmt.Errorf("buffering log output: %w", err)
	}
	return n, nil
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// glitchyMetric renders cleanly until the feed emits a nonzero reading, then
// fails every re-render — the shape that drives both re-render error-logging
// paths (event dispatch and the subscription pump) in one exchange.
type glitchyMetric struct {
	HydroID string
	Feed    *liquid.BehaviorSubject[int] `inject:""`
	Reading int
}

func (m *glitchyMetric) Selector() string { return "app-glitchy" }

func (m *glitchyMetric) Template() string {
	return `<div data-hydro-id="{{ .HydroID }}">{{ .Health }}</div>`
}

// Health errors once the reading goes nonzero, failing the html/template
// execution mid-render.
func (m *glitchyMetric) Health() (string, error) {
	if m.Reading != 0 {
		return "", errors.New("glitch")
	}
	return "ok", nil
}

// Subscriptions binds the feed so an emission marks the pump dirty.
func (m *glitchyMetric) Subscriptions() []liquid.Subscription {
	return []liquid.Subscription{liquid.Observe(m.Feed, func(v int) { m.Reading = v })}
}

// Break emits the reading that makes every subsequent render fail.
func (m *glitchyMetric) Break() { m.Feed.Next(1) }

// Actions is the compiled allowlist.
func (m *glitchyMetric) Actions() []string { return []string{"Break"} }

// waitForLog polls until the captured log output contains want — the pump
// logs asynchronously, so the hygiene assertions must wait for the line to
// exist before asserting what it omits.
func waitForLog(t *testing.T, logs *syncBuffer, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(logs.String(), want) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("log output never showed %q:\n%s", want, logs.String())
}

func TestLogsNeverCarryLiveSessionCredentials(t *testing.T) {
	logs := &syncBuffer{}
	app := liquid.New(liquid.WithLogger(slog.New(slog.NewTextHandler(logs, nil))))
	feed := liquid.NewBehaviorSubject(0)
	if err := app.Provide(feed); err != nil {
		t.Fatalf("Provide: %v", err)
	}
	if err := app.Route("/", &glitchyMetric{}); err != nil {
		t.Fatalf("Route: %v", err)
	}
	srv := httptest.NewServer(app)
	defer srv.Close()

	sess := renderInteractive(t, srv, "/")
	if status, _ := fire(t, srv, sess, "Break"); status != http.StatusInternalServerError {
		t.Fatalf("Break dispatch status = %d, want %d (the re-render must fail)", status, http.StatusInternalServerError)
	}

	// Both error paths must have logged before absence means anything: the
	// dispatch path synchronously with the 500, the pump from its goroutine.
	waitForLog(t, logs, "rendering event patch")
	waitForLog(t, logs, "rendering pushed patch")

	got := logs.String()
	for name, credential := range map[string]string{
		"session ID":  sess.id,
		"hydro token": sess.hydro,
		"CSRF token":  sess.csrf,
	} {
		if strings.Contains(got, credential) {
			t.Errorf("log output carries the live %s %q:\n%s", name, credential, got)
		}
	}
}
