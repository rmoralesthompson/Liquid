package liquid_test

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	liquid "github.com/rmoralesthompson/liquid/core"
)

type hello struct {
	Name string
}

func (h *hello) Selector() string { return "app-hello" }

func (h *hello) Template() string { return "<h1>Hello, {{ .Name }}!</h1>" }

func newServer(t *testing.T, path string, c liquid.Component) *httptest.Server {
	t.Helper()
	app := liquid.New()
	if err := app.Route(path, c); err != nil {
		t.Fatalf("Route: %v", err)
	}
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)
	return srv
}

func get(t *testing.T, url string) (*http.Response, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	return resp, string(body)
}

func TestRouteRendersComponentAsHTML(t *testing.T) {
	srv := newServer(t, "/", &hello{Name: "world"})

	resp, body := get(t, srv.URL+"/")

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	if want := "<h1>Hello, world!</h1>"; !strings.Contains(body, want) {
		t.Errorf("body = %q, want it to contain %q", body, want)
	}
}

func TestInterpolatedFieldsAreContextuallyEscaped(t *testing.T) {
	srv := newServer(t, "/", &hello{Name: "<script>alert(1)</script>"})

	_, body := get(t, srv.URL+"/")

	if strings.Contains(body, "<script>") {
		t.Fatalf("body contains unescaped markup: %q", body)
	}
	if want := "&lt;script&gt;"; !strings.Contains(body, want) {
		t.Errorf("body = %q, want it to contain escaped %q", body, want)
	}
}

func TestUnknownPathIs404(t *testing.T) {
	srv := newServer(t, "/", &hello{Name: "world"})

	resp, _ := get(t, srv.URL+"/nope")

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}

type brokenTemplate struct{}

func (b *brokenTemplate) Selector() string { return "app-broken" }

func (b *brokenTemplate) Template() string { return "{{ .Oops" }

func TestInvalidTemplateFailsAtRegistrationNotRequestTime(t *testing.T) {
	app := liquid.New()

	if err := app.Route("/", &brokenTemplate{}); err == nil {
		t.Fatal("Route accepted an invalid template; want an error at registration")
	}
}

type withItems struct {
	Items []string
}

func (w *withItems) Selector() string { return "app-items" }

func (w *withItems) Template() string { return "<p>items</p>" }

func TestRouteRejectsPrototypesWithSharedReferenceFields(t *testing.T) {
	app := liquid.New()

	if err := app.Route("/", &withItems{Items: []string{"a"}}); err == nil {
		t.Error("Route accepted a prototype with a non-nil slice field; a shallow per-request copy would share it across requests")
	}
	if err := app.Route("/", &withItems{}); err != nil {
		t.Errorf("Route rejected a prototype whose reference fields are all nil: %v", err)
	}
}

func TestNonGETMethodsAreRejected(t *testing.T) {
	srv := newServer(t, "/", &hello{Name: "world"})

	resp, err := http.Post(srv.URL+"/", "text/plain", strings.NewReader(""))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusMethodNotAllowed)
	}
}

type boom struct{}

func (b *boom) Selector() string { return "app-boom" }

func (b *boom) Template() string { return "{{ .Detonate }}" }

func TestRenderFailureIs500AndLoggedToInjectedLogger(t *testing.T) {
	var logs strings.Builder
	app := liquid.New(liquid.WithLogger(slog.New(slog.NewTextHandler(&logs, nil))))
	if err := app.Route("/", &boom{}); err != nil {
		t.Fatalf("Route: %v", err)
	}
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)

	resp, _ := get(t, srv.URL+"/")

	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusInternalServerError)
	}
	if !strings.Contains(logs.String(), "rendering component") {
		t.Errorf("injected logger saw no render error; logs: %q", logs.String())
	}
}

func TestConcurrentRequestsEachRenderAFreshInstance(t *testing.T) {
	srv := newServer(t, "/", &hello{Name: "world"})

	const n = 25
	errs := make(chan error, n)
	for range n {
		go func() {
			resp, err := http.Get(srv.URL + "/")
			if err != nil {
				errs <- err
				return
			}
			defer func() { _ = resp.Body.Close() }()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				errs <- err
				return
			}
			if !strings.Contains(string(body), "<h1>Hello, world!</h1>") {
				errs <- fmt.Errorf("unexpected body: %q", body)
				return
			}
			errs <- nil
		}()
	}
	for range n {
		if err := <-errs; err != nil {
			t.Error(err)
		}
	}
}
