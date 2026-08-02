package liquidtest_test

import (
	"net/http/httptest"
	"strings"
	"testing"

	liquid "github.com/rmoralesthompson/liquid/core"
	"github.com/rmoralesthompson/liquid/liquidtest"
)

// counter is the canonical interactive component, hand-written the way
// liquid build would emit it.
type counter struct {
	HydroID string
	Count   int
}

func (c *counter) Selector() string { return "app-counter" }

func (c *counter) Template() string {
	return `<div data-hydro-id="{{ .HydroID }}"><span id="count" class="value">{{ .Count }}</span><button data-liquid-action="Increment">+1</button></div>`
}

// Increment handles the +1 button.
func (c *counter) Increment() { c.Count++ }

// Actions mirrors the compiler-generated allowlist (D10).
func (c *counter) Actions() []string { return []string{"Increment"} }

// renamer is the canonical form component, hand-written the way liquid build
// would emit it: a (submit) binding and the auto-injected CSRF input.
type renamer struct {
	HydroID   string
	CSRFToken string
	Title     string
}

func (c *renamer) Selector() string { return "app-renamer" }

func (c *renamer) Template() string {
	return `<div data-hydro-id="{{ .HydroID }}"><h2 id="title">{{ .Title }}</h2><form data-liquid-submit="Rename"><input name="title"/><input type="hidden" name="csrf_token" value="{{ .CSRFToken }}"/></form></div>`
}

// Rename handles the rename form.
func (c *renamer) Rename(e liquid.Event) { c.Title = e.String("title") }

// Close answers with a redirect instead of a patch (D19).
func (c *renamer) Close(e liquid.Event) { e.Redirect("/done") }

// Actions mirrors the compiler-generated allowlist (D10).
func (c *renamer) Actions() []string { return []string{"Rename", "Close"} }

func newRenamerHarness(t *testing.T) *liquidtest.Harness {
	t.Helper()
	app := liquid.New()
	if err := app.Route("/", &renamer{Title: "Untitled"}); err != nil {
		t.Fatalf("Route: %v", err)
	}
	return liquidtest.New(t, app)
}

func TestRenderedFormCarriesTheCSRFTokenInMetaAndHiddenInput(t *testing.T) {
	h := newRenamerHarness(t)

	page := h.Get("/")

	token := page.CSRFToken()
	if token == "" {
		t.Fatal("CSRFToken() is empty; the shell must expose the token for the runtime script")
	}
	if want := `name="csrf_token" value="` + token + `"`; !strings.Contains(page.Body, want) {
		t.Errorf("page body missing populated CSRF input %q\n--- body ---\n%s", want, page.Body)
	}
}

func newCounterHarness(t *testing.T) *liquidtest.Harness {
	t.Helper()
	app := liquid.New()
	if err := app.Route("/", &counter{}); err != nil {
		t.Fatalf("Route: %v", err)
	}
	return liquidtest.New(t, app)
}

func TestHarnessRendersAndQueriesAComponent(t *testing.T) {
	h := newCounterHarness(t)

	page := h.Get("/")

	if got := page.Text("#count"); got != "0" {
		t.Errorf(`Text("#count") = %q, want "0"`, got)
	}
	if got := page.Text(".value"); got != "0" {
		t.Errorf(`Text(".value") = %q, want "0"`, got)
	}
	if got := page.Text("button"); got != "+1" {
		t.Errorf(`Text("button") = %q, want "+1"`, got)
	}
	if page.HydroID() == "" {
		t.Error("HydroID() is empty for an interactive page")
	}
}

func TestHarnessFiresAnActionAndExposesThePatch(t *testing.T) {
	h := newCounterHarness(t)
	page := h.Get("/")

	patch := page.Fire("Increment")

	if got := patch.Text("#count"); got != "1" {
		t.Errorf(`patch Text("#count") = %q, want "1"`, got)
	}
	if !strings.Contains(patch.Envelope.Patch, `data-hydro-id`) {
		t.Errorf("Envelope.Patch = %q, want the raw patch HTML", patch.Envelope.Patch)
	}
	if patch.Envelope.Redirect != "" {
		t.Errorf("Envelope.Redirect = %q, want empty for a patch response", patch.Envelope.Redirect)
	}

	// Firing again exercises the same live instance across the harness's
	// session continuity.
	if got := page.Fire("Increment").Text("#count"); got != "2" {
		t.Errorf(`second patch Text("#count") = %q, want "2"`, got)
	}
}

func TestHarnessExposesRefusedActionsViaCode(t *testing.T) {
	h := newCounterHarness(t)
	page := h.Get("/")

	patch := page.Fire("NotAllowlisted")

	if patch.Code != 404 {
		t.Errorf("Code = %d, want 404 for an action outside the allowlist", patch.Code)
	}
	if patch.Envelope.Patch != "" {
		t.Errorf("Envelope.Patch = %q, want empty for a refused action", patch.Envelope.Patch)
	}
}

// quota is a form component exercising Bind and the typed accessors.
type quota struct {
	HydroID   string
	CSRFToken string
	Name      string
	Seats     int
	BindErr   string
}

