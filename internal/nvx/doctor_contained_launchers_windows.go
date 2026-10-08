//go:build windows

package nvx

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// What doctor's control launch does not cover is the launchers pnpm and yarn
// arrive as.
//
// The control starts nvx's own supervisor, an .exe. pnpm and yarn installed
// with `npm install -g` are batch files, and inside the sandbox every one of
// them failed with a bare "Access is denied." (see windowsBatchLaunch) while
// the control succeeded and doctor printed "nvx is intercepting commands
// correctly". So doctor also runs a batch file by its full path, the way a
// contained pnpm starts, and runs pnpm and yarn themselves when they are
// installed under a runtime nvx manages. All of it goes through the
// supervisor, the route a contained command takes, with no network.

// reportContainedLaunchers prints a line per check and returns false when one
// fails.
func reportContainedLaunchers(box *doctorSandbox) bool {
	ok := true
	if code, out, err := box.runBatchFile(); err != nil || code != 0 {
		fmt.Printf("  [FAIL] the sandbox cannot run a batch file: %s\n", describeContainedFailure(code, out, err))
		fmt.Println("         pnpm and yarn installed with npm start through one, so they will fail contained.")
		ok = false
	} else {
		fmt.Println("  [OK]   the sandbox runs batch files, which pnpm and yarn installed with npm start through")
	}

	for _, tool := range []string{"pnpm", "yarn"} {
		path := findSandboxCommand(SandboxConfig{Command: tool, NvxHome: box.nvxHome}, Policy{})
		if path == "" {
			continue // not installed, nothing to check
		}
		if !isNvxManagedRuntimePath(box.nvxHome, path) {
			if !reportUnmanagedTool(tool, path) {
				ok = false
			}
			continue
		}
		// Corepack's launcher fetches the package manager the first time it runs, and
		// this check has no network, so it fails there whatever the sandbox can do.
		// Measured 2026-10-08 with a launcher that stops the way corepack does when it
		// needs the network, doctor printed [FAIL] and exited 1 whatever the sandbox
		// could do.
		if corepackLauncherScript(path) != "" {
			fmt.Printf("  [--]   %s is corepack's launcher (%s). Corepack downloads %s the first time it runs, so doctor does not start it with no network.\n", tool, path, tool)
			continue
		}
		code, out, err := box.runTool(tool, path)
		if err == nil && code == 0 {
			fmt.Printf("  [OK]   %s runs in the sandbox (%s)\n", tool, path)
			continue
		}
		fmt.Printf("  [FAIL] %s cannot run in the sandbox (%s): %s\n", tool, path, describeContainedFailure(code, out, err))
		fmt.Printf("         Contained %s commands will fail. 'nvx --no-sandbox %s ...' runs it uncontained,\n", tool, tool)
		fmt.Println("         and nvx's known limitations list what the Windows sandbox cannot run.")
		ok = false
	}
	return ok
}

// reportUnmanagedTool says what a contained run does with a pnpm or yarn kept
// outside nvx's runtimes, without starting it, and returns false when that run
// would be refused.
//
// Starting it means doing first what a contained run does, and for a tool
// beside another Node install that is copying the whole install into nvx's
// home. Measured 2026-10-07, a yarn from an nvm-windows install cost one
// doctor run 49 seconds and a 450 MB copy. So doctor reads only what decides
// the outcome.
func reportUnmanagedTool(tool, path string) bool {
	dir := filepath.Dir(path)
	if runtimeInstallExecutable(dir) != "" {
		fmt.Printf("  [--]   %s is in a Node or Bun install nvx does not manage (%s).\n", tool, dir)
		fmt.Println("         A contained run copies that install into nvx's folder first, so doctor does not start it.")
		return true
	}
	if cwd, err := os.Getwd(); err == nil {
		if capSID, err := scopeCapabilitySID(sandboxScopeForWorkDir(cwd)); err == nil &&
			appContainerHasGrantFor(capSID, path, grantReadExec) {
			fmt.Printf("  [--]   %s runs where it is in this project's sandbox (%s), so doctor does not start it.\n", tool, path)
			return true
		}
	}
	fmt.Printf("  [FAIL] %s cannot run in the sandbox: %v\n", tool, notARuntimeInstallError(path))
	return false
}

