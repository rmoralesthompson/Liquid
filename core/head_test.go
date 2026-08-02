package liquid_test

import (
	"strings"
	"testing"

	liquid "github.com/rmoralesthompson/liquid/core"
)

type aboutPage struct{}

func (a *aboutPage) Selector() string { return "app-about" }

func (a *aboutPage) Template() string { return "<p>about us</p>" }

func (a *aboutPage) Head() liquid.Head {
	return liquid.Head{
		Title: "About — Liquid",
		Meta:  []liquid.Meta{{Name: "description", Content: "all about it"}},
	}
}

func TestHeadProviderControlsDocumentTitleAndMeta(t *testing.T) {
	srv := newServer(t, "/", &aboutPage{})

	_, body := get(t, srv.URL+"/")

	if want := "<title>About — Liquid</title>"; !strings.Contains(body, want) {
		t.Errorf("body = %q, want it to contain %q", body, want)
	}
	if want := `<meta name="description" content="all about it">`; !strings.Contains(body, want) {
		t.Errorf("body = %q, want it to contain %q", body, want)
	}
	if want := "<p>about us</p>"; !strings.Contains(body, want) {
		t.Errorf("body = %q, want the component render %q alongside the head", body, want)
	}
}

type hostileHead struct{}

func (h *hostileHead) Selector() string { return "app-hostile-head" }

func (h *hostileHead) Template() string { return placeholderHTML }

func (h *hostileHead) Head() liquid.Head {
	return liquid.Head{
		Title: "</title><script>alert(1)</script>",
		Meta:  []liquid.Meta{{Name: "description", Content: `"><script>alert(2)</script>`}},
	}
}

func TestHeadValuesAreContextuallyEscaped(t *testing.T) {
	srv := newServer(t, "/", &hostileHead{})

	_, body := get(t, srv.URL+"/")

	if strings.Contains(body, "<script>") {
		t.Fatalf("body contains unescaped head markup: %q", body)
	}
}

func TestComponentWithoutHeadProviderFallsBackToSelectorTitle(t *testing.T) {
	srv := newServer(t, "/", &hello{Name: "world"})

	_, body := get(t, srv.URL+"/")

	if want := "<title>app-hello</title>"; !strings.Contains(body, want) {
		t.Errorf("body = %q, want fallback title %q — every page is a complete document", body, want)
	}
}

type userHead struct {
	Who string `pathParam:"who"`
}

func (u *userHead) Selector() string { return "app-user-head" }

func (u *userHead) Template() string { return "<p>{{ .Who }}</p>" }

func (u *userHead) OnInit(ctx liquid.Ctx) error {
	u.Who = strings.ToUpper(u.Who)
	return nil
}

func (u *userHead) Head() liquid.Head { return liquid.Head{Title: u.Who + " — Liquid"} }

func TestHeadRunsOnThePerRequestInstanceAfterOnInit(t *testing.T) {
	srv := newServer(t, "/u/:who", &userHead{})

	_, body := get(t, srv.URL+"/u/ada")

	if want := "<title>ADA — Liquid</title>"; !strings.Contains(body, want) {
		t.Errorf("body = %q, want %q — Head must see state bound and initialized for this request", body, want)
	}
}
