package liquid

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// plan is a closed-domain enum (D30) carried by the typed payload below.
type plan string

const (
	planFree plan = "free"
	planPro  plan = "pro"
)

// signupForm is a typed handler payload (#105) with a closed-domain field and a
// Validate method — the full forms surface in one struct.
type signupForm struct {
	Email string
	Plan  plan
}

func (f signupForm) Validate() Errors {
	var e Errors
	if !strings.Contains(f.Email, "@") {
		e.Add("Email", "must contain @")
	}
	return e
}

type signup struct {
	HydroID string
	Errors  Errors
	Saved   string
}

func (c *signup) Selector() string { return "app-signup" }
func (c *signup) Template() string {
	return `<div data-hydro-id="{{ .HydroID }}"><span id="saved">{{ .Saved }}</span>{{ range .Errors.For "Email" }}<p class="err">{{ . }}</p>{{ end }}</div>`
}
func (c *signup) Submit(f signupForm) { c.Saved = f.Email }
func (c *signup) Actions() []string   { return []string{"Submit"} }

// PayloadDomains is what the compiler generates for Submit's closed-domain Plan
// field — hand-written here to drive the runtime seam. Its presence for an
// unguarded action is the #105 / ADR-0004 resolution of #85.
func (c *signup) PayloadDomains() map[string]map[string][]string {
	return map[string]map[string][]string{"Submit": {"plan": {"free", "pro"}}}
}

type formSession struct{ session, hydroID, csrf string }

func renderForm(t *testing.T, app *App) formSession {
	t.Helper()
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("render status = %d", rec.Code)
	}
	body := rec.Body.String()
	var session string
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookieName {
			session = c.Value
		}
	}
	if session == "" {
		t.Fatal("render set no session cookie")
	}
	return formSession{session: session, hydroID: submatch(t, metricHydroRe, body), csrf: submatch(t, metricCSRFRe, body)}
}

func submitForm(t *testing.T, app *App, s formSession, payload map[string]string) (int, Envelope) {
	t.Helper()
	body, err := json.Marshal(struct {
		HydroID   string            `json:"hydroId"`
		Action    string            `json:"action"`
		Payload   map[string]string `json:"payload"`
		CSRFToken string            `json:"csrfToken"`
	}{s.hydroID, "Submit", payload, s.csrf})
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, hydroEventPath, strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: s.session})
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	var env Envelope
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Fatalf("decode envelope: %v (body %q)", err, rec.Body.String())
		}
	}
	return rec.Code, env
}

// TestTypedPayloadBindsValidatesAndSurfacesErrors exercises the whole #105 seam:
// a typed payload binds, its Validate gates the handler, failures re-render with
// errors, and a closed-domain field is enforced without a guard (closing #85).
func TestTypedPayloadBindsValidatesAndSurfacesErrors(t *testing.T) {
	app := New()
	if err := app.Route("/", &signup{}); err != nil {
		t.Fatalf("Route: %v", err)
	}
	live := renderForm(t, app)

	// Invalid email: Validate fails → handler NOT called → error surfaced.
	code, env := submitForm(t, app, live, map[string]string{"email": "nope", "plan": "free"})
	if code != http.StatusOK {
		t.Fatalf("invalid submit status = %d, want 200 (re-render with errors)", code)
	}
	if !strings.Contains(env.Patch, "must contain @") {
		t.Errorf("patch missing validation error; got %q", env.Patch)
	}
	if strings.Contains(env.Patch, ">nope<") {
		t.Error("handler ran on an invalid payload: Saved was set")
	}

	// Valid payload: handler runs, prior errors cleared.
	_, env2 := submitForm(t, app, live, map[string]string{"email": "a@b.com", "plan": "pro"})
	if !strings.Contains(env2.Patch, "a@b.com") {
		t.Errorf("valid submit did not run the handler; patch = %q", env2.Patch)
	}
	if strings.Contains(env2.Patch, "must contain @") {
		t.Error("validation errors not cleared after a valid submit")
	}

	// Out-of-domain closed-domain value: refused 400 at the seam WITHOUT a guard
	// — the #105 / ADR-0004 resolution of #85.
	code3, _ := submitForm(t, app, live, map[string]string{"email": "a@b.com", "plan": "enterprise"})
	if code3 != http.StatusBadRequest {
		t.Errorf("out-of-domain plan status = %d, want 400 (closed domain enforced without a guard)", code3)
	}
}

// TestTypedPayloadActionRegisters proves the four handler shapes all register,
// including the two new typed ones (#105).
func TestTypedPayloadActionRegisters(t *testing.T) {
	app := New()
	if err := app.Route("/", &signup{}); err != nil {
		t.Fatalf("a typed-payload handler must register: %v", err)
	}
	_ = planFree
	_ = planPro
}
