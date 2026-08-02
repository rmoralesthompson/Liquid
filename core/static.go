package liquid

import (
	"fmt"
	"net/http"
	"os"
)

// staticPrefix is where static files are mounted (D22); fixed in v0.1.
const staticPrefix = "/static/"

// staticCacheControl is the basic caching policy for served assets (D22).
// Fingerprinted, immutable-cacheable assets are deferred past v0.1.
const staticCacheControl = "public, max-age=3600"

// Static serves the files under dir at /static/ through the stdlib file
// server (D22). The directory must exist now: a bad path is a hard error at
// registration, matching the router's loud-misconfiguration posture.
func (a *App) Static(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("registering static dir: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("registering static dir: %s is not a directory", dir)
	}
	a.static = http.StripPrefix(staticPrefix, http.FileServer(http.Dir(dir)))
	return nil
}

// cacheOnSuccess adds the static caching policy to served files while
// leaving misses uncached — a cached 404 would outlive the deploy that adds
// the file.
type cacheOnSuccess struct {
	http.ResponseWriter
}

func (w cacheOnSuccess) WriteHeader(code int) {
	if code < http.StatusBadRequest {
		w.Header().Set("Cache-Control", staticCacheControl)
	}
	w.ResponseWriter.WriteHeader(code)
}
