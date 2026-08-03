package main

import (
	"encoding/hex"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// These pin the dev control surface's entire auth story (#34,
// THREAT-MODEL.md boundary 3): the listener must bind loopback only, and
// the random URL token must be the price of feeding the overlay — a
// localhost request without it learns nothing.

func TestDevControlBindsLoopbackWithARandomTokenPath(t *testing.T) {
	c, err := newDevControl()
	if err != nil {
		t.Fatalf("newDevControl: %v", err)
	}
	t.Cleanup(c.close)

	host, _, err := net.SplitHostPort(c.ln.Addr().String())
	if err != nil {
		t.Fatalf("splitting listener address %q: %v", c.ln.Addr(), err)
	}
	if host != "127.0.0.1" {
		t.Errorf("control listener bound %q, want 127.0.0.1 — the control surface must never leave loopback", host)
	}

	u, err := url.Parse(c.url)
	if err != nil {
		t.Fatalf("parsing control URL %q: %v", c.url, err)
	}
	// 16 crypto/rand bytes hex-encoded: a 32-char hex token. Randomness
	// itself is untestable, but a second control minting the same token
	// would mean the token is not minted per process at all.
	token := strings.TrimPrefix(u.Path, "/")
	if len(token) != 32 {
		t.Errorf("control token %q is %d chars, want 32 (16 random bytes, hex)", token, len(token))
	}
	if _, decodeErr := hex.DecodeString(token); decodeErr != nil {
		t.Errorf("control token %q is not hex: %v", token, decodeErr)
	}

	other, err := newDevControl()
	if err != nil {
		t.Fatalf("newDevControl (second): %v", err)
	}
	t.Cleanup(other.close)
	ou, err := url.Parse(other.url)
	if err != nil {
		t.Fatalf("parsing second control URL %q: %v", other.url, err)
	}
	if ou.Path == u.Path {
		t.Error("two dev controls minted the same token — the token is not random per mint")
	}
}

func TestDevControlRejectsRequestsWithoutTheToken(t *testing.T) {
	c, err := newDevControl()
	if err != nil {
		t.Fatalf("newDevControl: %v", err)
	}
	t.Cleanup(c.close)

	u, err := url.Parse(c.url)
	if err != nil {
		t.Fatalf("parsing control URL %q: %v", c.url, err)
	}
	base := "http://" + u.Host

	for _, path := range []string{"/", "/wrong", "/" + strings.Repeat("0", 32)} {
		resp, getErr := http.Get(base + path)
		if getErr != nil {
			t.Fatalf("GET %s: %v", path, getErr)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s = %d, want %d — only the minted token path exists", path, resp.StatusCode, http.StatusNotFound)
		}
	}

	// The minted path is the one door: it answers 200 and holds the stream
	// open (closing the body is the disconnect).
	resp, err := http.Get(c.url)
	if err != nil {
		t.Fatalf("GET control URL: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET minted control path = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}
