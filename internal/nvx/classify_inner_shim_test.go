package nvx

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// A package manager reached through corepack or through its own entry script is
// checked as that package manager, through runShim, which is the path a shim
// invocation takes.
//
// Both spellings were classified by the outer name. corepack was not a wrapped
// command at all and node is your own code, so `corepack pnpm add x` and `node
// npm-cli.js install x` ran the install uncontained and the blocklist never saw
// x. Isolation is off here and nothing is on PATH, so a regression fails to find
// the command and returns instead of installing anything.
func TestAPackageManagerBehindCorepackOrNodeIsVerified(t *testing.T) {
	origResolve := resolveNpmPackageDetailsForVerify
	resolveNpmPackageDetailsForVerify = func(pkgName, versionQuery string) (string, time.Time, bool, error) {
		return "1.0.0", time.Time{}, false, nil
	}
	t.Cleanup(func() { resolveNpmPackageDetailsForVerify = origResolve })

	home := tempDir(t)
	policy := `{"typosquatting":{"enabled":false},"blocked_packages":["left-pad"],"isolation":{"enabled":false}}`
	if err := os.WriteFile(filepath.Join(home, "policy.json"), []byte(policy), 0o600); err != nil {
		t.Fatal(err)
	}
	inProjectDir(t, tempDir(t))
	t.Setenv("PATH", tempDir(t))
	npmCli := filepath.Join(tempDir(t), "node_modules", "npm", "bin", "npm-cli.js")

	for _, tc := range []struct {
		cmd  string
		args []string
	}{
		{"corepack", []string{"pnpm", "add", "left-pad"}},
		{"corepack", []string{"yarn@1.22.22", "add", "left-pad"}},
		{"node", []string{npmCli, "install", "left-pad"}},
	} {
		var code int
		out := captureStderrHere(t, func() { code = runShim(tc.cmd, tc.args, home) })
		if code != exitRefused || !strings.Contains(out, "Blocked by security policy") {
			t.Errorf("`%s %v` exited %d without the blocklist refusing left-pad:\n%s", tc.cmd, tc.args, code, out)
		}
	}
}

// The same two spellings reach the containment decision. A global install
// through either is refused before anything runs, which can only happen when
// the run was going to be contained. The commands that always write outside the
// project say which flag runs them anyway.
func TestAPackageManagerBehindCorepackOrNodeIsContained(t *testing.T) {
	home := tempDir(t)
	inProjectDir(t, tempDir(t))
	t.Setenv("PATH", tempDir(t))
	prevQuiet := quietFlag
	quietFlag = false
	t.Cleanup(func() { quietFlag = prevQuiet })
	npmCli := filepath.Join(tempDir(t), "node_modules", "npm", "bin", "npm-cli.js")

	for _, tc := range []struct {
		cmd  string
		args []string
		says string
	}{
		{"corepack", []string{"pnpm", "add", "-g", "left-pad"}, "global installs"},
		{"node", []string{npmCli, "install", "-g", "left-pad"}, "global installs"},
		{"npm", []string{"link", "left-pad"}, "npm link"},
		{"pnpm", []string{"self-update"}, "pnpm self-update"},
		{"pnpm", []string{"env", "use", "--global", "20"}, "pnpm env use"},
	} {
		var code int
		out := captureStderrHere(t, func() { code = runShim(tc.cmd, tc.args, home) })
		if code != exitRefused {
			t.Errorf("`%s %v` exited %d, want the refusal %d; it was not contained:\n%s", tc.cmd, tc.args, code, exitRefused, out)
			continue
		}
		for _, want := range []string{tc.says, "--no-sandbox " + tc.cmd} {
			if !strings.Contains(out, want) {
				t.Errorf("`%s %v` refusal does not say %q:\n%s", tc.cmd, tc.args, want, out)
			}
		}
	}
}

// `bun pm trust <name>` runs that package's blocked scripts, so the name is
// checked. `bun pm trust --all` and `pnpm approve-builds` name nothing, and must
// not ask the registry about an empty or made-up name.
func TestScriptApprovalVerbsCheckOnlyTheNamesTheyAreGiven(t *testing.T) {
	inProjectDir(t, tempDir(t))
	for _, tc := range []struct {
		cmd  string
		args []string
		want []string
	}{
		{"bun", []string{"pm", "trust", "left-pad", "esbuild"}, []string{"left-pad", "esbuild"}},
		{"bun", []string{"pm", "trust", "--all"}, nil},
		{"pnpm", []string{"approve-builds"}, nil},
		{"npm", []string{"link", "left-pad"}, []string{"left-pad"}},
		{"npm", []string{"link"}, nil},
	} {
		if got := detectShimPackagesForVerification(tc.cmd, tc.args); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("`%s %v` checks %q, want %q", tc.cmd, tc.args, got, tc.want)
		}
	}
}
