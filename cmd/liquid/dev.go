package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/rmoralesthompson/liquid/cmd/liquid/internal/compiler"
)

// The dev loop (D16): watch the app tree, recompile templates on change,
// rebuild and restart the app on success, push the D13 diagnostics as an
// in-browser overlay on failure. The reload signal is the SSE stream cycle
// itself — the restart drops every stream and the runtime reloads on
// reconnect, the contract #10 built and #13 browser-verified; diagnostics
// ride the control stream into the running app and out over /hydro-sse.

// devPollInterval is how often the watcher re-fingerprints the tree. Polling
// keeps the watcher stdlib-only (no fsnotify dependency); at this cadence a
// save-to-rebuild gap is imperceptible next to the go build that follows.
const devPollInterval = 300 * time.Millisecond

// devUsage documents the dev verb's argument shape.
const devUsage = "usage: liquid dev [dir]"

// runDev runs `liquid dev [dir]` until interrupted. dir is the app's main
// package directory; every .lsx-holding directory beneath it is compiled on
// change.
func runDev(args []string, stdout io.Writer) error {
	if len(args) > 1 {
		return errors.New(devUsage)
	}
	dir := "."
	if len(args) == 1 {
		dir = args[0]
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return fmt.Errorf("%s is not a directory (%s)", dir, devUsage)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	binDir, err := os.MkdirTemp("", "liquid-dev-")
	if err != nil {
		return fmt.Errorf("creating build dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(binDir) }()

	control, err := newDevControl()
	if err != nil {
		return err
	}
	defer control.close()

	srv := &devServer{dir: dir, out: stdout, binDir: binDir, control: control}
	defer srv.stopChild()
	return srv.watch(ctx)
}

// devServer is one `liquid dev` run: the watcher, the build cycle, and the
// child app process.
type devServer struct {
	dir     string
	out     io.Writer
	binDir  string
	control *devControl
	child   *exec.Cmd
	builds  int
}

// watch fingerprints the tree on a fixed cadence and runs a build cycle on
// every change, starting with one immediately — the first cycle is what
// bootstraps a fresh scaffold's *_gen.go files.
func (s *devServer) watch(ctx context.Context) error {
	snap, err := snapshotTree(s.dir)
	if err != nil {
		return err
	}
	s.cycle(ctx)

	tick := time.NewTicker(devPollInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			_, _ = fmt.Fprintln(s.out, "liquid dev: shutting down")
			return nil
		case <-tick.C:
		}
		next, err := snapshotTree(s.dir)
		if err != nil {
			_, _ = fmt.Fprintln(s.out, "liquid dev: watch error:", err)
			continue
		}
		if snap.equal(next) {
			continue
		}
		// Debounce: editors write in bursts; wait for the tree to hold still
		// for one poll before building.
		for settling := true; settling; {
			select {
			case <-ctx.Done():
				_, _ = fmt.Fprintln(s.out, "liquid dev: shutting down")
				return nil
			case <-time.After(devPollInterval):
			}
			settled, err := snapshotTree(s.dir)
			if err != nil || settled.equal(next) {
				settling = false
				continue
			}
			next = settled
		}
		snap = next
		s.cycle(ctx)
		// The cycle rewrites *_gen.go and binaries only, which the snapshot
		// ignores — no re-fingerprint needed.
	}
}

// cycle runs one rebuild: templates first, then the app binary, then a child
// restart. Failures push the D13 diagnostics to the browser overlay and keep
// the previous app running.
func (s *devServer) cycle(ctx context.Context) {
	start := time.Now()
	diags, err := s.compileTemplates(ctx)
	if err != nil {
		_, _ = fmt.Fprintln(s.out, "liquid dev: compile error:", err)
		return
	}
	if len(diags) == 0 {
		var binary string
		binary, diags = s.buildApp(ctx)
		if diags == nil {
			_, _ = fmt.Fprintf(s.out, "liquid dev: rebuilt in %s, restarting\n", time.Since(start).Round(time.Millisecond))
			s.restartChild(binary)
			return
		}
	}
	if err := printDiagnostics(s.out, diags, false); err != nil {
		_, _ = fmt.Fprintln(s.out, "liquid dev:", err)
	}
	s.control.broadcastDiagnostics(diags)
}

// compileTemplates runs the AOT compiler over every .lsx-holding directory
// under the app dir, collecting D13 diagnostics.
func (s *devServer) compileTemplates(ctx context.Context) ([]compiler.Diagnostic, error) {
	dirs, err := lsxDirs(s.dir)
	if err != nil {
		return nil, err
	}
	var diags []compiler.Diagnostic
	for _, d := range dirs {
		ds, err := compiler.Build(ctx, d)
		if err != nil {
			return nil, fmt.Errorf("building %s: %w", d, err)
		}
		diags = append(diags, ds...)
	}
	return diags, nil
}

// buildApp compiles the app's main package with the liquiddev tag — the tag
// is what compiles the dev surface into the binary; a production build never
// carries it. A failed build comes back as D13 diagnostics.
func (s *devServer) buildApp(ctx context.Context) (binary string, diags []compiler.Diagnostic) {
	s.builds++
	binary = filepath.Join(s.binDir, "app-"+strconv.Itoa(s.builds))
	cmd := exec.CommandContext(ctx, "go", "build", "-tags", "liquiddev", "-o", binary, ".")
	cmd.Dir = s.dir
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", goBuildDiagnostics(s.dir, stderr.String())
	}
	return binary, nil
}

// restartChild swaps the running app for the fresh binary. The old process
// gets SIGTERM and a short grace, then SIGKILL — v0.1 apps have no graceful
// shutdown to wait on (D24's lifecycle is future work).
func (s *devServer) restartChild(binary string) {
	s.stopChild()
	cmd := exec.Command(binary)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "LIQUID_DEV_CONTROL="+s.control.url)
	if err := cmd.Start(); err != nil {
		_, _ = fmt.Fprintln(s.out, "liquid dev: starting app:", err)
		return
	}
	s.child = cmd
}

