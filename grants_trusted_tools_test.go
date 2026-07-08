package main

import (
	"os"
	"path/filepath"
	"testing"
)

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

func TestEnsureTrustedToolGrantReturnsTrueWhenAlreadyGranted(t *testing.T) {
	tmp := t.TempDir()
	projectDir := filepath.Join(tmp, "project")
	nvxHome := filepath.Join(tmp, ".nvx")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(nvxHome, 0755); err != nil {
		t.Fatal(err)
	}

	origWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origWd) }()
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}

	scope := projectScopeDir()
	g := loadProjectGrants(nvxHome, scope)
	g.TrustedTools = append(g.TrustedTools, "wrangler")
	g.ProjectPath = scope
	if err := saveProjectGrants(nvxHome, g); err != nil {
		t.Fatal(err)
	}

	// Already granted: must return true WITHOUT prompting (no TTY available in
	// `go test`, so a prompt attempt would deny and this assertion would catch it).
	if !ensureTrustedToolGrant(nvxHome, "wrangler") {
		t.Fatal("expected true for an already-granted tool")
	}
}

func TestEnsureTrustedToolGrantEmptyToolName(t *testing.T) {
	if ensureTrustedToolGrant(t.TempDir(), "") {
		t.Fatal("empty tool name must never be granted")
	}
}