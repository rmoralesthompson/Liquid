package liquid_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	liquid "github.com/rmoralesthompson/liquid/core"
)

// mover is the hand-written seam mirror of what liquid build emits for a
// component with a D30 payload contract: an action whose payload carries a
// closed-domain field (Dir) and a boundary guard (MoveGuard). The closed
// domain and guard are what the dispatch seam must enforce before Move runs.
type mover struct {
	HydroID string
	Pos     int
}

func (c *mover) Selector() string { return "app-mover" }

func (c *mover) Template() string {
	return `<div data-hydro-id="{{ .HydroID }}"><span id="pos">{{ .Pos }}</span>` +
		`<button data-liquid-action="Move">move</button></div>`
}

// movePayload is Move's payload struct; Dir is a closed domain, Step an
// unbounded scalar the guard constrains.
type movePayload struct {
	Dir  string
	Step int
}

// Move shifts Pos in the requested direction; it trusts the seam to have
// already refused an out-of-domain Dir or a non-positive Step.
func (c *mover) Move(e liquid.Event) {
	var p movePayload
	_ = e.Bind(&p)
	if p.Dir == "up" {
		c.Pos += p.Step
		return
	}
	c.Pos -= p.Step
}

// MoveGuard is the D30 boundary guard: a pure predicate refusing a
// non-positive step before any effect fires.
func (c *mover) MoveGuard(p movePayload) bool { return p.Step > 0 }

// Actions mirrors the compiler-generated allowlist (D10).
func (c *mover) Actions() []string { return []string{"Move"} }

// PayloadDomains mirrors the compiler-generated closed-domain contract (D30):
// Move's Dir field admits only "up" or "down".
func (c *mover) PayloadDomains() map[string]map[string][]string {
	return map[string]map[string][]string{"Move": {"dir": {"down", "up"}}}
}

// fireContract POSTs a Move event carrying payload and returns the status and
// decoded envelope, so a test can assert both refusal and resulting state.
func fireContract(t *testing.T, srv *httptest.Server, sess liveSession, payload map[string]string) (int, envelope) {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"hydroId": sess.hydro, "action": "Move", "csrfToken": sess.csrf, "payload": payload,
	})
	if err != nil {
		t.Fatalf("marshaling event body: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/hydro-event", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("building event request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "liquid_session", Value: sess.id})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /hydro-event: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading event response: %v", err)
	}
	var env envelope
	if resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal(raw, &env); err != nil {
			t.Fatalf("event response is not a JSON envelope: %v\n--- body ---\n%s", err, raw)
		}
	}
	return resp.StatusCode, env
}

func TestPayloadContractAdmitsInDomainGuardedPayload(t *testing.T) {
	srv := newServer(t, "/", &mover{})
	sess := renderInteractive(t, srv, "/")

	status, env := fireContract(t, srv, sess, map[string]string{"dir": "up", "step": "2"})

	if status != http.StatusOK {
		t.Fatalf("status = %d, want %d: an in-domain, guard-passing payload must dispatch", status, http.StatusOK)
	}
	if want := `<span id="pos">2</span>`; !strings.Contains(env.Patch, want) {
		t.Errorf("patch = %q, want it to contain %q", env.Patch, want)
	}
}

func TestPayloadContractRefusesOutOfDomainValue(t *testing.T) {
	srv := newServer(t, "/", &mover{})
	sess := renderInteractive(t, srv, "/")

	status, _ := fireContract(t, srv, sess, map[string]string{"dir": "sideways", "step": "2"})

	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d: an out-of-domain closed-domain value must be refused (D30)", status, http.StatusBadRequest)
	}
	// The refused event must not have touched the live instance.
	if _, env := fireContract(t, srv, sess, map[string]string{"dir": "up", "step": "5"}); !strings.Contains(env.Patch, `<span id="pos">5</span>`) {
		t.Errorf("patch = %q, want pos 5: the out-of-domain event must not have dispatched", env.Patch)
	}
}

func TestPayloadContractRefusesGuardRejection(t *testing.T) {
	srv := newServer(t, "/", &mover{})
	sess := renderInteractive(t, srv, "/")

	status, _ := fireContract(t, srv, sess, map[string]string{"dir": "up", "step": "0"})

	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d: a payload the guard rejects must be refused (D30)", status, http.StatusBadRequest)
	}
	if _, env := fireContract(t, srv, sess, map[string]string{"dir": "up", "step": "3"}); !strings.Contains(env.Patch, `<span id="pos">3</span>`) {
		t.Errorf("patch = %q, want pos 3: the guard-rejected event must not have dispatched", env.Patch)
	}
}

// badGuard declares a Do action with a DoGuard method of the wrong shape: a
// <Name>Guard is a broken contract unless it is func(p <struct>) bool.
type badGuard struct {
	HydroID string
}

func (c *badGuard) Selector() string { return "app-badguard" }

func (c *badGuard) Template() string {
	return `<div data-hydro-id="{{ .HydroID }}"><button data-liquid-action="Do">go</button></div>`
}

func (c *badGuard) Do(e liquid.Event) {}

// DoGuard is mis-shaped: neither a struct payload nor a bool result.
func (c *badGuard) DoGuard(x int) string { return "" }

func (c *badGuard) Actions() []string { return []string{"Do"} }

func TestMisshapenGuardFailsRegistrationLoudly(t *testing.T) {
	app := liquid.New()

	err := app.Route("/", &badGuard{})

	if err == nil {
		t.Fatal("a component with a mis-shaped payload guard must fail registration loudly (D30)")
	}
	if !strings.Contains(err.Error(), "DoGuard") {
		t.Errorf("registration error %q must name the offending guard method", err)
	}
}

func TestPayloadContractRefusesUnbindableGuardPayload(t *testing.T) {
	srv := newServer(t, "/", &mover{})
	sess := renderInteractive(t, srv, "/")

	// Step cannot bind into the guard's int field, so the guard never sees a
	// well-formed payload: the seam refuses it rather than passing a zero value.
	status, _ := fireContract(t, srv, sess, map[string]string{"dir": "up", "step": "notanumber"})

	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d: a payload that will not bind into the guard must be refused (D30)", status, http.StatusBadRequest)
	}
}

func TestPayloadContractRefusesOmittedDomainField(t *testing.T) {
	srv := newServer(t, "/", &mover{})
	sess := renderInteractive(t, srv, "/")

	// Omitting the closed-domain Dir must not bypass the domain: an absent
	// field carries the zero value (""), which is outside {up,down}. Checking
	// only posted keys would admit it and let Move run with an out-of-domain
	// direction, breaking the seam's promise (D30). The guard-passing Step
	// isolates the domain axis as the sole reason for refusal.
	status, _ := fireContract(t, srv, sess, map[string]string{"step": "2"})

	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d: omitting a closed-domain field must be refused, not admitted as the zero value (D30)", status, http.StatusBadRequest)
	}
	// The refused event must not have touched the live instance.
	if _, env := fireContract(t, srv, sess, map[string]string{"dir": "up", "step": "5"}); !strings.Contains(env.Patch, `<span id="pos">5</span>`) {
		t.Errorf("patch = %q, want pos 5: the field-omitting event must not have dispatched", env.Patch)
	}
}