// stopChild terminates the running app, if any, and reaps it.
func (s *devServer) stopChild() {
	if s.child == nil {
		return
	}
	child := s.child
	s.child = nil
	if err := child.Process.Signal(syscall.SIGTERM); err != nil {
		_ = child.Process.Kill()
	}
	done := make(chan struct{})
	go func() {
		_ = child.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		_ = child.Process.Kill()
		<-done
	}
}

// lsxDirs lists every directory under root holding at least one .lsx file,
// skipping hidden trees and testdata.
func lsxDirs(root string) ([]string, error) {
	seen := map[string]bool{}
	var dirs []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && skipDir(d.Name(), path != root) {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".lsx") {
			dir := filepath.Dir(path)
			if !seen[dir] {
				seen[dir] = true
				dirs = append(dirs, dir)
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scanning %s for templates: %w", root, err)
	}
	return dirs, nil
}

// skipDir reports whether the watcher and template scan ignore a directory.
func skipDir(name string, nested bool) bool {
	return nested && (strings.HasPrefix(name, ".") || name == "testdata" || name == "node_modules")
}

// fileStamp is one watched file's change fingerprint.
type fileStamp struct {
	size  int64
	mtime int64
}

// treeSnapshot fingerprints the watched source files under the app dir.
type treeSnapshot map[string]fileStamp

// equal reports whether two snapshots describe the same tree state.
func (s treeSnapshot) equal(o treeSnapshot) bool {
	if len(s) != len(o) {
		return false
	}
	for path, stamp := range s {
		if o[path] != stamp {
			return false
		}
	}
	return true
}

// snapshotTree fingerprints every .go and .lsx source under dir, ignoring
// *_gen.go — the build cycle rewrites those, and watching them would loop
// the watcher on its own output.
func snapshotTree(dir string) (treeSnapshot, error) {
	snap := treeSnapshot{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDir(d.Name(), path != dir) {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		isSource := strings.HasSuffix(name, ".lsx") ||
			(strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_gen.go"))
		if !isSource {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return fmt.Errorf("stat %s: %w", path, err)
		}
		snap[path] = fileStamp{size: info.Size(), mtime: info.ModTime().UnixNano()}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("fingerprinting %s: %w", dir, err)
	}
	return snap, nil
}

// goBuildLine matches one position-carrying line of `go build` output.
var goBuildLine = regexp.MustCompile(`^(.+\.go):(\d+):(\d+): (.+)$`)

// goBuildDiagnostics translates `go build` stderr into the D13 shape so the
// overlay and agents read app-build failures the same way as template
// diagnostics. GO001 marks a Go compile error, next to the LSX code space.
// Output that carries no file positions collapses to one catch-all entry.
func goBuildDiagnostics(dir, stderr string) []compiler.Diagnostic {
	var diags []compiler.Diagnostic
	for _, line := range strings.Split(stderr, "\n") {
		m := goBuildLine.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		lineNo, _ := strconv.Atoi(m[2])
		col, _ := strconv.Atoi(m[3])
		file := m[1]
		if !filepath.IsAbs(file) {
			file = filepath.Join(dir, file)
		}
		diags = append(diags, compiler.Diagnostic{
			File: file, Line: lineNo, Col: col,
			Severity: compiler.SeverityError, Code: "GO001", Message: m[4],
		})
	}
	if len(diags) == 0 {
		diags = append(diags, compiler.Diagnostic{
			File: dir, Line: 1, Col: 1,
			Severity: compiler.SeverityError, Code: "GO001",
			Message: strings.TrimSpace(stderr),
		})
	}
	return diags
}

// devControl is the dev server's push channel into the running app: the app
// dials the URL (handed over via LIQUID_DEV_CONTROL) and holds the response
// open; each broadcast writes one JSON line per connection, which the app
// relays onto its /hydro-sse dev streams. The URL path is a random token so
// nothing else on localhost can feed the overlay.
type devControl struct {
	url    string
	server *http.Server
	ln     net.Listener

	mu    sync.Mutex
	conns map[chan string]struct{}
}

func newDevControl() (*devControl, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("starting dev control listener: %w", err)
	}
	token := make([]byte, 16)
	if _, err := rand.Read(token); err != nil {
		return nil, fmt.Errorf("minting control token: %w", err)
	}
	path := "/" + hex.EncodeToString(token)

	c := &devControl{
		url:   "http://" + ln.Addr().String() + path,
		ln:    ln,
		conns: map[chan string]struct{}{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc(path, c.serveStream)
	c.server = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := c.server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintln(os.Stderr, "liquid dev: control server:", err)
		}
	}()
	return c, nil
}

// serveStream holds one app connection open, relaying broadcast lines.
func (c *devControl) serveStream(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	lines := make(chan string, 16)
	c.mu.Lock()
	c.conns[lines] = struct{}{}
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.conns, lines)
		c.mu.Unlock()
	}()

	w.WriteHeader(http.StatusOK)
	fl.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case line := <-lines:
			if _, err := io.WriteString(w, line+"\n"); err != nil {
				return
			}
			fl.Flush()
		}
	}
}

// broadcastDiagnostics pushes a D13 diagnostics array to every connected
// app for the browser overlay.
func (c *devControl) broadcastDiagnostics(diags []compiler.Diagnostic) {
	if diags == nil {
		diags = []compiler.Diagnostic{}
	}
	payload, err := json.Marshal(map[string]any{"event": "diagnostics", "data": diags})
	if err != nil {
		fmt.Fprintln(os.Stderr, "liquid dev: encoding diagnostics frame:", err)
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for lines := range c.conns {
		select {
		case lines <- string(payload):
		default: // a stalled app connection never blocks the build loop
		}
	}
}

// close shuts the control server down.
func (c *devControl) close() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.server.Shutdown(ctx); err != nil {
		_ = c.server.Close()
	}
}
