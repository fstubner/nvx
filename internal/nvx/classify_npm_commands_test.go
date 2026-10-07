package nvx

import (
	"bufio"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// npm reads any unambiguous prefix of a command, and camelCase, as that
// command. Each spelling below ran as your own code until 2026-10-07, with no
// sandbox and no pre-install checks. Measured that day in a Linux container
// with npm 11.19.0: `nvx npm exe --yes --package=cowsay -c "cat ~/canary.txt"`
// printed the canary, while `npm exec` was contained, and
// `nvx npm installTest left-pad` ran directly.
func TestNpmAbbreviationsAreContainedAndChecked(t *testing.T) {
	inProjectDir(t, tempDir(t))
	const (
		install = classInstall
		adhoc   = classAdHocTool
		yours   = classYourCode
	)
	cases := []struct {
		line   string
		want   invocationClass
		checks []string
	}{
		{"npm exe --yes --package=cowsay -c cat", adhoc, []string{"cowsay"}},
		{"npm cre vite", adhoc, []string{"create-vite"}},
		{"npm crea vite", adhoc, []string{"create-vite"}},
		{"npm inn vite", adhoc, []string{"create-vite"}},
		{"npm upd left-pad", install, []string{"left-pad"}},
		{"npm upg left-pad", install, []string{"left-pad"}},
		{"npm u left-pad", install, []string{"left-pad"}}, // npm 11.13 and later
		{"npm reb", install, nil},
		{"npm ded", install, nil},
		{"npm installTest left-pad", install, []string{"left-pad"}},
		{"npm install-t left-pad", install, []string{"left-pad"}},
		{"npm install-ci", install, nil},
		{"npm cleanInstall", install, nil},
		{"npm install-cl", install, nil},
		{"npm pru", install, nil},
		{"npm uni left-pad", install, nil},
		{"npm rem left-pad", install, nil},
		{"npm ad left-pad", install, []string{"left-pad"}}, // npm 12, where adduser is gone
		{"npm --silent upd left-pad", install, []string{"left-pad"}},
		{"npm -- exe cowsay", adhoc, []string{"cowsay"}},
		{"npm cac add github:user/repo", adhoc, []string{"github:user/repo"}},
		// npm refuses a command it does not know, and nvx contains it.
		{"npm frobnicate", classUnknownCommand, nil},
		{"npm --foo frob", classUnknownCommand, nil},

		// These keep their treatment.
		{"npm run build", yours, nil},
		{"npm run upd", yours, nil}, // a script called upd
		{"npm --silent run update", yours, nil},
		{"npm rum build", yours, nil},
		{"npm test", yours, nil},
		{"npm t", yours, nil},
		{"npm test -- install", yours, nil},
		{"npm view left-pad version", yours, nil},
		{"npm vie left-pad", yours, nil},
		{"npm init", yours, nil},
		{"npm", yours, nil},
		{"npm --version", yours, nil},
		{"npm help", yours, nil},
		{"npm -C t update", install, nil}, // -C is npm's --prefix
	}
	policy := DefaultPolicy()
	for _, tc := range cases {
		f := strings.Fields(tc.line)
		if got := classifyInvocation(f[0], f[1:]); got != tc.want {
			t.Errorf("`%s` classified as %v, want %v", tc.line, got, tc.want)
		}
		if contained := shouldSandbox(f[0], f[1:], policy, shimOptions{}); contained != (tc.want != yours) {
			t.Errorf("`%s` contained=%v, want %v", tc.line, contained, tc.want != yours)
		}
		if got := detectShimPackagesForVerification(f[0], f[1:]); !reflect.DeepEqual(got, tc.checks) {
			t.Errorf("`%s` checks %q, want %q", tc.line, got, tc.checks)
		}
	}
}

// Every token any npm release resolves, with what npm's own deref returned for
// it, recorded in testdata/npm_command_names.txt from 81 releases' source.
// nvx resolves each the same way, and contains it unless every command it
// names is one nvx runs as your own code.
func TestNpmCommandNamesMatchNpm(t *testing.T) {
	f, err := os.Open(filepath.Join("testdata", "npm_command_names.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	inProjectDir(t, tempDir(t))
	n := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		tok, list, _ := strings.Cut(line, " ")
		var want []string
		if list != "-" {
			want = strings.Split(list, ",")
		}
		if got := npmCommandNames(tok); !reflect.DeepEqual(got, want) {
			t.Errorf("npm %s resolves to %q in nvx, and to %q in npm", tok, got, want)
		}
		contain := len(want) == 0
		for _, c := range want {
			contain = contain || !npmSafeCommands[c]
		}
		if got := classifyInvocation("npm", []string{tok}) != classYourCode; got != contain {
			t.Errorf("`npm %s` (npm reads %q) contained=%v, want %v", tok, want, got, contain)
		}
		n++
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if n < 500 {
		t.Fatalf("read %d tokens, want the whole file", n)
	}
}

// Every command in nvx's tables is either one it runs as your own code or one
// its classifier contains, so a command added to the tables is never left out
// of both.
func TestEveryNpmCommandIsSafeOrContained(t *testing.T) {
	seen := map[string]bool{}
	for _, l := range npmCmdLists() {
		for c := range l.commands {
			seen[c] = true
		}
		for c := range l.plumbing {
			seen[c] = true
		}
	}
	for c := range seen {
		contained := classifyInvocation("npm", []string{c}) != classYourCode
		if npmSafeCommands[c] == contained {
			t.Errorf("npm %s: listed as safe %v, contained %v", c, npmSafeCommands[c], contained)
		}
	}
}

// pnpm, yarn and bun aliases, and commands that run another command, that ran
// as your own code until 2026-10-07. Read from pnpm 8.15 to 12.9, yarn 1.22.22
// and bun 1.4.2.
func TestOtherPackageManagerAliasesAreContained(t *testing.T) {
	inProjectDir(t, tempDir(t))
	cases := []struct {
		line   string
		want   invocationClass
		checks []string
	}{
		{"pnpm uni left-pad", classInstall, nil},
		{"pnpm dislink left-pad", classInstall, nil},
		{"pnpm edit left-pad", classInstall, nil},
		{"pnpm m rm left-pad", classInstall, nil},
		{"pnpm multi remove left-pad", classInstall, nil},
		{"pnpm recursive uninstall left-pad", classInstall, nil},
		{"pnpm with 10 add left-pad", classInstall, []string{"left-pad"}},
		{"pnpm with current dlx cowsay", classAdHocTool, []string{"cowsay"}},
		{"pnpm runtime set node 22", classInstall, nil},
		{"pnpm rt set node 22", classInstall, nil},
		{"yarn upgradeInteractive", classInstall, nil},
		{"yarn workspace web remove left-pad", classInstall, nil},
		{"yarn workspace web add left-pad", classInstall, []string{"left-pad"}},
		{"yarn workspaces foreach -A unplug left-pad", classInstall, nil},
		{"bun r left-pad", classInstall, nil},
		{"bun uninstall left-pad", classInstall, nil},
		{"bun ci", classInstall, nil},

		{"pnpm m run build", classYourCode, nil},
		{"pnpm with 10 run build", classYourCode, nil},
		{"pnpm runtime list", classYourCode, nil},
		{"yarn workspace web run build", classYourCode, nil},
		{"yarn workspaces foreach -A run build", classYourCode, nil},
		{"yarn workspaces list", classYourCode, nil},
		{"bun run ci", classYourCode, nil},
	}
	for _, tc := range cases {
		f := strings.Fields(tc.line)
		if got := classifyInvocation(f[0], f[1:]); got != tc.want {
			t.Errorf("`%s` classified as %v, want %v", tc.line, got, tc.want)
		}
		if got := detectShimPackagesForVerification(f[0], f[1:]); !reflect.DeepEqual(got, tc.checks) {
			t.Errorf("`%s` checks %q, want %q", tc.line, got, tc.checks)
		}
	}
}
