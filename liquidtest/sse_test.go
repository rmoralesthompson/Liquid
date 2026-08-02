package liquidtest_test

import (
	"net/http"
	"sync"
	"testing"

	liquid "github.com/rmoralesthompson/liquid/core"
	"github.com/rmoralesthompson/liquid/liquidtest"
)

// metric is the canonical push component: it reads a shared
// BehaviorSubject on render and subscribes for live updates, the D17
// "live metric" shape.
type metric struct {
	HydroID string
	Feed    *liquid.BehaviorSubject[int] `inject:""`
	Reading int
}

func (m *metric) Selector() string { return "app-metric" }

func (m *metric) Template() string {
	return `<div data-hydro-id="{{ .HydroID }}"><span id="reading">{{ .Reading }}</span></div>`
}

// OnInit seeds the render from the subject's current value (request-scoped
// read, no subscription).
func (m *metric) OnInit(liquid.Ctx) error {
	m.Reading = m.Feed.Value()
	return nil
}

// Subscriptions declares the live binding: every emission updates Reading
// and pushes the re-render.
func (m *metric) Subscriptions() []liquid.Subscription {
	return []liquid.Subscription{
		liquid.Observe(m.Feed, func(v int) { m.Reading = v }),
	}
}

func newMetricHarness(t *testing.T, feed *liquid.BehaviorSubject[int]) *liquidtest.Harness {
	t.Helper()
	app := liquid.New()
	if err := app.Provide(feed); err != nil {
		t.Fatalf("Provide: %v", err)
	}
	if err := app.Route("/", &metric{}); err != nil {
		t.Fatalf("Route: %v", err)
	}
	return liquidtest.New(t, app)
}

func TestStreamWithoutALiveSessionIsRefused(t *testing.T) {
	h := newCounterHarness(t)

	// No Get first: the harness carries no session cookie, so the server has
	// nothing to attach a stream to.
	stream := h.Stream()
	defer stream.Close()

	if stream.Code != http.StatusNotFound {
		t.Fatalf("Stream() without a session: code = %d, want %d", stream.Code, http.StatusNotFound)
	}
}

func TestStreamConnectsForALiveSession(t *testing.T) {
	h := newCounterHarness(t)
	h.Get("/")

	stream := h.Stream()
	defer stream.Close()

	if stream.Code != http.StatusOK {
		t.Fatalf("Stream() for a live session: code = %d, want %d", stream.Code, http.StatusOK)
	}
}

// awaitPush reads pushes until one addressed to hydroID shows want, failing
// after a few tries. Connect-time current-state pushes and coalescing make
// the exact push sequence timing-dependent; what the stream owes the client
// is that the wanted state arrives.
func awaitPush(t *testing.T, stream *liquidtest.Stream, hydroID, selector, want string) {
	t.Helper()
	var seen []string
	for range 5 {
		push := stream.Next()
		got := push.Text(selector)
		if got == want && (hydroID == "" || push.HydroID == hydroID) {
			return
		}
		seen = append(seen, got)
	}
	t.Fatalf("no push for %q showed %q; saw %v", hydroID, want, seen)
}

func TestConnectingStreamIsPrimedWithCurrentState(t *testing.T) {
	feed := liquid.NewBehaviorSubject(1)
	h := newMetricHarness(t, feed)

	page := h.Get("/")
	// Emitted after the render but before any stream exists: without a
	// connect-time push this state would be invisible until the next
	// emission, however far away that is.
	feed.Next(42)

	stream := h.Stream()
	defer stream.Close()

	awaitPush(t, stream, page.HydroID(), "#reading", "42")
}

