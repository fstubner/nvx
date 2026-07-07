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

// runPrivilegedCmd runs a setup command with a timeout so a stuck external tool
// surfaces as an error instead of hanging the whole setup.
func runPrivilegedCmd(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return out, fmt.Errorf("%s timed out after 30s", name)
	}
	return out, err
}

var procGetTokenInformation = modAdvapi32.NewProc("GetTokenInformation")

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
	if up := os.Getenv("USERPROFILE"); up != "" {
		if vol := filepath.VolumeName(up); vol != "" {
			add(vol + `\`)
			add(filepath.Join(vol+`\`, "Users"))
		}
		add(up)
	}
	return paths
}

func grantSidReadExecThisFolder(sidStr, path string) error {
	out, err := runPrivilegedCmd("icacls", path, "/grant", fmt.Sprintf("*%s:(RX)", sidStr), "/c", "/q")
	if err != nil {
		return fmt.Errorf("icacls grant %s: %v (%s)", path, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func revokeSidGrant(sidStr, path string) error {
	out, err := runPrivilegedCmd("icacls", path, "/remove:g", "*"+sidStr, "/c", "/q")
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
	out, err := runPrivilegedCmd("CheckNetIsolation", "LoopbackExempt", flag, "-p="+sidStr)
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
