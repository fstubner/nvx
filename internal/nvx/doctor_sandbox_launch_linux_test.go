//go:build linux

package nvx

import (
	"errors"
	"fmt"
	"strings"
	"syscall"
	"testing"
)

// Before this, doctor's sandbox check on Linux was `return true`, so a host whose
// kernel refused the sandbox its namespaces got a clean bill of health. A failed
// control launch must now cost the verdict.
func TestDoctorReportsALinuxSandboxThatCannotStart(t *testing.T) {
	restore := linuxControlLaunchFn
	t.Cleanup(func() { linuxControlLaunchFn = restore })

	linuxControlLaunchFn = func(string, string) (string, error) { return "", nil }
	var ok bool
	out := captureStdout(t, func() { ok = reportSandboxLaunch(tempDir(t)) })
	if !ok || !strings.Contains(out, "the sandbox starts") {
		t.Fatalf("a launch that worked was reported as ok=%v: %q", ok, out)
	}

	linuxControlLaunchFn = func(string, string) (string, error) {
		return "[ERROR] Landlock isolation failed: landlock is not available\n", errors.New("exit status 1")
	}
	out = captureStdout(t, func() { ok = reportSandboxLaunch(tempDir(t)) })
	if ok {
		t.Fatal("a launch that failed was reported as healthy")
	}
	if !strings.Contains(out, "Landlock isolation failed") {
		t.Errorf("the supervisor's own explanation is missing from the report: %q", out)
	}
	if strings.Contains(out, doctorSandboxApparmorSysctl) {
		t.Errorf("a Landlock failure was blamed on AppArmor: %q", out)
	}
}

// The Ubuntu 23.10+ case, as measured on WSL Ubuntu 24.04: the namespaces are
// created, and loopback setup inside them is refused. Doctor names the sysctl,
// gives both ways forward, and says whether network.mode open starts, by trying
// it rather than assuming.
func TestDoctorNamesTheAppArmorSysctlWhenNamespacesAreRefused(t *testing.T) {
	restore := linuxControlLaunchFn
	t.Cleanup(func() { linuxControlLaunchFn = restore })

	refusal := "[ERROR] Network isolation failed (fail-closed): bring up loopback (install iproute2): exit status 2: RTNETLINK answers: Operation not permitted\n"
	for _, openStarts := range []bool{true, false} {
		var modes []string
		linuxControlLaunchFn = func(_, mode string) (string, error) {
			modes = append(modes, mode)
			if mode == "open" && openStarts {
				return "", nil
			}
			return refusal, errors.New("exit status 1")
		}
		var ok bool
		out := captureStdout(t, func() { ok = reportSandboxLaunch(tempDir(t)) })
		if ok {
			t.Fatal("a refused sandbox was reported as healthy")
		}
		for _, want := range []string{
			doctorSandboxApparmorSysctl,
			"sudo sysctl -w " + doctorSandboxApparmorSysctl + "=0",
			"isolation.network.mode",
			"--no-sandbox",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("openStarts=%v: report does not mention %q:\n%s", openStarts, want, out)
			}
		}
		if got := strings.Join(modes, ","); got != "offline,open" {
			t.Errorf("launches tried %q, want the strict mode and then open", got)
		}
		if gives := strings.Contains(out, "egress allowlist is not enforced"); gives != openStarts {
			t.Errorf("openStarts=%v but the report's account of what open gives up appeared=%v:\n%s", openStarts, gives, out)
		}
	}
}

func TestNamespaceSetupRefusedRecognisesOnlyNamespaceRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		out  string
		want bool
	}{
		{"clone refused", fmt.Errorf("fork/exec nvx: %w", syscall.EPERM), "", true},
		{"loopback refused inside the namespace", errors.New("exit status 1"),
			"Network isolation failed (fail-closed): ip: Operation not permitted", true},
		{"target clone refused", errors.New("exit status 1"),
			"Sandbox execution failed: fork/exec /bin/true: operation not permitted", true},
		{"landlock failure", errors.New("exit status 1"),
			"Landlock isolation failed: landlock_restrict_self: operation not permitted", false},
		{"missing iproute2", errors.New("exit status 1"),
			"Network isolation failed (fail-closed): bring up loopback (install iproute2)", false},
	} {
		if got := namespaceSetupRefused(tc.err, tc.out); got != tc.want {
			t.Errorf("%s: namespaceSetupRefused = %v, want %v", tc.name, got, tc.want)
		}
	}
}
