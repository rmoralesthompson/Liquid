//go:build !liquiddev

package liquid

// This file is the production counterpart of determinism_on.go (D28). It
// deliberately defines nothing: WithDeterminism and the deterministic token
// source exist only under the liquiddev build tag, so a normal `go build`
// cannot name them. Combined with the App's token/clock seams having no
// exported mutator, that leaves no compiled path to replace the CSPRNG
// (crypto/rand.Reader) or the real clock in a production binary — the D15
// invariant. determinism_off_test.go pins this behaviorally, the way
// dev_off_test.go pins the absent dev surface.
