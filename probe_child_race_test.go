//go:build race

package main

// probeChildIsInstrumented reports whether this test binary carries race
// instrumentation, which is the only case where the contained probe child has to
// be rebuilt rather than copied. See probe_child_test.go for why.
const probeChildIsInstrumented = true
