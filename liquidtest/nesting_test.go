package liquidtest_test

import (
	"strings"
	"testing"

	liquid "github.com/rmoralesthompson/liquid/core"
	"github.com/rmoralesthompson/liquid/liquidtest"
)

// cardOwner is the input value the nesting tests seed and expect back from
// the child render.
const cardOwner = "Ada"

// userCard is a plain child component: no HydroID, state arrives by [input].
// Template text mirrors what liquid build generates for
// `<div class="card">{{ Name }}</div>`.
type userCard struct {
	Name string
}

func (c *userCard) Selector() string { return "app-user-card" }

func (c *userCard) Template() string { return `<div class="card">{{ .Name }}</div>` }

// dashboard nests userCard, binding Owner to the card's Name field. Template
// text mirrors the compiled form of
// `<section><h1>{{ Title }}</h1><app-user-card [name]="Owner"></app-user-card></section>`.
type dashboard struct {
	Title string
	Owner string
}

func (c *dashboard) Selector() string { return "app-dashboard" }

func (c *dashboard) Template() string {
	return `<section><h1>{{ .Title }}</h1>{{liquidChild "app-user-card" "name" .Owner}}</section>`
}

// ownerPanel is an interactive parent nesting the plain userCard. Template
// text mirrors the compiled form of a [hydroId] root with a (click) button
// and a nested `<app-user-card [name]="Owner">`.
type ownerPanel struct {
	HydroID string
	Owner   string
}

func (c *ownerPanel) Selector() string { return "app-owner-panel" }

func (c *ownerPanel) Template() string {
	return `<div data-hydro-id="{{ .HydroID }}"><button data-liquid-action="Promote">promote</button>{{liquidChild "app-user-card" "name" .Owner}}</div>`
}

// Promote mutates the parent state the child's [input] is bound to.
func (c *ownerPanel) Promote() { c.Owner = "Grace" }

// Actions mirrors the compiler-generated allowlist (D10).
func (c *ownerPanel) Actions() []string { return []string{"Promote"} }

