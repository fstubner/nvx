package main

import "testing"

func TestTrustedToolCandidate(t *testing.T) {
	tests := []struct {
		name          string
		cmd           string
		args          []string
		wantTool      string
		wantWantsHome bool
	}{
		{"npx wrangler login", "npx", []string{"wrangler", "login"}, "wrangler", true},
		{"npx gh auth", "npx", []string{"gh", "auth"}, "gh", true},
		{"bunx aws configure", "bunx", []string{"aws", "configure"}, "aws", true},
		{"npx wrangler deploy (not auth-shaped)", "npx", []string{"wrangler", "deploy"}, "wrangler", false},
		{"npx cowsay (no subcommand)", "npx", []string{"cowsay", "hi"}, "cowsay", false},
		{"npx with version pin", "npx", []string{"wrangler@3", "login"}, "wrangler", true},
		{"npx scoped package", "npx", []string{"@cloudflare/wrangler@2", "login"}, "@cloudflare/wrangler", true},
		{"npx with leading flag", "npx", []string{"-y", "wrangler", "login"}, "wrangler", true},
		{"npm run (not an executor command)", "npm", []string{"run", "login"}, "", false},
		{"node direct (not an executor command)", "node", []string{"login.js"}, "", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotTool, gotWantsHome := trustedToolCandidate(tc.cmd, tc.args)
			if gotTool != tc.wantTool || gotWantsHome != tc.wantWantsHome {
				t.Errorf("trustedToolCandidate(%q, %v) = (%q, %v), want (%q, %v)",
					tc.cmd, tc.args, gotTool, gotWantsHome, tc.wantTool, tc.wantWantsHome)
			}
		})
	}
}

func TestStripVersionSuffix(t *testing.T) {
	tests := []struct{ in, want string }{
		{"wrangler", "wrangler"},
		{"wrangler@3", "wrangler"},
		{"wrangler@3.1.0", "wrangler"},
		{"@cloudflare/wrangler", "@cloudflare/wrangler"},
		{"@cloudflare/wrangler@2", "@cloudflare/wrangler"},
		{"", ""},
	}
	for _, tc := range tests {
		if got := stripVersionSuffix(tc.in); got != tc.want {
			t.Errorf("stripVersionSuffix(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
