package nvx

import (
	"reflect"
	"testing"
)

// A verb placed after "--" is still the verb.
//
// Every scan in nvx stopped at "--", on the reasoning that the end-of-options
// separator ends everything nvx has an interest in. That is true of nvx's own
// flags and wrong for the package manager's command: npm takes its command from
// the first positional token, and "--" only ends FLAG parsing, so a verb right
// after it is the command exactly as it would be without it.
//
// Measured against npm 11 on 2026-09-06, from a scratch directory:
//
//	npm -- view left-pad version          -> 1.3.0
//	npm --dry-run -- install left-pad@1.3.0 -> "add left-pad 1.3.0"
//	npm --dry-run install -- left-pad@1.3.0 -> "added 1 package"
//	npm --dry-run install -- --weird-name -> "No matching version found for undefined@--weird-name"
//	npm test -- install                    -> the test script ran with argv ["install"]
//
// So `nvx npm -- install evil` installed evil, uncontained, with no typosquat,
// OSV or release-age check: the scan returned "no verb" at the "--" and every
// decision downstream took the your-own-code branch. The last line is the
// other half of the rule and the reason this cannot be "ignore -- entirely":
// after a script-running verb, whatever follows belongs to the script.
func TestAVerbAfterTheSeparatorIsStillTheVerb(t *testing.T) {
	for _, tc := range []struct {
		name string
		cmd  string
		args []string
		want invocationClass
	}{
		// The bypass.
		{"npm -- install", "npm", []string{"--", "install", "evil"}, classInstall},
		{"npm -- i", "npm", []string{"--", "i", "evil"}, classInstall},
		{"yarn -- add", "yarn", []string{"--", "add", "evil"}, classInstall},
		{"pnpm -- install", "pnpm", []string{"--", "install", "evil"}, classInstall},
		{"bun -- install", "bun", []string{"--", "install"}, classInstall},
		{"npm flag then -- then install", "npm", []string{"--loglevel", "verbose", "--", "install", "evil"}, classInstall},
		{"npm -- exec", "npm", []string{"--", "exec", "evil"}, classAdHocTool},
		{"npm -- create", "npm", []string{"--", "create", "evil-app"}, classAdHocTool},
		{"npm -- update", "npm", []string{"--", "update"}, classInstall},
		{"npm -- audit fix", "npm", []string{"--", "audit", "fix"}, classInstall},

		// The other half: after a script-running verb, "--" hands everything to
		// the script, and a word there is the script's argument. Measured: `npm
		// test -- install` ran the test script with argv ["install"].
		{"npm test -- install", "npm", []string{"test", "--", "install"}, classYourCode},
		{"npm start -- add", "npm", []string{"start", "--", "add", "item"}, classYourCode},
		{"npm run build -- create", "npm", []string{"run", "build", "--", "create"}, classYourCode},
		{"bun run dev -- install", "bun", []string{"run", "dev", "--", "install"}, classYourCode},

		// A "--" after a non-script command ends nvx's interest too: what
		// follows belongs to that command.
		{"npm view -- install", "npm", []string{"view", "--", "install"}, classYourCode},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyInvocation(tc.cmd, tc.args); got != tc.want {
				t.Errorf("classifyInvocation(%s %v) = %v, want %v", tc.cmd, tc.args, got, tc.want)
			}
		})
	}
}

// The packages named after the verb reach the checks, on both sides of a "--".
//
// installPackagesArg stopped at "--" after the verb, so `npm install -- evil`
// was contained (the verb came first) but verified nothing: the typosquat, OSV
// and release-age checks received an empty list. After the verb, "--" makes
// every remaining token a package spec, including ones that start with a
// dash -- measured: `npm install -- --weird-name` tried to resolve a package
// of that name.
func TestPackagesAfterTheSeparatorReachTheChecks(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{"verb after --", []string{"--", "install", "evil"}, []string{"evil"}},
		{"-- after verb", []string{"install", "--", "evil"}, []string{"evil"}},
		{"dash-prefixed spec after --", []string{"install", "--", "evil", "--weird-name"}, []string{"evil", "--weird-name"}},
		{"only tokens after the verb, and a flag there is a flag", []string{"--loglevel", "verbose", "install", "-D", "evil"}, []string{"evil"}},
		{"script arguments are not packages", []string{"test", "--", "install"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := detectInstallPackages(tc.args)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("detectInstallPackages(%v) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}

// A global-install flag after "--" is a package spec, not a flag, so it does
// not make the install global. This pins what npm does rather than what a
// reader might assume; isGlobalInstall already behaved this way and the point
// is that it keeps doing so once the verb scan looks past the separator.
func TestAGlobalFlagAfterTheSeparatorIsNotAFlag(t *testing.T) {
	if isGlobalInstall("npm", []string{"--", "install", "-g", "evil"}) {
		t.Error("`npm -- install -g evil` was read as a global install; after the separator -g is a package spec")
	}
	if isGlobalInstall("npm", []string{"install", "--", "-g"}) {
		t.Error("`npm install -- -g` was read as a global install; after the separator -g is a package spec")
	}
	if !isGlobalInstall("npm", []string{"install", "-g", "evil"}) {
		t.Error("`npm install -g evil` is a global install")
	}
}

// And the decision itself: the separator does not talk an install out of the
// sandbox at the standard level.
func TestTheSeparatorDoesNotTalkAnInstallOutOfTheSandbox(t *testing.T) {
	policy := DefaultPolicy()
	normalizePolicy(&policy)
	for _, tc := range []struct {
		name string
		cmd  string
		args []string
		want bool
	}{
		{"npm -- install", "npm", []string{"--", "install", "evil"}, true},
		{"npm -- exec", "npm", []string{"--", "exec", "evil"}, true},
		{"npm test -- install stays yours", "npm", []string{"test", "--", "install"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := shouldSandbox(tc.cmd, tc.args, policy, parseShimOptions(tc.args))
			if got != tc.want {
				t.Errorf("shouldSandbox(%s %v) = %v, want %v", tc.cmd, tc.args, got, tc.want)
			}
		})
	}
}
