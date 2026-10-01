//go:build linux

package nvx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// Asking whether the Linux sandbox can actually start, rather than assuming it.
//
// doctor checked the shims and PATH and nothing about the sandbox, so a host
// whose kernel refuses the sandbox's namespaces printed "nvx is intercepting
// commands correctly" and exited 0 while every contained command failed. The
// common case is Ubuntu 23.10 and later, which restricts unprivileged user
// namespaces through AppArmor (kernel.apparmor_restrict_unprivileged_userns=1):
// the clone succeeds, the process has no capabilities inside the new namespace,
// and nvx's loopback setup fails with "Operation not permitted". The project's
// own CI relaxes that sysctl for the same reason.
//
// The check runs the same supervisor a contained command runs, around /bin/true,
// in the strictest namespace mode the host's policy allows.

// doctorSandboxApparmorSysctl is the Ubuntu setting that restricts the sandbox's
// namespaces.
const doctorSandboxApparmorSysctl = "kernel.apparmor_restrict_unprivileged_userns"

// controlLaunchTimeout bounds the control launch. It ends in milliseconds on a
// healthy host; this only stops a wedged one from hanging doctor.
const controlLaunchTimeout = 30 * time.Second

// linuxControlLaunchFn is the seam the check is driven through in tests.
var linuxControlLaunchFn = linuxControlLaunch

// linuxControlLaunch runs the supervisor around /bin/true under network mode
// mode and returns what it printed. A nil error means a contained process
// started and exited 0.
func linuxControlLaunch(nvxHome, mode string) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("the nvx executable could not be resolved: %w", err)
	}
	guest, err := os.MkdirTemp("", "nvx-doctor-")
	if err != nil {
		return "", fmt.Errorf("could not make a throwaway guest home: %w", err)
	}
	defer os.RemoveAll(guest)

	ctx, cancel := context.WithTimeout(context.Background(), controlLaunchTimeout)
	defer cancel()
	// #nosec G204 -- the executable is nvx itself and every argument is built here.
	cmd := exec.CommandContext(ctx, exe, "__landlock-exec",
		"--guest-home="+guest,
		"--work-dir="+guest,
		"--nvx-home="+nvxHome,
		"--network-mode="+mode,
		"--", "/bin/true")
	cmd.Dir = guest
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "HOME=" + guest}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	cmd.SysProcAttr = supervisorSysProcAttr(mode)
	err = runSupervisor(cmd, guest)
	return out.String(), err
}

// namespaceSetupRefused reports whether a failed control launch was the kernel
// or AppArmor refusing the sandbox its namespaces: either the clone itself came
// back EPERM, or the supervisor got as far as setting up inside the namespace and
// was refused there. A bare "operation not permitted" elsewhere (a Landlock
// failure, say) is not this.
func namespaceSetupRefused(err error, out string) bool {
	if errors.Is(err, syscall.EPERM) {
		return true
	}
	low := strings.ToLower(out)
	return strings.Contains(low, "operation not permitted") &&
		(strings.Contains(low, "network isolation failed") || strings.Contains(low, "sandbox execution failed"))
}

// lastLines returns up to n non-empty trailing lines of s, joined for display.
func lastLines(s string, n int) string {
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(s), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " | ")
}

// sandboxLaunchAdvice is the text printed under a failed check. openStarts says
// whether the same launch succeeded with network.mode open, and sysctl is the
// restriction's current value ("" when it does not exist on this kernel).
func sandboxLaunchAdvice(refused bool, openStarts bool, sysctl string) string {
	var b strings.Builder
	if !refused {
		b.WriteString("         Contained commands will fail until this is fixed.\n")
		b.WriteString("         Run the command with --no-sandbox to proceed without containment.\n")
		return b.String()
	}
	b.WriteString("         The kernel refused the sandbox its user and network namespaces. On Ubuntu\n")
	b.WriteString("         23.10 and later that is AppArmor restricting unprivileged user namespaces\n")
	fmt.Fprintf(&b, "         (%s", doctorSandboxApparmorSysctl)
	if sysctl != "" {
		fmt.Fprintf(&b, ", set to %s here", sysctl)
	}
	b.WriteString(").\n")
	b.WriteString("         Two ways forward:\n")
	fmt.Fprintf(&b, "           1. Relax it: sudo sysctl -w %s=0\n", doctorSandboxApparmorSysctl)
	b.WriteString("              Put the same line in a file under /etc/sysctl.d/ to keep it after a\n")
	b.WriteString("              reboot. It loosens a hardening that applies to every program on the\n")
	b.WriteString("              machine, not only nvx.\n")
	b.WriteString("           2. Set isolation.network.mode to \"open\" in your nvx policy.\n")
	if openStarts {
		b.WriteString("              The sandbox does start that way on this machine. It gives up the\n")
		b.WriteString("              network namespace, so contained code shares the host network and the\n")
		b.WriteString("              egress allowlist is not enforced. Filesystem containment stays.\n")
	} else {
		b.WriteString("              That did not start here either, so it is no way round this host.\n")
	}
	b.WriteString("         Or run the command with --no-sandbox to proceed without containment.\n")
	return b.String()
}

// reportSandboxLaunch prints the verdict and returns true when the sandbox is
// usable. A false answer is a real problem: every contained command on this
// host is going to fail until it is fixed.
func reportSandboxLaunch(nvxHome string) bool {
	mode := "offline" // the strictest: a network namespace with loopback brought up
	if p, err := LoadPolicy(nvxHome); err == nil && !networkModeRequiresNamespace(p.Isolation.Network.Mode) {
		mode = "open"
	}
	out, err := linuxControlLaunchFn(nvxHome, mode)
	if err == nil {
		fmt.Println("  [OK]   the sandbox starts (a contained process launched and exited)")
		return true
	}

	why := lastLines(out, 3)
	if why == "" {
		why = err.Error()
	}
	fmt.Printf("  [FAIL] the sandbox cannot start: %s\n", why)

	refused := namespaceSetupRefused(err, out)
	openStarts := false
	sysctl := ""
	if refused {
		if raw, rerr := os.ReadFile("/proc/sys/" + strings.ReplaceAll(doctorSandboxApparmorSysctl, ".", "/")); rerr == nil {
			sysctl = strings.TrimSpace(string(raw))
		}
		if mode != "open" {
			_, oerr := linuxControlLaunchFn(nvxHome, "open")
			openStarts = oerr == nil
		}
	}
	fmt.Print(sandboxLaunchAdvice(refused, openStarts, sysctl))
	return false
}