func TestStreamsPerSessionAreBoundedByEvictingTheOldest(t *testing.T) {
	feed := liquid.NewBehaviorSubject(0)
	app := liquid.New(liquid.WithLimits(liquid.Limits{MaxStreamsPerSession: 1}))
	if err := app.Provide(feed); err != nil {
		t.Fatalf("Provide: %v", err)
	}
	if err := app.Route("/", &metric{}); err != nil {
		t.Fatalf("Route: %v", err)
	}
	h := liquidtest.New(t, app)
	page := h.Get("/")

	oldest := h.Stream()
	defer oldest.Close()
	newest := h.Stream()
	defer newest.Close()

	// The registry must stay bounded in every dimension a cookie-holder can
	// grow (CLAUDE.md invariant): at the cap, a new stream evicts the
	// session's oldest.
	if !oldest.Closed() {
		t.Fatal("opening a stream past MaxStreamsPerSession left the oldest open; want it disconnected")
	}
	feed.Next(8)
	awaitPush(t, newest, page.HydroID(), "#reading", "8")
}

func TestReconnectYieldsCurrentStateWithNoMissedPatchReplay(t *testing.T) {
	feed := liquid.NewBehaviorSubject(1)
	h := newMetricHarness(t, feed)

	first := h.Get("/")
	stream := h.Stream()
	feed.Next(2)
	awaitPush(t, stream, first.HydroID(), "#reading", "2")
	stream.Close()

	// Two emissions while disconnected. Reconnecting must surface only the
	// current state (3) — a replay would deliver the intermediate 90 first
	// (D20).
	feed.Next(90)
	feed.Next(3)

	// A browser reconnect is a full re-render of current state (the runtime
	// reloads the page) followed by a fresh stream.
	page := h.Get("/")
	if got := page.Text("#reading"); got != "3" {
		t.Fatalf("re-render after reconnect shows %q, want the current value 3", got)
	}
	reconnected := h.Stream()
	defer reconnected.Close()

	feed.Next(4)

	// The pre-reload instance is still registered under this session
	// (indistinguishable from a second tab), so its pushes ride the stream
	// too; wait for the re-rendered page's own. The intermediate 90 must
	// never appear — connect-time pushes carry current state, not history.
	for range 5 {
		push := reconnected.Next()
		if got := push.Text("#reading"); got == "90" {
			t.Fatal("a missed intermediate emission was replayed after reconnect; want current state only (D20)")
		} else if got == "4" && push.HydroID == page.HydroID() {
			return
		}
	}
	t.Fatalf("no pushed patch addressed the re-rendered page %q with the post-reconnect state", page.HydroID())
}

func TestSessionEvictionDisconnectsItsStream(t *testing.T) {
	feed := liquid.NewBehaviorSubject(0)
	app := liquid.New(liquid.WithLimits(liquid.Limits{MaxSessions: 1}))
	if err := app.Provide(feed); err != nil {
		t.Fatalf("Provide: %v", err)
	}
	if err := app.Route("/", &metric{}); err != nil {
		t.Fatalf("Route: %v", err)
	}
	first := liquidtest.New(t, app)
	first.Get("/")
	stream := first.Stream()
	defer stream.Close()

	// A second browser at the one-session cap evicts the first session; its
	// open stream must drop so that browser reconnects instead of waiting on
	// a dead session (D20).
	liquidtest.New(t, app).Get("/")

	if !stream.Closed() {
		t.Fatal("evicted session's stream stayed open; want the server to disconnect it")
	}
}

func TestEveryStreamOfASessionGetsThePush(t *testing.T) {
	feed := liquid.NewBehaviorSubject(0)
	h := newMetricHarness(t, feed)
	page := h.Get("/")

	// Two tabs of one browser session: one stream each.
	tab1 := h.Stream()
	defer tab1.Close()
	tab2 := h.Stream()
	defer tab2.Close()

	feed.Next(5)

	awaitPush(t, tab1, page.HydroID(), "#reading", "5")
	awaitPush(t, tab2, page.HydroID(), "#reading", "5")
}

// ticker extends metric with an action that emits to the very subject it
// observes — the shape where a naive synchronous delivery would deadlock
// dispatch against itself.
type ticker struct {
	HydroID string
	Feed    *liquid.BehaviorSubject[int] `inject:""`
	Reading int
}

