package main

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestDevLoopEndToEnd drives the whole D16 loop through the real CLI binary
// and toolchain: bootstrap build of a gen-less scaffold-shaped app, watch →
// rebuild → restart on an .lsx edit, and a build break pushed as a
// diagnostics frame on the app's own /hydro-sse dev stream.
func TestDevLoopEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns the Go toolchain repeatedly")
	}
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolving repo root: %v", err)
	}
	tmp := t.TempDir()
	copyExampleModuleList(t, repoRoot, tmp, []string{"go.mod", "go.sum", "core"})
	if err := os.CopyFS(filepath.Join(tmp, "devapp"), os.DirFS(filepath.Join(repoRoot, "cmd", "liquid", "testdata", "devapp"))); err != nil {
		t.Fatalf("copying fixture app: %v", err)
	}

	liquidBin := filepath.Join(tmp, "liquid-cli")
	build := exec.Command("go", "build", "-o", liquidBin, "./cmd/liquid")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the CLI: %v\n%s", err, out)
	}

	addr := freeAddr(t)
	dev := exec.Command(liquidBin, "dev", "devapp")
	dev.Dir = tmp
	dev.Env = append(os.Environ(), "DEVAPP_ADDR="+addr)
	var devLog strings.Builder
	dev.Stdout = &devLog
	dev.Stderr = &devLog
	if err := dev.Start(); err != nil {
		t.Fatalf("starting liquid dev: %v", err)
	}
	t.Cleanup(func() {
		_ = dev.Process.Signal(syscall.SIGTERM)
		done := make(chan struct{})
		go func() { _ = dev.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = dev.Process.Kill()
			<-done
		}
		t.Logf("liquid dev output:\n%s", devLog.String())
	})

	base := "http://" + addr

	// 1. Bootstrap: the fixture ships no *_gen.go; the first cycle must
	// compile templates, build with the liquiddev tag, and start the app.
	waitForBody(t, base+"/", "hello v1", 120*time.Second, "bootstrap build")
	if body := getBody(t, base+"/"); !strings.Contains(body, "/liquid/dev.js") {
		t.Errorf("dev-built app must inject the dev script, got:\n%s", body)
	}

	// 2. Watch → rebuild → restart: an .lsx edit shows up at the same URL.
	lsx := filepath.Join(tmp, "devapp", "ui", "hello.lsx")
	writeDevFile(t, lsx, "<p>hello v2</p>\n")
	waitForBody(t, base+"/", "hello v2", 120*time.Second, "edit-triggered rebuild")

	// 3. Build failure: the diagnostics ride the app's own SSE stream —
	// the running (v2) app stays up and relays the overlay frame.
	frames := openDevStream(t, base)
	writeDevFile(t, lsx, "<p>{{ Broken\n")
	waitForFrame(t, frames, "LSX001", 60*time.Second)
	if body := getBody(t, base+"/"); !strings.Contains(body, "hello v2") {
		t.Errorf("a failed build must leave the previous app serving, got:\n%s", body)
	}

	// 4. Fixing the template rebuilds and restarts into the new content.
	writeDevFile(t, lsx, "<p>hello v3</p>\n")
	waitForBody(t, base+"/", "hello v3", 120*time.Second, "recovery rebuild")
}

// copyExampleModuleList copies the named repo paths into dst, forming a
// loadable module slice.
func copyExampleModuleList(t *testing.T, repoRoot, dst string, paths []string) {
	t.Helper()
	for _, rel := range paths {
		src := filepath.Join(repoRoot, filepath.FromSlash(rel))
		info, err := os.Stat(src)
		if err != nil {
			t.Fatalf("stat %s: %v", src, err)
		}
		target := filepath.Join(dst, filepath.FromSlash(rel))
		if info.IsDir() {
			if copyErr := os.CopyFS(target, os.DirFS(src)); copyErr != nil {
				t.Fatalf("copying %s: %v", rel, copyErr)
			}
			continue
		}
		data, readErr := os.ReadFile(src)
		if readErr != nil {
			t.Fatalf("reading %s: %v", rel, readErr)
		}
		if writeErr := os.WriteFile(target, data, 0o644); writeErr != nil {
			t.Fatalf("writing %s: %v", rel, writeErr)
		}
	}
}

// freeAddr reserves an ephemeral localhost port and hands its address out
// for the fixture app to bind.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("picking a port: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

// getBody GETs url, returning "" on any failure — callers poll.
func getBody(t *testing.T, url string) string {
	t.Helper()
	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}
	return string(body)
}

// waitForBody polls url until its body contains want.
func waitForBody(t *testing.T, url, want string, timeout time.Duration, phase string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(getBody(t, url), want) {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("%s: %s never served %q", phase, url, want)
}

// openDevStream connects the ?dev=1 SSE stream and feeds its data lines to
// a channel until the stream drops.
func openDevStream(t *testing.T, base string) <-chan string {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, base+"/hydro-sse?dev=1", nil)
	if err != nil {
		t.Fatalf("building stream request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("opening dev stream: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		t.Fatalf("dev stream status = %d, want 200", resp.StatusCode)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	lines := make(chan string, 64)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			lines <- sc.Text()
		}
	}()
	return lines
}

// waitForFrame reads stream lines until one contains want.
func waitForFrame(t *testing.T, lines <-chan string, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	var seen []string
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatalf("dev stream closed before a frame containing %q; saw:\n%s", want, strings.Join(seen, "\n"))
			}
			seen = append(seen, line)
			if strings.Contains(line, want) {
				return
			}
		case <-deadline:
			t.Fatalf("no dev frame containing %q within %s; saw:\n%s", want, timeout, strings.Join(seen, "\n"))
		}
	}
}
