package main

import (
	"os"
	"testing"
)

// TestMain exists to drop the uninstrumented probe child built for a -race run.
//
// It is deliberately the only thing here. Tests in this package re-run the test
// binary as a contained child, so anything done before m.Run() runs again inside
// every AppContainer probe -- which is how a global setup step becomes a
// per-probe one nobody meant to write.
func TestMain(m *testing.M) {
	code := m.Run()
	cleanupProbeChildBinary()
	os.Exit(code)
}
