package nvx

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Every external program nvx launches by a bare name is one of a named,
// explained set.
//
// A bare name is resolved through PATH, and PATH is not nvx's. The user's
// half of it is written by ordinary user-level code, the shell integration
// prepends directories to it on every cd, and `nvx setup` runs the same
// binary elevated. So `exec.Command("CheckNetIsolation", ...)` from setup was
// an Administrator running whatever a user-writable directory earlier in PATH
// chose to call CheckNetIsolation.exe. The same shape, unelevated, is how
// icacls, reg, cmd and powershell were reached.
//
// Windows system tools are now resolved under the system directory the
// kernel reports, never by name. What remains by name is listed here with the
// reason it cannot be anything else, and adding one is a decision with a
// reviewer attached rather than a habit.
func TestEveryProgramLaunchedByNameIsANamedException(t *testing.T) {
	allowed := map[string]string{
		"docker": "a user-installed tool with no fixed location; nvx runs it unelevated as the user, who could run it themselves",
		"ip":     "Linux iproute2 inside the sandbox's own network namespace, run as the user with no privilege to gain",
	}
	// The program is exec.Command's first argument and exec.CommandContext's
	// second. runWinCmd is not scanned: it resolves its name under the system
	// directory by contract, and TestAPlantedSystemToolOnPathIsNotTheOneRun
	// holds it to that.
	call := regexp.MustCompile(`\bexec\.(Command|CommandContext)\((.*)`)
	programArg := func(kind, rest string) string {
		args := strings.Split(rest, ",")
		idx := 0
		if kind == "CommandContext" {
			idx = 1
		}
		if idx >= len(args) {
			return ""
		}
		return strings.TrimSpace(args[idx])
	}

	files := goSourcesForAudit(t)
	seen := map[string]bool{}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for n, line := range strings.Split(string(src), "\n") {
			code := line
			if i := strings.Index(code, "//"); i >= 0 {
				code = code[:i]
			}
			m := call.FindStringSubmatch(code)
			if m == nil {
				continue
			}
			arg := programArg(m[1], m[2])
			// Only a string literal is a bare name. A variable or a call holds a
			// path something else chose, and the resolver is what chooses it.
			if len(arg) < 2 || arg[0] != '"' || !strings.HasSuffix(arg, `"`) {
				continue
			}
			name := strings.Trim(arg, `"`)
			if strings.ContainsAny(name, `\/`) {
				continue // a path, not a name for PATH to resolve
			}
			if _, ok := allowed[name]; !ok {
				t.Errorf("%s:%d launches %q by bare name, which PATH resolves:\n    %s\n"+
					"A Windows system tool must go through systemToolPath so it is taken from the system directory. "+
					"If this program genuinely has to be found on PATH, add it to this test's list WITH the reason.",
					file, n+1, name, strings.TrimSpace(line))
				continue
			}
			seen[name] = true
		}
	}
	for name := range allowed {
		if !seen[name] {
			t.Errorf("allowed program %q is no longer launched by name anywhere; remove it from the list so the list stays true", name)
		}
	}
}
