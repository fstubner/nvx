//go:build windows

package nvx

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
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

// doctorSandbox is a throwaway sandbox, set up the way a contained command's
// is, for doctor's launch checks.
type doctorSandbox struct {
	nvxHome, supervisor, guestHome, launchDir string
	sid                                       uintptr
	scopeCaps                                 []string
	cleanup                                   []func()
}

// openDoctorSandbox sets one up. It returns nil and a reason when it cannot.
//
// The reason is a sentence for a human, not an error to match on. Its whole job
// is to turn "Access is denied" into something a reader can act on.
func openDoctorSandbox(nvxHome string) (*doctorSandbox, string) {
	if nvxHome == "" {
		return nil, "no nvx home directory to stage the sandbox supervisor into"
	}
	box := &doctorSandbox{nvxHome: nvxHome}

	const controlProfile = "nvx.sandbox.doctorcontrol"
	sid, err := ensureAppContainerSID(controlProfile)
	if err != nil {
		return nil, fmt.Sprintf("could not create a throwaway AppContainer profile: %v", err)
	}
	box.sid = sid
	box.cleanup = append(box.cleanup, func() { deleteAppContainerProfile(controlProfile) })

	// The supervisor a real launch would run, staged the way a real launch
	// stages it. Already present and already granted after any contained
	// command, so the common case costs a stat.
	supervisor, err := stageAppContainerSupervisor(nvxHome)
	if err != nil {
		box.close()
		return nil, fmt.Sprintf("could not stage the sandbox supervisor: %v", err)
	}
	if err := grantRuntimeReadExecTree(filepath.Dir(supervisor)); err != nil {
		box.close()
		return nil, fmt.Sprintf("could not grant the sandbox access to its supervisor: %v", err)
	}
	box.supervisor = supervisor

	guestHome, err := os.MkdirTemp("", "nvx-doctor-home-")
	if err != nil {
		box.close()
		return nil, fmt.Sprintf("could not make a throwaway guest home: %v", err)
	}
	box.cleanup = append(box.cleanup, func() { _ = os.RemoveAll(guestHome) })
	box.guestHome = guestHome
	workDir, err := os.MkdirTemp("", "nvx-doctor-work-")
	if err != nil {
		box.close()
		return nil, fmt.Sprintf("could not make a throwaway working directory: %v", err)
	}
	box.cleanup = append(box.cleanup, func() { _ = os.RemoveAll(workDir) })

	scopeCaps, launchDir, err := prepareAppContainerFilesystem(sid, nvxHome, guestHome, workDir)
	if err != nil {
		box.close()
		return nil, fmt.Sprintf("could not prepare the sandbox filesystem: %v", err)
	}
	if launchDir == "" {
		launchDir = workDir
	}
	box.scopeCaps, box.launchDir = scopeCaps, launchDir
	return box, ""
}

// close removes what openDoctorSandbox made, last first.
func (b *doctorSandbox) close() {
	for i := len(b.cleanup) - 1; i >= 0; i-- {
		b.cleanup[i]()
	}
	b.cleanup = nil
	if b.sid != 0 {
		_, _ = syscall.LocalFree(syscall.Handle(b.sid))
		b.sid = 0
	}
}

// launch runs the staged supervisor with args inside the sandbox.
//
// The same capabilities a real launch carries, not just the scope ones.
//
// This passed scopeCaps alone, and a contained command does not: the runtime
// capability is what the staged supervisor and the guest home's parent are
// granted to. Without it the control could not traverse to its own binary on a
// machine where every real contained command works -- a diagnostic reporting a
// broken sandbox against a healthy host, which is the one failure this check
// must not have.
func (b *doctorSandbox) launch(args, env []string) (int, error) {
	return launchAppContainerProcess(b.supervisor, args, env, b.launchDir, b.sid, 0,
		launchCapabilitySIDs(b.scopeCaps, nil))
}

// control starts the supervisor and lets it exit, which answers whether this
// host can start a process inside an AppContainer at all.
func (b *doctorSandbox) control() (bool, string) {
	exitCode, launchErr := b.launch([]string{"__appcontainer-control"},
		append(scrubEnvironment(b.guestHome), "NVX_SANDBOX=1"))
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

// sandboxLaunchWorks reports whether this host can start a process inside an
// AppContainer, and what went wrong when it cannot.
func sandboxLaunchWorks(nvxHome string) (bool, string) {
	box, why := openDoctorSandbox(nvxHome)
	if box == nil {
		return false, why
	}
	defer box.close()
	return box.control()
}

// reportSandboxLaunch prints the verdict and returns true when the sandbox is
// usable. A false answer is a real problem: every contained command on this
// host is going to fail until it is fixed.
func reportSandboxLaunch(nvxHome string) bool {
	box, why := openDoctorSandbox(nvxHome)
	ok := false
	if box != nil {
		defer box.close()
		ok, why = box.control()
	}
	if ok {
		fmt.Println("  [OK]   the sandbox starts (AppContainer launch succeeded)")
		return reportContainedLaunchers(box)
	}
	fmt.Printf("  [FAIL] the sandbox cannot start: %s\n", why)
	fmt.Println("         Contained commands will fail until this is fixed. Common causes:")
	fmt.Println("         the machine is out of memory (check Task Manager's committed figure),")
	fmt.Println("         security software is blocking AppContainer processes, or")
	fmt.Println("         this Windows edition or session does not allow them.")
	fmt.Println("         Run the command with --no-sandbox to proceed without containment.")
	return false
}