func (c *quota) Selector() string { return "app-quota" }

func (c *quota) Template() string {
	return `<div data-hydro-id="{{ .HydroID }}"><span id="name">{{ .Name }}</span><span id="seats">{{ .Seats }}</span><span id="err">{{ .BindErr }}</span></div>`
}

// quotaForm is the shape Save binds the submitted form into.
type quotaForm struct {
	Name  string
	Seats int
}

// Save binds the whole form at once.
func (c *quota) Save(e liquid.Event) {
	var f quotaForm
	if err := e.Bind(&f); err != nil {
		c.BindErr = err.Error()
		return
	}
	c.Name, c.Seats = f.Name, f.Seats
}

// Grow reads a single typed field.
func (c *quota) Grow(e liquid.Event) { c.Seats += e.Int("by") }

// Actions mirrors the compiler-generated allowlist (D10).
func (c *quota) Actions() []string { return []string{"Save", "Grow"} }

func newQuotaHarness(t *testing.T) *liquidtest.Harness {
	t.Helper()
	app := liquid.New()
	if err := app.Route("/", &quota{}); err != nil {
		t.Fatalf("Route: %v", err)
	}
	return liquidtest.New(t, app)
}

func TestBindFillsAStructFromTheFormPayload(t *testing.T) {
	page := newQuotaHarness(t).Get("/")

	patch := page.Fire("Save",
		liquidtest.Field("name", "Ops Team"),
		liquidtest.Field("seats", "12"),
	)

	if got := patch.Text("#name"); got != "Ops Team" {
		t.Errorf(`patch Text("#name") = %q, want "Ops Team"`, got)
	}
	if got := patch.Text("#seats"); got != "12" {
		t.Errorf(`patch Text("#seats") = %q, want "12"`, got)
	}
	if got := patch.Text("#err"); got != "" {
		t.Errorf(`patch Text("#err") = %q, want no bind error`, got)
	}
}

func TestBindReportsAFieldThatDoesNotParse(t *testing.T) {
	page := newQuotaHarness(t).Get("/")

	patch := page.Fire("Save",
		liquidtest.Field("name", "Ops Team"),
		liquidtest.Field("seats", "twelve"),
	)

	if got := patch.Text("#err"); !strings.Contains(got, "seats") {
		t.Errorf(`patch Text("#err") = %q, want an error naming the unparseable "seats" field`, got)
	}
}

func TestBindIgnoresPayloadFieldsWithoutAMatchingStructField(t *testing.T) {
	page := newQuotaHarness(t).Get("/")

	// A real (submit) serializes the whole form, so the auto-injected
	// csrf_token field always rides along; Bind must not choke on it.
	patch := page.Fire("Save",
		liquidtest.Field("name", "Ops Team"),
		liquidtest.Field("seats", "3"),
		liquidtest.Field("csrf_token", page.CSRFToken()),
	)

	if got := patch.Text("#err"); got != "" {
		t.Errorf(`patch Text("#err") = %q, want unmatched payload fields ignored`, got)
	}
	if got := patch.Text("#name"); got != "Ops Team" {
		t.Errorf(`patch Text("#name") = %q, want "Ops Team"`, got)
	}
}

func TestIntAccessorParsesAFieldAndDefaultsToZero(t *testing.T) {
	page := newQuotaHarness(t).Get("/")

	if got := page.Fire("Grow", liquidtest.Field("by", "5")).Text("#seats"); got != "5" {
		t.Errorf(`seats after Grow by "5" = %q, want "5"`, got)
	}
	if got := page.Fire("Grow", liquidtest.Field("by", "not-a-number")).Text("#seats"); got != "5" {
		t.Errorf(`seats after Grow by garbage = %q, want unchanged "5": Int defaults to zero`, got)
	}
	if got := page.Fire("Grow").Text("#seats"); got != "5" {
		t.Errorf(`seats after Grow with no payload = %q, want unchanged "5"`, got)
	}
}

func TestSubmitHandlerReceivesFormPayloadThroughTypedAccessors(t *testing.T) {
	h := newRenamerHarness(t)
	page := h.Get("/")

	patch := page.Fire("Rename", liquidtest.Field("title", "Ops Dashboard"))

	if patch.Code != 200 {
		t.Fatalf("Code = %d, want 200", patch.Code)
	}
	if got := patch.Text("#title"); got != "Ops Dashboard" {
		t.Errorf(`patch Text("#title") = %q, want the submitted field value "Ops Dashboard"`, got)
	}
}

func TestHandlerRedirectReachesTheClientInsteadOfAPatch(t *testing.T) {
	h := newRenamerHarness(t)
	page := h.Get("/")

	patch := page.Fire("Close")

	if patch.Code != 200 {
		t.Fatalf("Code = %d, want 200", patch.Code)
	}
	if got := patch.Envelope.Redirect; got != "/done" {
		t.Errorf("Envelope.Redirect = %q, want %q", got, "/done")
	}
	if patch.Envelope.Patch != "" {
		t.Errorf("Envelope.Patch = %q, want empty when the handler redirects", patch.Envelope.Patch)
	}
}

