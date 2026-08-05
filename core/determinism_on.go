//go:build liquiddev

package liquid

import (
	"crypto/sha256"
	"encoding/binary"
	"io"
	"time"
)

// This file is the opt-in half of D28's deterministic render mode. Like the
// dev surface (dev_on.go/dev_off.go) it compiles only under the liquiddev
// build tag, which `liquid dev` and the test/CI commands set. A production
// `go build` contains none of it: determinism_off.go documents the absence,
// and there is no other way to reach the App's token/clock seams, so the
// CSPRNG invariant (D15) and the real clock are provably intact in prod.

// determinismEpoch is the frozen instant a deterministic build's clock
// reports. Any wall-clock-derived framework output (CSRF token expiry) pins
// to it. A component that calls time.Now() itself is out of scope — pinning
// application time is the author's responsibility (D28 non-goal).
var determinismEpoch = time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)

// WithDeterminism pins the App's two non-deterministic framework seams — the
// opaque-token CSPRNG source and the clock — to fixed values, so a fresh App
// renders a given component byte-identically on every run (D28). It is the
// supported way for snapshot assertions (#D27), agent self-verification
// (D23), and diffable output to get reproducible tokens without white-box
// access.
//
// It exists only under the liquiddev build tag: a production build cannot
// name it, so no override knob reaches prod (D28 non-goal) and the D15
// CSPRNG/real-clock invariants stand. Each application seeds a freshly reset
// source, so two independently constructed deterministic Apps agree — not one
// App rendering twice, which still hands distinct hydro IDs to sibling
// components on a page.
func WithDeterminism() Option {
	return func(a *App) {
		a.now = func() time.Time { return determinismEpoch }
		a.rand = newDeterministicSource(0xD28)
	}
}

// deterministicSource is a reproducible io.Reader: SHA-256 in counter mode
// over a fixed seed. It stands in for crypto/rand.Reader only under
// WithDeterminism, and only in a liquiddev build — never a package global, so
// it cannot leak into a production token path. It is not a CSPRNG and is not
// safe for real tokens; that is the whole point of gating it out of prod.
type deterministicSource struct {
	seed  uint64
	ctr   uint64
	block []byte // buffered keystream not yet returned
}

// newDeterministicSource returns a deterministic byte source seeded from seed.
func newDeterministicSource(seed uint64) *deterministicSource {
	return &deterministicSource{seed: seed}
}

// Read fills p with the next reproducible keystream bytes, refilling from the
// next counter block as needed. It never errors and always fills p fully, so
// io.ReadFull over it never short-reads.
func (d *deterministicSource) Read(p []byte) (int, error) {
	for i := range p {
		if len(d.block) == 0 {
			d.block = d.nextBlock()
		}
		p[i] = d.block[0]
		d.block = d.block[1:]
	}
	return len(p), nil
}

// nextBlock hashes seed:counter into the next 32-byte keystream block.
func (d *deterministicSource) nextBlock() []byte {
	var in [16]byte
	binary.BigEndian.PutUint64(in[0:8], d.seed)
	binary.BigEndian.PutUint64(in[8:16], d.ctr)
	d.ctr++
	sum := sha256.Sum256(in[:])
	return sum[:]
}

// ensure the source satisfies the seam's contract at compile time.
var _ io.Reader = (*deterministicSource)(nil)
