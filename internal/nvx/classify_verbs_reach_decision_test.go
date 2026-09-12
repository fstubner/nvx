package nvx

import "testing"

// The ad-hoc verbs reach the CONTAINMENT DECISION, not just the classifier.
//
// classify_untrusted_verbs_test.go covers `npm exec`, `npm create`, `pnpm dlx`
// and the rest -- through classifyInvocation() alone. The tests of
// shouldSandbox, which is what actually decides, covered only `npm install` and
// a bare `node`. So the classifier could stop reaching the decision for exactly
// these verbs and every test would stay green.
//
// That is not hypothetical. `npm exec cowsay` running UNCONTAINED is the bug
// this project already shipped once; it was caught by an acceptance pass rather
// than by the suite, and the fix was tested at the classifier where the defect
// was in the composition.
//
// Asserted through shouldSandbox so the classifier, the isolation level and the
// flags are judged together, and paired with the negative case: a test that only
// checked "contained" would pass against a decision that contains everything,
// which is its own way of being broken.
func TestAdHocVerbsReachTheContainmentDecision(t *testing.T) {
	policy := DefaultPolicy()

	contained := []struct {
		name string
		cmd  string
		args []string
	}{
		{"npm exec", "npm", []string{"exec", "cowsay", "hi"}},
		{"npm create", "npm", []string{"create", "vite@latest"}},
		{"npx", "npx", []string{"cowsay", "hi"}},
		{"pnpm dlx", "pnpm", []string{"dlx", "cowsay"}},
		{"yarn dlx", "yarn", []string{"dlx", "cowsay"}},
		{"bunx", "bunx", []string{"cowsay"}},
		{"npm install", "npm", []string{"install", "left-pad"}},
	}
	for _, tc := range contained {
		if !shouldSandbox(tc.cmd, tc.args, policy, shimOptions{}) {
			t.Errorf("%s runs UNCONTAINED at standard isolation. It executes code the project did not "+
				"write, which is the whole category the sandbox exists for -- and `npm exec cowsay` "+
				"escaping this way is a bug that has already shipped once.", tc.name)
		}
	}

	// The other half. Your own code is deliberately not contained at standard
	// isolation, so a decision that contained everything would satisfy the loop
	// above while breaking `npm run build` for everyone.
	yourCode := []struct {
		name string
		cmd  string
		args []string
	}{
		{"npm run", "npm", []string{"run", "build"}},
		{"bare node", "node", []string{"server.js"}},
	}
	for _, tc := range yourCode {
		if shouldSandbox(tc.cmd, tc.args, policy, shimOptions{}) {
			t.Errorf("%s was contained at standard isolation; containment covers code you did not write, "+
				"and widening it silently would break ordinary builds", tc.name)
		}
	}
}
