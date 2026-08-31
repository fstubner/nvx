//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Deciding whether a refused AppContainer launch is this host's limitation or a
// real defect, by asking the host instead of assuming.
//
// requireAppContainerLaunch skips on two refusal texts, "Access is denied" and
// "The system cannot find the file specified", because GitHub-hosted Windows
// runners produce them for every executable including cmd.exe. That reasoning is
// sound for a host that can never create an AppContainer. It is wrong for a host
// that normally can and just failed -- and the second text is exactly what
// CreateProcess returns on a developer machine that has run out of commit charge.
//
// The cost of not telling those apart was measured, not imagined: a run of the
// full gate reported 0 failures with 23 skips, seventeen more than the 6 it skips
// when healthy, because every containment probe in the run was refused and every
// refusal was read as "cannot run here". A gate cannot be allowed to report
// success for a run in which no containment was checked.
//
// So the host is asked once per run, with a control launch that has nothing to do
// with any probe's subject: start a process in an AppContainer and let it exit
// without running anything. If that works, this host creates AppContainers, and a
// later refusal is a finding rather than an excuse.
var (
	appContainerControlOnce sync.Once
	appContainerControlOK   bool
	appContainerControlWhy  string
)

// hostAppContainerCapability is the seam requireAppContainerLaunch consults, so
// its decision can be tested against both answers without needing a host that
// really cannot create AppContainers.
var hostAppContainerCapability = hostCanCreateAppContainers

// hostCanCreateAppContainers reports whether a control AppContainer launch
// succeeded in this process, and what happened if it did not.
func hostCanCreateAppContainers() (bool, string) {
	appContainerControlOnce.Do(func() {
		appContainerControlOK, appContainerControlWhy = runAppContainerControlLaunch()
	})
	return appContainerControlOK, appContainerControlWhy
}

func runAppContainerControlLaunch() (bool, string) {
	const controlProfile = "nvx.sandbox.hostcontrol"
	sid, err := ensureAppContainerSID(controlProfile)
	if err != nil {
		return false, fmt.Sprintf("could not create the control AppContainer profile: %v", err)
	}
	defer deleteAppContainerProfile(controlProfile)

	guestHome, err := os.MkdirTemp("", "nvx-control-home-")
	if err != nil {
		return false, fmt.Sprintf("could not make a control guest home: %v", err)
	}
	defer os.RemoveAll(guestHome)
	workDir, err := os.MkdirTemp("", "nvx-control-work-")
	if err != nil {
		return false, fmt.Sprintf("could not make a control working directory: %v", err)
	}
	defer os.RemoveAll(workDir)

	scopeCaps, err := prepareAppContainerFilesystem(sid, "", guestHome, workDir)
	if err != nil {
		return false, fmt.Sprintf("could not prepare the control filesystem: %v", err)
	}

	// The control runs the same staged child the probes run, not cmd.exe.
	//
	// cmd.exe was the obvious choice and it is the wrong one: granting an
	// AppContainer read/execute on C:\Windows\System32\cmd.exe means writing that
	// file's ACL, which an unelevated process cannot do. Measured on this machine,
	// the control then reported "Access is denied" and concluded the host cannot
	// create AppContainers -- on a host that creates them all day. A control that
	// answers "incapable" everywhere would have restored the exact silent-skip
	// behaviour it was written to remove, while looking like a fix.
	//
	// Staging into the guest home needs no elevation, and it exercises the path the
	// probes actually take.
	child, err := probeChildBinary()
	if err != nil {
		return false, fmt.Sprintf("could not produce a control child binary: %v", err)
	}
	data, err := os.ReadFile(child)
	if err != nil {
		return false, fmt.Sprintf("could not read the control child binary: %v", err)
	}
	childExe := filepath.Join(guestHome, "hostcontrol.exe")
	if err := os.WriteFile(childExe, data, 0o700); err != nil {
		return false, fmt.Sprintf("could not stage the control child: %v", err)
	}

	// NVX_HOST_CONTROL_CHILD makes the child exit 0 from TestMain before the
	// testing package runs or prints anything -- see main_test.go. A -test.run
	// pattern matching no test was the obvious way to do this and the wrong one:
	// the child still reached the testing package, still wrote to the stdout this
	// process shares with it, and cmd/go put "[no tests to run]" on the gate's
	// summary line for the whole package.
	//
	// All this asks is whether CreateProcess can start a process inside an
	// AppContainer at all, which is the only thing the verdict is about.
	exitCode, launchErr := launchAppContainerProcess(childExe, nil,
		append(scrubEnvironment(guestHome), "NVX_HOST_CONTROL_CHILD=1"),
		workDir, sid, 0, scopeCaps)
	if launchErr != nil {
		return false, fmt.Sprintf("the control launch was refused: %v", launchErr)
	}
	if exitCode != 0 {
		return false, fmt.Sprintf("the control child started but exited %d", exitCode)
	}
	return true, ""
}
