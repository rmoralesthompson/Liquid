package liquid_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	liquid "github.com/rmoralesthompson/liquid/core"
)

const siteCSS = "body { color: teal }"

// newStaticApp builds an App serving a temp static dir holding site.css.
func newStaticApp(t *testing.T) *liquid.App {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "site.css"), []byte(siteCSS), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	app := liquid.New()
	if err := app.Static(dir); err != nil {
		t.Fatalf("Static: %v", err)
	}
	return app
}

func TestFilesInTheStaticDirAreServedUnderStatic(t *testing.T) {
	srv := newAppServer(t, newStaticApp(t))

	resp, body := get(t, srv.URL+"/static/site.css")

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %q)", resp.StatusCode, http.StatusOK, body)
	}
	if body != siteCSS {
		t.Errorf("body = %q, want the file content %q", body, siteCSS)
	}
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "max-age") {
		t.Errorf("Cache-Control = %q, want a basic caching policy (D22)", cc)
	}
}

func TestMissingStaticFileIs404AndNotCached(t *testing.T) {
	srv := newAppServer(t, newStaticApp(t))

	resp, _ := get(t, srv.URL+"/static/nope.css")

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
	if cc := resp.Header.Get("Cache-Control"); strings.Contains(cc, "max-age") {
		t.Errorf("Cache-Control = %q on a miss; a cached 404 would outlive the deploy that adds the file", cc)
	}
}

func TestStaticRejectsAMissingDirectoryAtRegistration(t *testing.T) {
	app := liquid.New()

	if err := app.Static(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Error("Static accepted a nonexistent directory; misconfiguration must fail loudly at registration")
	}
}

func TestStaticPathTraversalCannotEscapeTheDirectory(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(secret, []byte("s3cret"), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	public := filepath.Join(dir, "public")
	if err := os.Mkdir(public, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	app := liquid.New()
	if err := app.Static(public); err != nil {
		t.Fatalf("Static: %v", err)
	}
	srv := newAppServer(t, app)

	for _, path := range []string{"/static/../secret.txt", "/static/%2e%2e/secret.txt"} {
		resp, body := get(t, srv.URL+path)
		if strings.Contains(body, "s3cret") {
			t.Errorf("GET %s leaked a file outside the static dir (status %d): %q", path, resp.StatusCode, body)
		}
	}
}

func TestStaticPrefixIsUnroutedWithoutConfiguration(t *testing.T) {
	srv := newServer(t, "/", &hello{Name: "world"})

	resp, _ := get(t, srv.URL+"/static/site.css")

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want %d — no static dir was configured", resp.StatusCode, http.StatusNotFound)
	}
}
