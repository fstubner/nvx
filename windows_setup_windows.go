//go:build windows

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// runWinCmd runs an external command with a timeout so a stuck tool surfaces as
// an error instead of hanging. (icacls can hang indefinitely when a filter
// driver intercepts writes to certain paths, e.g. the OneDrive/Defender-guarded
// profile root — so every privileged call is time-boxed.)
func runWinCmd(timeout time.Duration, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return out, fmt.Errorf("%s timed out after %s", name, timeout)
	}
	return out, err
}

var procGetTokenInformation = modAdvapi32.NewProc("GetTokenInformation")

var procGetDriveTypeW = modKernel32.NewProc("GetDriveTypeW")

const driveFixed = 3 // DRIVE_FIXED

// fixedDriveRoots returns the root directory of every fixed volume on the
// machine (skipping removable, network, and CD-ROM drives). Projects commonly
// live off the system drive, and a tool that resolves a path walks up to that
// volume's root — a stat an AppContainer cannot perform unless the root itself
// carries a grant, since "bypass traverse checking" does not cover reading the
// root's own attributes.
func fixedDriveRoots() []string {
	var roots []string
	for c := 'A'; c <= 'Z'; c++ {
		root := string(c) + `:\`
		p, err := syscall.UTF16PtrFromString(root)
		if err != nil {
			continue
		}
		t, _, _ := procGetDriveTypeW.Call(uintptr(unsafe.Pointer(p)))
		if t == driveFixed {
			roots = append(roots, root)
		}
	}
	return roots
}

// isElevated reports whether the current process token is elevated (admin).
func isElevated() bool {
	var token syscall.Token
	proc, _, _ := procGetCurrentProcess.Call()
	if r, _, _ := procOpenProcessToken.Call(proc, uintptr(TOKEN_QUERY), uintptr(unsafe.Pointer(&token))); r == 0 {
		return false
	}
	defer syscall.CloseHandle(syscall.Handle(token))

	const tokenElevation = 20 // TokenElevation
	var elevation uint32
	var retLen uint32
	r, _, _ := procGetTokenInformation.Call(
		uintptr(token), tokenElevation,
		uintptr(unsafe.Pointer(&elevation)), unsafe.Sizeof(elevation),
		uintptr(unsafe.Pointer(&retLen)),
	)
	return r != 0 && elevation != 0
}

// windowsAncestorGrantPaths lists the system-owned directories the sandbox must
// be able to stat/traverse (npm and other tools walk ancestors up to the drive
// root). Granted this-folder-only, so contents of sibling directories stay
// inaccessible.
func windowsAncestorGrantPaths() []string {
	seen := map[string]bool{}
	var paths []string
	add := func(p string) {
		if p == "" {
			return
		}
		c := filepath.Clean(p)
		key := strings.ToLower(c)
		if !seen[key] {
			seen[key] = true
			paths = append(paths, c)
		}
	}

	sysDrive := os.Getenv("SystemDrive")
	if sysDrive == "" {
		sysDrive = "C:"
	}
	add(sysDrive + `\`)
	add(filepath.Join(sysDrive+`\`, "Users"))
	// The profile root (C:\Users\<user>) already grants ALL APPLICATION PACKAGES,
	// so it needs no grant and is deliberately excluded (its ACL write hangs
	// behind the OneDrive/Defender filter driver). Cover another volume's roots
	// only if the profile lives off the system drive.
	if up := os.Getenv("USERPROFILE"); up != "" {
		if vol := filepath.VolumeName(up); vol != "" && !strings.EqualFold(vol, sysDrive) {
			add(vol + `\`)
			add(filepath.Join(vol+`\`, "Users"))
		}
	}
	// Every other fixed volume's root too: a project living off the system drive
	// (H:\work\...) makes tools resolve paths up to that root, and without a
	// grant there the stat fails with a bare EPERM on e.g. "H:\". Root only —
	// this-folder-only RX, so the volume's contents stay governed by their own
	// ACLs and nothing below the root becomes readable by this.
	for _, root := range fixedDriveRoots() {
		add(root)
	}
	return paths
}

func grantSidReadExecThisFolder(sidStr, path string) error {
	out, err := runWinCmd(30*time.Second, "icacls", path, "/grant", fmt.Sprintf("*%s:(RX)", sidStr), "/c", "/q")
	if err != nil {
		return fmt.Errorf("icacls grant %s: %v (%s)", path, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func revokeSidGrant(sidStr, path string) error {
	out, err := runWinCmd(30*time.Second, "icacls", path, "/remove:g", "*"+sidStr, "/c", "/q")
	if err != nil {
		return fmt.Errorf("icacls remove %s: %v (%s)", path, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func setLoopbackExempt(add bool, sidStr string) error {
	flag := "-a"
	if !add {
		flag = "-d"
	}
	out, err := runWinCmd(30*time.Second, "CheckNetIsolation", "LoopbackExempt", flag, "-p="+sidStr)
	if err != nil {
		return fmt.Errorf("CheckNetIsolation LoopbackExempt %s: %v (%s)", flag, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// runWindowsSetup performs the one-time elevated setup that lets the AppContainer
// sandbox run real package-manager workflows (npm/npx) and reach the loopback
// egress proxy. It is idempotent and reversible via --undo.
func runWindowsSetup(nvxHome string, undo bool) int {
	if !isElevated() {
		LogError("nvx setup must run from an elevated (Administrator) terminal.")
		LogInfo("It grants the nvx sandbox the OS access needed to run npm/npx and to reach the egress proxy. Undo later with: nvx setup --undo")
		return 1
	}

	LogInfo("Preparing the nvx sandbox profile ...")
	sid, err := ensureAppContainerSID(stableSandboxProfile)
	if err != nil {
		LogError("Could not create the nvx AppContainer profile: %v", err)
		return 1
	}
	defer syscall.LocalFree(syscall.Handle(sid))
	sidStr, err := appContainerSidToString(sid)
	if err != nil {
		LogError("Could not read the AppContainer SID: %v", err)
		return 1
	}

	if undo {
		for _, p := range windowsAncestorGrantPaths() {
			if err := revokeSidGrant(sidStr, p); err != nil {
				LogWarn("Could not remove grant on %s: %v", p, err)
			}
		}
		if err := setLoopbackExempt(false, sidStr); err != nil {
			LogWarn("Could not remove loopback exemption: %v", err)
		}
		if err := clearWindowsSetupState(nvxHome); err != nil {
			LogWarn("Could not clear setup state: %v", err)
		}
		LogSuccess("nvx sandbox setup removed.")
		return 0
	}

	paths := windowsAncestorGrantPaths()
	for _, p := range paths {
		LogInfo("Granting sandbox stat access on %s ...", p)
		if err := grantSidReadExecThisFolder(sidStr, p); err != nil {
			LogError("Failed to grant sandbox stat access on %s: %v", p, err)
			return 1
		}
	}
	LogInfo("Registering the loopback exemption ...")
	if err := setLoopbackExempt(true, sidStr); err != nil {
		LogError("Failed to register the loopback exemption: %v", err)
		return 1
	}
	if err := writeWindowsSetupState(nvxHome, windowsSetupState{
		AppContainerSID: sidStr,
		GrantedPaths:    paths,
		LoopbackExempt:  true,
	}); err != nil {
		LogWarn("Setup applied, but recording state failed: %v", err)
	}

	LogSuccess("nvx sandbox setup complete.")
	LogInfo("npm/npx now run inside the sandbox with allowlisted egress. Undo with: nvx setup --undo (elevated).")
	return 0
}
