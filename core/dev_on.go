//go:build liquiddev

package liquid

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"html/template"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// This file is the entire dev-mode surface: it compiles only under the
// liquiddev build tag, which `liquid dev` sets when it builds the app. A
// production `go build` contains none of it (#12's acceptance criterion);
// dev_off.go supplies the inert counterparts.

// devMode reports at compile time whether this binary carries the dev
// surface.
const devMode = true

// devScriptPath serves the dev overlay/reload script, a sibling of the fixed
// runtime script.
const devScriptPath = "/liquid/dev.js"

// devControlEnv names the environment variable through which `liquid dev`
// hands its spawned app the control-stream URL to dial.
const devControlEnv = "LIQUID_DEV_CONTROL"

// devShellScript is injected into every page shell so static pages get the
// reload/overlay loop too.
const devShellScript template.HTML = `<script src="/liquid/dev.js" defer></script>`

// devScriptJS is the dev loop's browser half: one ?dev=1 EventSource for
// reload and diagnostics-overlay frames.
//
//go:embed dev.js
var devScriptJS []byte

// devMaxStreams bounds the dev broadcaster: ?dev=1 streams skip the session
// registry, and even a dev-only surface keeps every registry bounded
// (CLAUDE.md invariant). The oldest stream is disconnected at the cap.
const devMaxStreams = 128

// devState is the dev broadcaster: every open ?dev=1 stream, session or not.
type devState struct {
	mu      sync.Mutex
	streams []*sseStream
}

// initDev starts the control client when `liquid dev` spawned this process.
// The goroutine is owned by the process: it lives until exit, alongside the
// dev server that feeds it.
func (a *App) initDev() {
	url := os.Getenv(devControlEnv)
	if url == "" {
		return
	}
	go a.runDevControl(context.Background(), url)
}

// devAttachStream adds a dev stream to the broadcaster, evicting the oldest
// at the cap.
func (a *App) devAttachStream(s *sseStream) {
	a.dev.mu.Lock()
	defer a.dev.mu.Unlock()
	if len(a.dev.streams) >= devMaxStreams {
		a.dev.streams[0].close()
		a.dev.streams = append(a.dev.streams[:0:0], a.dev.streams[1:]...)
	}
	a.dev.streams = append(a.dev.streams, s)
}

// devDetachStream removes a closed dev stream from the broadcaster.
func (a *App) devDetachStream(s *sseStream) {
	a.dev.mu.Lock()
	defer a.dev.mu.Unlock()
	for i, open := range a.dev.streams {
		if open == s {
			a.dev.streams = append(a.dev.streams[:i], a.dev.streams[i+1:]...)
			return
		}
	}
}

// devBroadcast fans one dev frame out to every open dev stream.
func (a *App) devBroadcast(f sseFrame) {
	a.dev.mu.Lock()
	streams := make([]*sseStream, len(a.dev.streams))
	copy(streams, a.dev.streams)
	a.dev.mu.Unlock()
	for _, s := range streams {
		s.send(f)
	}
}

// serveDevScript serves the dev loop's browser script.
func (a *App) serveDevScript(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if _, err := w.Write(devScriptJS); err != nil {
		a.logger.Error("writing dev script", "error", err)
	}
}

// The dev frame events the control stream may carry onto /hydro-sse (D16).
const (
	devEventReload      = "reload"
	devEventDiagnostics = "diagnostics"
)

// devControlFrame is one line on the control stream `liquid dev` feeds the
// app: an event name plus its JSON payload, relayed verbatim to dev streams.
type devControlFrame struct {
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data"`
}

// runDevControl dials the dev server's control stream and relays its frames
// to the browser-facing dev streams, reconnecting until ctx ends. The dev
// server going away is normal (it restarts the app; the user Ctrl-Cs it) —
// reconnection is quiet and patient.
func (a *App) runDevControl(ctx context.Context, url string) {
	for {
		a.relayDevControl(ctx, url)
		select {
		case <-ctx.Done():
			return
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// relayDevControl holds one control connection, relaying frames until it
// drops.
func (a *App) relayDevControl(ctx context.Context, url string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		a.logger.Error("building dev control request", "url", url, "error", err)
		return
	}
	resp, err := devControlClient.Do(req)
	if err != nil {
		return // the dev server is between restarts; the caller retries
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var f devControlFrame
		if err := json.Unmarshal(line, &f); err != nil {
			a.logger.Error("decoding dev control frame", "error", err)
			continue
		}
		switch f.Event {
		case devEventReload, devEventDiagnostics:
			data := string(f.Data)
			if data == "" {
				data = "{}"
			}
			a.devBroadcast(sseFrame{event: f.Event, data: data})
		default:
			// Unknown frames are future protocol, not errors.
		}
	}
}

// devControlClient is the control stream's own HTTP client — core never
// mutates or depends on the shared http.DefaultClient.
var devControlClient = &http.Client{}

// devErrorTmpl renders the dev error page; the detail interpolates through
// html/template contextual escaping like all other output (CLAUDE.md
// invariant).
var devErrorTmpl = template.Must(template.New("devError").Parse(`<!doctype html>
<html><head><title>500 · Liquid</title></head>
<body><h1>Something went wrong</h1><p>The server hit an error handling this request.</p>
<pre style="white-space:pre-wrap;background:#1e1e1e;color:#ff8080;padding:1em">{{.}}</pre></body></html>
`))

// errorPageBody is the dev half of D18's error-page split: the full
// diagnostic — escaped, it is data — on the clean page prod serves alone.
func errorPageBody(detail string) string {
	var b strings.Builder
	if err := devErrorTmpl.Execute(&b, detail); err != nil {
		// Unreachable for a string datum; degrade to the detail-free page.
		return "<!doctype html><html><body><h1>Something went wrong</h1></body></html>"
	}
	return b.String()
}