func TestFireWithForgedCSRFTokenIsRefusedBeforeDispatch(t *testing.T) {
	h := newCounterHarness(t)
	page := h.Get("/")

	forged := page.Fire("Increment", liquidtest.CSRF("forged-token"))

	if forged.Code != 403 {
		t.Errorf("Code = %d, want 403 for a forged CSRF token", forged.Code)
	}
	if forged.Envelope.Patch != "" {
		t.Errorf("Envelope.Patch = %q, want empty for a refused event", forged.Envelope.Patch)
	}

	// The refused event must not have reached the handler: the next valid
	// fire sees the count still at zero.
	if got := page.Fire("Increment").Text("#count"); got != "1" {
		t.Errorf(`count after forged+valid fire = %q, want "1": the forged event must not dispatch`, got)
	}
}

func TestFireWithAMissingCSRFTokenIsRefused(t *testing.T) {
	h := newCounterHarness(t)
	page := h.Get("/")

	if got := page.Fire("Increment", liquidtest.CSRF("")).Code; got != 403 {
		t.Errorf("Code = %d, want 403 for an event with no CSRF token", got)
	}
}

func TestFireWithACSRFTokenFromAnotherSessionIsRefused(t *testing.T) {
	app := liquid.New()
	if err := app.Route("/", &counter{}); err != nil {
		t.Fatalf("Route: %v", err)
	}
	victim := liquidtest.New(t, app).Get("/")
	attacker := liquidtest.New(t, app).Get("/")

	crossed := victim.Fire("Increment", liquidtest.CSRF(attacker.CSRFToken()))

	if crossed.Code != 403 {
		t.Errorf("Code = %d, want 403: a token minted for one session must not validate under another", crossed.Code)
	}
}

// whoami records the session ID as seen from OnInit and from an event
// handler, exercising the D18 session accessor on both paths.
type whoami struct {
	HydroID   string
	InitSess  string
	EventSess string
}

func (c *whoami) Selector() string { return "app-whoami" }

func (c *whoami) Template() string {
	return `<div data-hydro-id="{{ .HydroID }}"><span id="init">{{ .InitSess }}</span><span id="event">{{ .EventSess }}</span></div>`
}

// OnInit records the session the render runs under.
func (c *whoami) OnInit(ctx liquid.Ctx) error {
	c.InitSess = ctx.Session()
	return nil
}

// Note records the session an event dispatch runs under.
func (c *whoami) Note(e liquid.Event) { c.EventSess = e.Session() }

// Actions mirrors the compiler-generated allowlist (D10).
func (c *whoami) Actions() []string { return []string{"Note"} }

func TestSessionAccessorIsStableFromOnInitThroughEventDispatch(t *testing.T) {
	app := liquid.New()
	if err := app.Route("/", &whoami{}); err != nil {
		t.Fatalf("Route: %v", err)
	}
	page := liquidtest.New(t, app).Get("/")

	initSess := page.Text("#init")
	if initSess == "" {
		t.Fatal("OnInit saw an empty Session() on an interactive render; the minted session must be visible (D18)")
	}

	if got := page.Fire("Note").Text("#event"); got != initSess {
		t.Errorf("event handler Session() = %q, want the render's session %q", got, initSess)
	}
}

func TestSessionAccessorIsEmptyForANonInteractivePage(t *testing.T) {
	app := liquid.New()
	if err := app.Route("/", &plain{}); err != nil {
		t.Fatalf("Route: %v", err)
	}
	page := liquidtest.New(t, app).Get("/")

	if got := page.Text("#sess"); got != "" {
		t.Errorf(`Session() on a session-less page = %q, want ""`, got)
	}
}

// plain is a non-interactive component recording its Session().
type plain struct {
	Sess string
}

func (c *plain) Selector() string { return "app-plain" }

func (c *plain) Template() string { return `<span id="sess">{{ .Sess }}</span>` }

// OnInit records the session the render runs under.
func (c *plain) OnInit(ctx liquid.Ctx) error {
	c.Sess = ctx.Session()
	return nil
}

func TestCtxConstructorBuildsAUsableCtxForHandTests(t *testing.T) {
	req := httptest.NewRequest("GET", "/dashboards/x?tab=alerts", nil)

	ctx := liquidtest.Ctx(req, map[string]string{"id": "d-42"})

	if got := ctx.Param("id"); got != "d-42" {
		t.Errorf(`Param("id") = %q, want "d-42"`, got)
	}
	if got := ctx.Query("tab"); got != "alerts" {
		t.Errorf(`Query("tab") = %q, want "alerts"`, got)
	}
	if err := ctx.Err(); err != nil {
		t.Errorf("embedded context is not usable: %v", err)
	}
}

func TestCtxConstructorToleratesANilRequest(t *testing.T) {
	ctx := liquidtest.Ctx(nil, nil)

	if got := ctx.Query("anything"); got != "" {
		t.Errorf(`Query on a nil-request Ctx = %q, want ""`, got)
	}
	if got := ctx.Param("anything"); got != "" {
		t.Errorf(`Param on an empty Ctx = %q, want ""`, got)
	}
}
