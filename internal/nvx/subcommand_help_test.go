package nvx

import (
	"strings"
	"testing"
)

// `nvx policy init --help` and its siblings printed "Unknown option" and exited 1,
// although `--help` after any command is documented to print that command's help.
// The top-level dispatcher only looks at the argument right after the command, so
// the subcommands, which parse their own flags, have to answer it themselves.
func TestSubcommandHelpFlagsPrintHelpAndExitZero(t *testing.T) {
	home := tempDir(t)
	inProjectDir(t, tempDir(t))

	cases := []struct {
		name string
		run  func(args []string) int
		want string
	}{
		{"policy init", func(a []string) int { return runPolicyInit(a, home) }, "nvx policy init"},
		{"policy check", func(a []string) int { return runPolicyCheck(a, home) }, "nvx policy check"},
		{"policy explain", func(a []string) int { return runPolicyExplain(a, home) }, "nvx policy explain"},
		{"audit export", func(a []string) int { return runAuditExport(a, home) }, "nvx audit export"},
	}
	for _, tc := range cases {
		for _, flag := range []string{"--help", "-h"} {
			t.Run(tc.name+" "+flag, func(t *testing.T) {
				var code int
				out := captureStdout(t, func() { code = tc.run([]string{flag}) })
				if code != 0 {
					t.Errorf("exit code = %d, want 0", code)
				}
				if !strings.Contains(out, tc.want) {
					t.Errorf("stdout = %q, want it to contain %q", out, tc.want)
				}
			})
		}
	}
}

// With no findings the JSON said `"findings": null`, which a consumer iterating
// the array has to special-case. An empty list is the same fact in the type the
// field always has.
func TestPolicyCheckJSONPrintsAnEmptyFindingsListNotNull(t *testing.T) {
	home := tempDir(t)
	writePolicyFixture(t, home, "policy.json", `{"isolation": {"enabled": false}}`)
	project := tempDir(t)
	writePolicyFixture(t, project, "package.json", `{"dependencies": {"react": "^18.0.0"}}`)
	inProjectDir(t, project)

	var code int
	out := captureStdout(t, func() { code = runPolicyCheck([]string{"--format=json"}, home) })
	if code != exitPolicyPass {
		t.Fatalf("exit code = %d, want a pass; output: %s", code, out)
	}
	if strings.Contains(out, `"findings": null`) || !strings.Contains(out, `"findings": []`) {
		t.Fatalf("output has no empty findings array:\n%s", out)
	}
}
