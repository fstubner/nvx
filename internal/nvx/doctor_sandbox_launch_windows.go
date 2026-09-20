//go:build windows

package nvx

import (
	"fmt"
	"os"
	"path/filepath"
)

// Asking whether the sandbox can actually start, rather than inferring it.
//
// doctor checked the shims, PATH and the shell profile -- everything that
// decides whether nvx is INVOKED -- and nothing that decides whether the
// sandbox nvx invokes can run. Those are different failures with different
// causes, and the gap between them was measured on 2026-09-20: with the
// machine out of commit charge, every contained install died on
//
//	AppContainer launch failed: CreateProcess(AppContainer) ...: Access is denied.
//
// while doctor printed "nvx is intercepting commands correctly" and exited 0.
// A user in that state has a green diagnostic, a broken product and a bare
// Windows error code to search for.
//
// The check is the same control launch the probe gate already runs
// (probe_appcontainer_capability_windows_test.go), promoted out of test-only
// code: start a process inside an AppContainer and let it exit without doing
// anything. It answers the only question worth asking here -- can CreateProcess
// start a process in an AppContainer on this host, right now.
//
// It deliberately runs the STAGED SUPERVISOR, the same binary a real contained
// command runs, rather than a purpose-built child. Two reasons. It exercises
// the staging and the grants a real launch depends on, so a fault in either is
// caught rather than stepped around. And it needs no elevation: granting an
// AppContainer read and execute on something like System32\cmd.exe means
// writing that file's ACL, which an unelevated process cannot do -- a control
// built that way reported "Access is denied" on a host that creates
// AppContainers all day, which is the failure this check exists to detect,
// reported against a healthy machine.

// sandboxLaunchWorks reports whether this host can start a process inside an
// AppContainer, and what went wrong when it cannot.
//
// The reason is a sentence for a human, not an error to match on. Its whole job
// is to turn "Access is denied" into something a reader can act on.
func sandboxLaunchWorks(nvxHome string) (bool, string) {
	if nvxHome == "" {
		return false, "no nvx home directory to stage the sandbox supervisor into"
	}

	const controlProfile = "nvx.sandbox.doctorcontrol"
	sid, err := ensureAppContainerSID(controlProfile)
	if err != nil {
		return false, fmt.Sprintf("could not create a throwaway AppContainer profile: %v", err)
	}
	defer deleteAppContainerProfile(controlProfile)

	// The supervisor a real launch would run, staged the way a real launch
	// stages it. Already present and already granted after any contained
	// command, so the common case costs a stat.
	supervisor, err := stageAppContainerSupervisor(nvxHome)
	if err != nil {
		return false, fmt.Sprintf("could not stage the sandbox supervisor: %v", err)
	}
	if err := grantRuntimeReadExecTree(filepath.Dir(supervisor)); err != nil {
		return false, fmt.Sprintf("could not grant the sandbox access to its supervisor: %v", err)
	}

	guestHome, err := os.MkdirTemp("", "nvx-doctor-home-")
	if err != nil {
		return false, fmt.Sprintf("could not make a throwaway guest home: %v", err)
	}
	defer os.RemoveAll(guestHome)
	workDir, err := os.MkdirTemp("", "nvx-doctor-work-")
	if err != nil {
		return false, fmt.Sprintf("could not make a throwaway working directory: %v", err)
	}
	defer os.RemoveAll(workDir)

	scopeCaps, launchDir, err := prepareAppContainerFilesystem(sid, nvxHome, guestHome, workDir)
	if err != nil {
		return false, fmt.Sprintf("could not prepare the sandbox filesystem: %v", err)
	}
	if launchDir == "" {
		launchDir = workDir
	}

	// The same capabilities a real launch carries, not just the scope ones.
	//
	// This passed scopeCaps alone, and a contained command does not: the setup
	// capability is what an elevated `nvx setup` grants the drive roots and the
	// profile parent to, and the runtime capability is what the staged supervisor
	// and the guest home's parent are granted to. Without them the control could
	// not traverse to its own binary on a machine where every real contained
	// command works -- a diagnostic reporting a broken sandbox against a healthy
	// host, which is the one failure this check must not have.
	exitCode, launchErr := launchAppContainerProcess(
		supervisor,
		[]string{"__appcontainer-control"},
		append(scrubEnvironment(guestHome), "NVX_SANDBOX=1"),
		launchDir, sid, 0, launchCapabilitySIDs(scopeCaps, nil),
	)
	if launchErr != nil {
		// Bare, because reportSandboxLaunch already says "the sandbox cannot
		// start:" ahead of it. Both halves said it and the line read "the sandbox
		// cannot start: the sandbox could not start: CreateProcess...".
		return false, fmt.Sprintf("%v", launchErr)
	}
	if exitCode != 0 {
		return false, fmt.Sprintf("the sandbox started but its supervisor exited %d", exitCode)
	}
	return true, ""
}

// reportSandboxLaunch prints the verdict and returns true when the sandbox is
// usable. A false answer is a real problem: every contained command on this
// host is going to fail until it is fixed.
func reportSandboxLaunch(nvxHome string) bool {
	ok, why := sandboxLaunchWorks(nvxHome)
	if ok {
		fmt.Println("  [OK]   the sandbox starts (AppContainer launch succeeded)")
		return true
	}
	fmt.Printf("  [FAIL] the sandbox cannot start: %s\n", why)
	fmt.Println("         Contained commands will fail until this is fixed. Common causes:")
	fmt.Println("         the machine is out of memory (check Task Manager's committed figure),")
	fmt.Println("         security software is blocking AppContainer processes, or")
	fmt.Println("         this Windows edition or session does not allow them.")
	fmt.Println("         Run the command with --no-sandbox to proceed without containment.")
	return false
}
