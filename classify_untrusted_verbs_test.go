package main

import "testing"

// A family of commands that fetch or execute package-authored code was
// classified as "your own code" and ran with no sandbox, no OSV scan and no
// typosquat check. Found by an independent acceptance pass on 2026-08-24,
// measured against the shipped binary:
//
//	nvx npx cowsay hi        -> contained
//	nvx npm exec cowsay hi   -> "Running directly (not sandboxed)", and it
//	                            fetched cowsay from the registry to do it
//
// Those two are the same operation. Only the `npx` spelling was recognised.
//
// README claimed the opposite in four places, SECURITY.md in one, and no
// Known-limitations entry mentioned it -- a grep for "npm exec", "dlx" and
// "bun x" across all five documents returned nothing. Nor did any test cover it:
// classify_test.go enumerated npx/bunx/uvx/pyx for classAdHocTool and stopped,
// so the gap was invisible from both directions.
//
// The worst case is the one README leads with. Under --agent-mode the single
// runtime signal ("Running directly (not sandboxed)") is suppressed, so an agent
// running `npm audit fix` -- the command a human runs *because of* a security
// advisory -- got silence.

func TestFetchAndRunVerbsAreTreatedAsAdHocTools(t *testing.T) {
	for _, tc := range []struct {
		name string
		cmd  string
		args []string
	}{
		{"npm exec", "npm", []string{"exec", "cowsay", "hi"}},
		{"npm create", "npm", []string{"create", "vite@latest"}},
		{"npm init with an initializer", "npm", []string{"init", "vite"}},
		{"pnpm dlx", "pnpm", []string{"dlx", "cowsay"}},
		{"yarn dlx", "yarn", []string{"dlx", "cowsay"}},
		{"bun x", "bun", []string{"x", "cowsay"}},
		{"bun create", "bun", []string{"create", "vite"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyInvocation(tc.cmd, tc.args); got != classAdHocTool {
				t.Errorf("`%s %v` classified as %v; it fetches and runs a package that is not in the "+
					"project, which is exactly what npx does and what the sandbox exists for",
					tc.cmd, tc.args, got)
			}
		})
	}
}

func TestVerbsThatRerunInstallScriptsAreTreatedAsInstalls(t *testing.T) {
	for _, tc := range []struct {
		name string
		cmd  string
		args []string
	}{
		{"npm update", "npm", []string{"update"}},
		{"npm up", "npm", []string{"up"}},
		{"npm rebuild", "npm", []string{"rebuild"}},
		{"npm dedupe", "npm", []string{"dedupe"}},
		{"npm audit fix", "npm", []string{"audit", "fix"}},
		{"yarn upgrade", "yarn", []string{"upgrade"}},
		{"pnpm update", "pnpm", []string{"update"}},
		{"bun update", "bun", []string{"update"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyInvocation(tc.cmd, tc.args); got != classInstall {
				t.Errorf("`%s %v` classified as %v; it re-runs dependency install scripts or fetches "+
					"new versions, which is package-authored code executing", tc.cmd, tc.args, got)
			}
		})
	}
}

// The other direction matters as much. Over-containing a command that runs the
// developer's own code makes nvx unusable, and "contain everything" would pass
// the tests above while being a different product.
func TestOrdinaryCommandsAreStillYourOwnCode(t *testing.T) {
	for _, tc := range []struct {
		name string
		cmd  string
		args []string
	}{
		{"npm run build", "npm", []string{"run", "build"}},
		{"npm test", "npm", []string{"test"}},
		{"npm audit without fix", "npm", []string{"audit"}},
		{"bare npm init", "npm", []string{"init"}},
		{"npm publish", "npm", []string{"publish"}},
		{"npm whoami", "npm", []string{"whoami"}},
		{"node app.js", "node", []string{"app.js"}},
		// After "--" the words belong to the script, so a matching verb there is
		// not nvx's to interpret.
		{"npm run build -- create", "npm", []string{"run", "build", "--", "create"}},
		{"npm run x -- update", "npm", []string{"run", "x", "--", "update"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyInvocation(tc.cmd, tc.args); got != classYourCode {
				t.Errorf("`%s %v` classified as %v; over-containing your own code is how a security "+
					"tool becomes one people turn off", tc.cmd, tc.args, got)
			}
		})
	}
}
