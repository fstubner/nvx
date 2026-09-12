//go:build !race

package nvx

// probeChildIsInstrumented reports whether this test binary carries race
// instrumentation. Without -race the binary is already the cheap child the
// probes want, so it is copied as-is.
const probeChildIsInstrumented = false