func TestPlainChildReRendersInsideParentPatch(t *testing.T) {
	app := liquid.New()
	if err := app.Register(&userCard{}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := app.Route("/", &ownerPanel{Owner: cardOwner}); err != nil {
		t.Fatalf("Route: %v", err)
	}
	h := liquidtest.New(t, app)

	page := h.Get("/")
	if got, want := page.Text(".card"), cardOwner; got != want {
		t.Fatalf("initial child render = %q, want %q", got, want)
	}

	patch := page.Fire("Promote")
	if patch.Code != 200 {
		t.Fatalf("Fire(Promote) = %d, want 200", patch.Code)
	}
	if got, want := patch.Text(".card"), "Grace"; got != want {
		t.Errorf("child in patch = %q, want %q — a plain child must re-render inside its parent's patch (D14)", got, want)
	}
}

func TestChildRenderEscapesInputValues(t *testing.T) {
	app := liquid.New()
	if err := app.Register(&userCard{}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := app.Route("/", &dashboard{Title: "Ops", Owner: "<i>hi</i>"}); err != nil {
		t.Fatalf("Route: %v", err)
	}

	page := liquidtest.New(t, app).Get("/")

	if got, want := page.Text(".card"), "<i>hi</i>"; got != want {
		t.Errorf("child text = %q, want the markup rendered as text %q", got, want)
	}
	if !strings.Contains(page.Body, "&lt;i&gt;hi&lt;/i&gt;") {
		t.Errorf("input value must flow through contextual escaping inside the child render\n--- body ---\n%s", page.Body)
	}
}

// liveBadge is a subscriber child: nested with its own [hydroId], observing
// an injected subject, so its patches push over the session's SSE stream.
type liveBadge struct {
	HydroID string
	Feed    *liquid.BehaviorSubject[int] `inject:""`
	Reading int
}

func (c *liveBadge) Selector() string { return "app-live-badge" }

func (c *liveBadge) Template() string {
	return `<div class="badge" data-hydro-id="{{ .HydroID }}"><span id="badge-reading">{{ .Reading }}</span></div>`
}

// Subscriptions declares the live binding, as a routed subscriber would.
func (c *liveBadge) Subscriptions() []liquid.Subscription {
	return []liquid.Subscription{
		liquid.Observe(c.Feed, func(v int) { c.Reading = v }),
	}
}

// badgeShell is the non-interactive parent nesting liveBadge.
type badgeShell struct{}

func (c *badgeShell) Selector() string { return "app-badge-shell" }

func (c *badgeShell) Template() string {
	return `<main>{{liquidChild "app-live-badge"}}</main>`
}

func TestSubscriberChildPushesPatchesOverSSE(t *testing.T) {
	feed := liquid.NewBehaviorSubject(0)
	app := liquid.New()
	if err := app.Provide(feed); err != nil {
		t.Fatalf("Provide: %v", err)
	}
	if err := app.Register(&liveBadge{}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := app.Route("/", &badgeShell{}); err != nil {
		t.Fatalf("Route: %v", err)
	}
	h := liquidtest.New(t, app)

	page := h.Get("/")
	childID := page.HydroID()
	if childID == "" {
		t.Fatalf("subscriber child must render its own data-hydro-id\n--- body ---\n%s", page.Body)
	}

	stream := h.Stream()
	defer stream.Close()
	if stream.Code != 200 {
		t.Fatalf("Stream() = %d, want 200", stream.Code)
	}

	feed.Next(42)
	awaitPush(t, stream, childID, "#badge-reading", "42")
}

// ghostParent nests a selector nothing registers.
type ghostParent struct{}

func (c *ghostParent) Selector() string { return "app-ghost-parent" }

func (c *ghostParent) Template() string { return `<div>{{liquidChild "app-ghost"}}</div>` }

// selfNest nests itself — a composition cycle the runtime must refuse.
type selfNest struct{}

func (c *selfNest) Selector() string { return "app-self-nest" }

func (c *selfNest) Template() string { return `<div>{{liquidChild "app-self-nest"}}</div>` }

// mismatchParent binds its int field to userCard's string Name field.
type mismatchParent struct {
	Count int
}

func (c *mismatchParent) Selector() string { return "app-mismatch-parent" }

func (c *mismatchParent) Template() string {
	return `<div>{{liquidChild "app-user-card" "name" .Count}}</div>`
}

func TestChildRenderFailuresProduceTheErrorPage(t *testing.T) {
	cases := []struct {
		name     string
		register []liquid.Component
		root     liquid.Component
	}{
		{"unregistered selector", nil, &ghostParent{}},
		{"cyclic composition", nil, &selfNest{}},
		{"input type mismatch", []liquid.Component{&userCard{}}, &mismatchParent{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := liquid.New()
			for _, c := range tc.register {
				if err := app.Register(c); err != nil {
					t.Fatalf("Register: %v", err)
				}
			}
			if err := app.Route("/", tc.root); err != nil {
				t.Fatalf("Route: %v", err)
			}

			page := liquidtest.New(t, app).Get("/")

			if page.Code != 500 {
				t.Errorf("GET / = %d, want 500\n--- body ---\n%s", page.Code, page.Body)
			}
		})
	}
}

func TestRegisterRefusesSelectorClaimedByAnotherType(t *testing.T) {
	app := liquid.New()
	if err := app.Register(&userCard{}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := app.Register(&userCard{}); err != nil {
		t.Errorf("re-registering the same component type must be a no-op, got %v", err)
	}

	err := app.Register(&sameSelectorImpostor{})
	if err == nil {
		t.Fatal("registering a different type under an already-claimed selector must fail")
	}
	if !strings.Contains(err.Error(), "app-user-card") {
		t.Errorf("conflict error should name the selector, got %v", err)
	}
}

// sameSelectorImpostor claims userCard's selector with a different type.
type sameSelectorImpostor struct{}

func (c *sameSelectorImpostor) Selector() string { return "app-user-card" }

func (c *sameSelectorImpostor) Template() string { return `<p>impostor</p>` }

// org nests dashboard, which itself nests userCard — three levels, so the
// child render must recurse.
type org struct {
	Dept string
	Lead string
}

func (c *org) Selector() string { return "app-org" }

func (c *org) Template() string {
	return `<main>{{liquidChild "app-dashboard" "title" .Dept "owner" .Lead}}</main>`
}

func TestChildSelectorsRenderRecursively(t *testing.T) {
	app := liquid.New()
	if err := app.Register(&userCard{}); err != nil {
		t.Fatalf("Register(userCard): %v", err)
	}
	if err := app.Register(&dashboard{}); err != nil {
		t.Fatalf("Register(dashboard): %v", err)
	}
	if err := app.Route("/", &org{Dept: "Eng", Lead: cardOwner}); err != nil {
		t.Fatalf("Route: %v", err)
	}

	page := liquidtest.New(t, app).Get("/")

	if page.Code != 200 {
		t.Fatalf("GET / = %d, want 200\n--- body ---\n%s", page.Code, page.Body)
	}
	if got, want := page.Text("h1"), "Eng"; got != want {
		t.Errorf("mid-level child render = %q, want %q", got, want)
	}
	if got, want := page.Text(".card"), cardOwner; got != want {
		t.Errorf("grandchild render = %q, want %q — inputs must flow through recursive child renders", got, want)
	}
}

// likeButton is an interactive child: it declares its own HydroID, so it
// gets its own session entry and patch boundary (D14).
type likeButton struct {
	HydroID string
	Label   string
	Count   int
}

func (c *likeButton) Selector() string { return "app-like-button" }

func (c *likeButton) Template() string {
	return `<div class="like" data-hydro-id="{{ .HydroID }}"><span id="likes">{{ .Label }}: {{ .Count }}</span><button data-liquid-action="Like">+1</button></div>`
}

// Like handles the button's single allowlisted action.
func (c *likeButton) Like() { c.Count++ }

// Actions mirrors the compiler-generated allowlist (D10).
func (c *likeButton) Actions() []string { return []string{"Like"} }

// staticShell is a non-interactive parent nesting the interactive child, so
// the page's only [hydroId] boundary — and its session binding — comes from
// the child.
type staticShell struct {
	Caption string
}

func (c *staticShell) Selector() string { return "app-static-shell" }

func (c *staticShell) Template() string {
	return `<article><h1>{{ .Caption }}</h1>{{liquidChild "app-like-button" "label" .Caption}}</article>`
}

func TestInteractiveNestedChildGetsItsOwnSessionEntry(t *testing.T) {
	app := liquid.New()
	if err := app.Register(&likeButton{}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := app.Route("/", &staticShell{Caption: "Likes"}); err != nil {
		t.Fatalf("Route: %v", err)
	}
	h := liquidtest.New(t, app)

	page := h.Get("/")
	if page.HydroID() == "" {
		t.Fatalf("nested interactive child must render a data-hydro-id boundary\n--- body ---\n%s", page.Body)
	}
	if page.CSRFToken() == "" {
		t.Fatalf("a page hosting an interactive child must carry the liquid-csrf meta tag\n--- body ---\n%s", page.Body)
	}

	patch := page.Fire("Like")
	if patch.Code != 200 {
		t.Fatalf("Fire(Like) against the child's hydro token = %d, want 200", patch.Code)
	}
	if got, want := patch.Text("#likes"), "Likes: 1"; got != want {
		t.Errorf("child patch = %q, want %q — the child's own action must dispatch against its live instance", got, want)
	}
}

func TestChildSelectorRendersRegisteredChildWithInputCopied(t *testing.T) {
	app := liquid.New()
	if err := app.Register(&userCard{}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := app.Route("/", &dashboard{Title: "Ops", Owner: cardOwner}); err != nil {
		t.Fatalf("Route: %v", err)
	}

	page := liquidtest.New(t, app).Get("/")

	if page.Code != 200 {
		t.Fatalf("GET / = %d, want 200\n--- body ---\n%s", page.Code, page.Body)
	}
	if got, want := page.Text("h1"), "Ops"; got != want {
		t.Errorf("parent render = %q, want %q", got, want)
	}
	if got, want := page.Text(".card"), cardOwner; got != want {
		t.Errorf("child render = %q, want %q — the [input] copy must reach the child field", got, want)
	}
}