// runBatchFile runs a batch file from the guest home by its full path, the
// shape that failed. The guest home is the one folder every launch is granted.
func (b *doctorSandbox) runBatchFile() (int, string, error) {
	batch := filepath.Join(b.guestHome, "nvx-doctor.cmd")
	if err := os.WriteFile(batch, []byte("@exit /b 0\r\n"), 0o600); err != nil {
		return 0, "", err
	}
	return b.runContained(batch, nil, scrubEnvironment(b.guestHome))
}

// runTool runs `<tool> --version` the way a contained command runs it. It is
// staged or run in place and rewritten, and gets a contained command's environment.
func (b *doctorSandbox) runTool(tool, path string) (int, string, error) {
	cmdPath, args, err := containedCommand(SandboxConfig{Command: tool, Args: []string{"--version"}, NvxHome: b.nvxHome},
		path, b.scopeCaps)
	if err != nil {
		return 0, "", err
	}
	env := containedEnv(scrubEnvironment(b.guestHome), b.guestHome, cmdPath, b.nvxHome)
	return b.runContained(cmdPath, args, env)
}

// runContained runs cmdPath with args through the supervisor and returns its
// exit code and what it printed.
func (b *doctorSandbox) runContained(cmdPath string, args, env []string) (int, string, error) {
	supervisorArgs := append([]string{
		"__appcontainer-exec",
		"--guest-home=" + b.guestHome,
		"--work-dir=" + b.launchDir,
		"--nvx-home=" + b.nvxHome,
		"--network-mode=offline",
		"--", cmdPath,
	}, args...)
	return captureLaunchOutput(func() (int, error) { return b.launch(supervisorArgs, env) })
}

// captureLaunchOutput runs launch with this process's standard output and
// error pointed at a pipe, and returns what the contained process wrote there.
//
// A launch hands the child this process's standard handles, so without this a
// passing check would print pnpm's version into the middle of doctor's report.
// doctor's own lines are unaffected, because os.Stdout keeps the handle it
// opened with.
func captureLaunchOutput(launch func() (int, error)) (int, string, error) {
	var r, w syscall.Handle
	if err := syscall.CreatePipe(&r, &w, nil, 0); err != nil {
		code, lerr := launch()
		return code, "", lerr
	}
	read := make(chan string, 1)
	go func() {
		var sb strings.Builder
		buf := make([]byte, 4096)
		for {
			var n uint32
			err := syscall.ReadFile(r, buf, &n, nil)
			if n > 0 && sb.Len() < 64<<10 {
				sb.Write(buf[:n])
			}
			if err != nil || n == 0 {
				break
			}
		}
		read <- sb.String()
	}()

	// STD_ERROR_HANDLE, as SetStdHandle takes it: (DWORD)-12.
	const stdErrorHandle = uint32(0xFFFFFFF4)
	prevOut, _ := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	prevErr, _ := syscall.GetStdHandle(syscall.STD_ERROR_HANDLE)
	_, _, _ = procSetStdHandle.Call(uintptr(stdOutputHandle), uintptr(w))
	_, _, _ = procSetStdHandle.Call(uintptr(stdErrorHandle), uintptr(w))
	code, err := launch()
	_, _, _ = procSetStdHandle.Call(uintptr(stdOutputHandle), uintptr(prevOut))
	_, _, _ = procSetStdHandle.Call(uintptr(stdErrorHandle), uintptr(prevErr))

	_ = syscall.CloseHandle(w)
	out := <-read
	_ = syscall.CloseHandle(r)
	return code, out, err
}

// describeContainedFailure says what a failed contained check printed, or why
// it did not run.
func describeContainedFailure(code int, out string, err error) string {
	if err != nil {
		return err.Error()
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	if last == "" {
		return fmt.Sprintf("it exited %d", code)
	}
	return fmt.Sprintf("it exited %d after %q", code, last)
}
