package main

import (
	"os"
	"path/filepath"
	"testing"
)

func benchNvxHome(b *testing.B) string {
	b.Helper()
	nvxHome := b.TempDir()
	if err := os.WriteFile(filepath.Join(nvxHome, "policy.json"), []byte(`{"isolation":{"enabled":true}}`), 0644); err != nil {
		b.Fatal(err)
	}
	return nvxHome
}

func BenchmarkLoadPolicy(b *testing.B) {
	nvxHome := benchNvxHome(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := LoadPolicy(nvxHome); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEnsureProjectPolicyTrust(b *testing.B) {
	nvxHome := benchNvxHome(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := ensureProjectPolicyTrust(nvxHome); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkGetActiveShellVersionFor(b *testing.B) {
	nvxHome := benchNvxHome(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = getActiveShellVersionFor(nvxHome, "node")
	}
}

func BenchmarkLookPathUncached(b *testing.B) {
	nvxHome := benchNvxHome(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = lookPathSkippingNvxShimsUncached("node", nvxHome)
	}
}

func BenchmarkLookPathCached(b *testing.B) {
	nvxHome := benchNvxHome(b)
	// Warm the cache once, then measure cache hits.
	_, _ = lookPathSkippingNvxShims("node", nvxHome)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = lookPathSkippingNvxShims("node", nvxHome)
	}
}
