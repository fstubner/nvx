package nvx

import "testing"

// Every spelling of an install is contained, and checked before it runs.
//
// Each case below ran as your own code until 2026-09-26 -- no sandbox and no
// pre-install checks -- because the verb lists knew the main spelling and not
// the alias: npm's own aliases, and yarn's default command, which installs.
func TestInstallAliasesAreContained(t *testing.T) {
	for _, c := range [][]string{
		{"yarn"},
		{"yarn", "--frozen-lockfile"},
		{"npm", "clean-install"},
		{"npm", "ic"},
		{"npm", "install-clean"},
		{"npm", "isntall-clean"},
		{"npm", "it"},
		{"npm", "install-test"},
		{"npm", "cit"},
		{"npm", "sit"},
		{"npm", "install-ci-test"},
		{"npm", "clean-install-test"},
		{"npm", "isntal", "left-pad"},
		{"npm", "rb"},
		{"pnpm", "rb"},
		{"npm", "udpate"},
	} {
		if got := classifyInvocation(c[0], c[1:]); got != classInstall {
			t.Errorf("%v classified as %v, want an install", c, got)
		}
	}
	if got := classifyInvocation("npm", []string{"x", "cowsay"}); got != classAdHocTool {
		t.Errorf("npm x (npm exec) classified as %v, want an ad-hoc tool", got)
	}
}

// And the ones that install nothing stay your own code.
func TestNonInstallsStayYourCode(t *testing.T) {
	for _, c := range [][]string{
		{"yarn", "--version"},
		{"yarn", "-v"},
		{"yarn", "--help"},
		{"yarn", "run", "build"},
		{"yarn", "build"},
		{"npm", "test"},
		{"npm", "run", "it"},
	} {
		if got := classifyInvocation(c[0], c[1:]); got != classYourCode {
			t.Errorf("%v classified as %v, want your own code", c, got)
		}
	}
}

// Bare yarn and npm's clean-install spellings are verified against the
// lockfile like `npm ci`, not only contained.
func TestCleanInstallSpellingsAreVerified(t *testing.T) {
	for _, c := range [][]string{{"yarn"}, {"npm", "clean-install"}, {"npm", "ic"}} {
		if !hasInstallVerb(c[1:], ciVerbs...) && !isBareYarnInstall(c[0], c[1:]) {
			t.Errorf("%v would not reach the lockfile verification", c)
		}
	}
}