func (c *ticker) Selector() string { return "app-ticker" }

func (c *ticker) Template() string {
	return `<div data-hydro-id="{{ .HydroID }}"><span id="reading">{{ .Reading }}</span><button data-liquid-action="Bump">+</button></div>`
}

// OnInit seeds the render from the subject's current value.
func (c *ticker) OnInit(liquid.Ctx) error {
	c.Reading = c.Feed.Value()
	return nil
}

// Bump emits through the subject rather than mutating state directly, so
// every subscribed session sees the change.
func (c *ticker) Bump() { c.Feed.Next(c.Feed.Value() + 1) }

// Actions mirrors the compiler-generated allowlist (D10).
func (c *ticker) Actions() []string { return []string{"Bump"} }

// Subscriptions declares the live binding.
func (c *ticker) Subscriptions() []liquid.Subscription {
	return []liquid.Subscription{
		liquid.Observe(c.Feed, func(v int) { c.Reading = v }),
	}
}

// Two behaviors in one shape: an in-dispatch emission must not deadlock
// (the dirty signal never blocks), and the event's own response patch must
// already reflect the emission rather than trailing one push behind.
func TestHandlerMayEmitToItsOwnSubjectMidDispatch(t *testing.T) {
	feed := liquid.NewBehaviorSubject(0)
	app := liquid.New()
	if err := app.Provide(feed); err != nil {
		t.Fatalf("Provide: %v", err)
	}
	if err := app.Route("/", &ticker{}); err != nil {
		t.Fatalf("Route: %v", err)
	}
	h := liquidtest.New(t, app)

	page := h.Get("/")
	stream := h.Stream()
	defer stream.Close()

	patch := page.Fire("Bump")
	if patch.Code != http.StatusOK {
		t.Fatalf("Fire(Bump) = %d, want %d", patch.Code, http.StatusOK)
	}
	if got := patch.Text("#reading"); got != "1" {
		t.Errorf("event patch shows %q, want 1", got)
	}
	awaitPush(t, stream, page.HydroID(), "#reading", "1")
	if got := feed.Value(); got != 1 {
		t.Errorf("subject value = %d, want 1", got)
	}
}

// Pin (green on write): the machinery it exercises exists; this holds the
// whole push loop race-free under contention. Eight goroutines hammer the
// subject while the main goroutine fires dispatch events into the same
// instance and a stream fans out — -race owns the interleavings, and the
// final event patch proves convergence on the last value.
func TestConcurrentEmissionsAndDispatchConverge(t *testing.T) {
	feed := liquid.NewBehaviorSubject(0)
	app := liquid.New()
	if err := app.Provide(feed); err != nil {
		t.Fatalf("Provide: %v", err)
	}
	if err := app.Route("/", &ticker{}); err != nil {
		t.Fatalf("Route: %v", err)
	}
	h := liquidtest.New(t, app)
	page := h.Get("/")
	stream := h.Stream()
	defer stream.Close()

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range 50 {
				feed.Next(j)
			}
		}()
	}
	// Dispatch events race the emissions (Fire stays on the test goroutine —
	// its failure paths may only run there).
	for range 10 {
		page.Fire("Bump")
	}
	wg.Wait()

	feed.Next(9_999)
	patch := page.Fire("Bump")
	if got := patch.Text("#reading"); got != "10000" {
		t.Errorf("after the storm, Bump's patch shows %q, want 10000 (9999 + 1: latest state, fully converged)", got)
	}
}

func TestSubjectEmissionReachesTheSessionAsAPushedPatch(t *testing.T) {
	feed := liquid.NewBehaviorSubject(7)
	h := newMetricHarness(t, feed)

	page := h.Get("/")
	if got := page.Text("#reading"); got != "7" {
		t.Fatalf("initial render shows %q, want the subject's current value 7", got)
	}
	stream := h.Stream()
	defer stream.Close()

	feed.Next(42)

	awaitPush(t, stream, page.HydroID(), "#reading", "42")
}
