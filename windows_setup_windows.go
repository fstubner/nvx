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
	// This folder only: no inheritance flags, so nothing below it is affected.
	if err := grantACL(path, sidStr, aclMaskReadExec, 0); err != nil {
		return fmt.Errorf("grant read/execute on %s: %w", path, err)
	}
	return nil
}

func revokeSidGrant(sidStr, path string) error {
	if err := revokeACL(path, sidStr); err != nil {
		return fmt.Errorf("remove the permission on %s: %w", path, err)
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

// runWindowsSetup performs the optional elevated setup that grants the
// AppContainer sandbox stat access on drive roots, for tools that resolve paths
// that far up. It is idempotent and reversible via --undo.
//
// It is no longer needed for egress. Until 0.5.0 this was also where the loopback
// exemption was registered, without which the sandbox could not reach the egress
// proxy at all -- so allowlisted egress was an elevated opt-in and the default was
// an unrestricted direct connection. The in-container relay reaches the proxy over
// a UNIX socket instead, which needs no exemption and no elevation.
func runWindowsSetup(nvxHome string, undo bool) int {
	if !isElevated() {
		LogError("nvx setup must run from an elevated (Administrator) terminal.")
		LogInfo("It grants the nvx sandbox drive-root stat access for tools that need it. Egress is allowlisted either way. Undo later with: nvx setup --undo")
		return 1
	}

	LogInfo("Preparing the nvx sandbox identity ...")

	// Granted to a CAPABILITY, not to an AppContainer package.
	//
	// This used to grant the package SID, which worked only while every sandbox on
	// the machine shared one package. Packages are per-project now -- that is what
	// stops one sandbox reaching another's loopback listeners -- so a grant made
	// here could not name them; they do not exist until a project is first run.
	// Every launch carries this capability, so one elevated grant still covers all
	// of them.
	sidStr, err := deriveCapabilitySIDString(setupCapabilityName)
	if err != nil {
		LogError("Could not derive the nvx sandbox capability: %v", err)
		return 1
	}

	// The package identity older versions granted. Nothing launches under it any
	// more, but --undo has to be able to take back what an older setup gave, so it
	// is derived here for the revoke sweep below and for nothing else.
	legacySidStr := ""
	if legacySid, lerr := ensureAppContainerSID(stableSandboxProfile); lerr == nil {
		defer syscall.LocalFree(syscall.Handle(legacySid))
		if s, serr := appContainerSidToString(legacySid); serr == nil {
			legacySidStr = s
		}
	}

	if undo {
		// The profile root is deliberately excluded from the GRANT sweep (its ACL
		// write stalls behind the OneDrive/Defender filter driver), but earlier
		// versions did grant it, and README/SECURITY.md tell users --undo removes
		// it. Revoking is cheap where nothing was granted, so sweep it here even
		// though it is not granted here.
		undoPaths := append(windowsAncestorGrantPaths(), filepath.Clean(os.Getenv("USERPROFILE")))
		for _, p := range undoPaths {
			if err := revokeSidGrant(sidStr, p); err != nil {
				LogWarn("Could not remove grant on %s: %v", p, err)
			}
			// Anyone who ran an older setup has the grant on the package identity
			// instead. Removing only the capability would leave that one behind,
			// and --undo is documented as removing what setup added.
			if legacySidStr != "" {
				if err := revokeSidGrant(legacySidStr, p); err != nil {
					LogWarn("Could not remove the older grant on %s: %v", p, err)
				}
			}
		}
		if legacySidStr != "" {
			if err := setLoopbackExempt(false, legacySidStr); err != nil {
				LogWarn("Could not remove loopback exemption: %v", err)
			}
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
	// Setup used to register a loopback exemption here, because reaching the egress
	// proxy meant dialling a listener OUTSIDE the container -- which Windows blocks
	// for AppContainers without one. The in-container relay removed that need: the
	// proxy is reached over a UNIX socket and re-exposed on loopback inside the
	// container, where no exemption applies.
	//
	// So the exemption is now a permission granted for no remaining reason -- it
	// lets the sandbox reach every other loopback listener on the machine. Remove
	// it, including for users who ran an earlier setup. Best-effort: on a machine
	// that never had it, CheckNetIsolation simply reports nothing to delete.
	if legacySidStr != "" {
		if err := setLoopbackExempt(false, legacySidStr); err != nil {
			LogInfo("No loopback exemption to remove (the sandbox no longer needs one).")
		}
	}
	if err := writeWindowsSetupState(nvxHome, windowsSetupState{
		AppContainerSID: sidStr,
		GrantedPaths:    paths,
		LoopbackExempt:  false,
	}); err != nil {
		LogWarn("Setup applied, but recording state failed: %v", err)
	}

	LogSuccess("nvx sandbox setup complete.")
	LogInfo("Drive-root access granted, for tools that resolve paths that far. Undo with: nvx setup --undo (elevated).")
	LogInfo("Egress is allowlisted with or without this step; setup is not required for it.")
	return 0
}
