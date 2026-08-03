//go:build !liquiddev

package liquid_test

// devBuild mirrors the internal devMode constant for seam tests whose
// expectations differ across D18's dev/prod split.
const devBuild = false
