package main

import "testing"

func TestResolveUseRealHome(t *testing.T) {
	tests := []struct {
		name          string
		toolName      string
		swapSupported bool
		realHome      string
		wantUsesReal  bool
	}{
		{"no tool: ephemeral", "", true, "/home/u", false},
		{"tool granted, swap supported: real", "wrangler", true, "/home/u", true},
		{"tool granted, swap unsupported (windows): ephemeral", "wrangler", false, "/home/u", false},
		{"tool granted, real home unresolvable: ephemeral", "wrangler", true, "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			usesReal := resolveUseRealHome(tc.toolName, tc.swapSupported, tc.realHome)
			if usesReal != tc.wantUsesReal {
				t.Errorf("resolveUseRealHome(%q, %v, %q) = %v, want %v",
					tc.toolName, tc.swapSupported, tc.realHome, usesReal, tc.wantUsesReal)
			}
		})
	}
}
