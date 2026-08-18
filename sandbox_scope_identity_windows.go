//go:build windows

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// The containment identity problem this file solves.
//
// The AppContainer profile is stable on purpose: `nvx setup` grants drive-root
// stat access to its SID, and that grant has to survive across runs. But it means
// every sandbox session on the machine runs as the SAME security identity, while
// prepareAppContainerFilesystem grants that identity (M) on each working
// directory and never revokes it.
//
// Those two compose into a hole. The grant added while installing in project A is
// still present, and still satisfied by the same SID, when nvx later runs in
// project B -- so an install in one project could read and write every project
// nvx had ever run in, other concurrent sessions' guest homes, and tool_home
// profiles, which hold what a trusted tool authenticated with. Measured
// 2026-08-18.
//
// The fix gives each project its own identity on a second axis. A Windows token
// carries capability SIDs alongside the package SID, an ACE naming a capability
// is honoured for file access, and a process holding a DIFFERENT capability is
// denied -- all three measured before this was built (see the capability SID
// probe). So the package SID stays stable for setup, and per-project capabilities
// carry the isolation.
//
// Deriving from the project scope rather than the session is what makes it
// affordable. The same project derives the same SID every run, so the icacls
// write happens once and appContainerHasGrant skips it thereafter. A per-session
// identity would pay that write on every launch and leave a dead ACE on the
// user's project directory after each one.

var (
	procDeriveCapabilitySidsFromName = findDeriveCapabilitySids()

	scopeCapabilityMu    sync.Mutex
	scopeCapabilityCache = map[string]string{}
)

func findDeriveCapabilitySids() *syscall.LazyProc {
	// Documented in userenv.dll; present in kernelbase.dll on current Windows.
	// Try both rather than assuming, and let the caller handle absence.
	for _, dll := range []string{"userenv.dll", "kernelbase.dll"} {
		proc := syscall.NewLazyDLL(dll).NewProc("DeriveCapabilitySidsFromName")
		if proc.Find() == nil {
			return proc
		}
	}
	return nil
}

// scopeCapabilityName maps a project directory to a stable capability name.
//
// The path is hashed rather than embedded: capability names have a restricted
// character set, and a project path can contain anything. Hashing also keeps the
// name from disclosing where the user's projects live to anything that can
// enumerate capability SIDs.
func scopeCapabilityName(scopeDir string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(scopeDir))))
	return "nvx.scope." + hex.EncodeToString(sum[:])[:32]
}

// deriveCapabilitySIDString returns the capability SID for a capability name, as
// a string suitable for icacls and for buildCapabilitySIDAttrs.
func deriveCapabilitySIDString(name string) (string, error) {
	if procDeriveCapabilitySidsFromName == nil {
		return "", fmt.Errorf("DeriveCapabilitySidsFromName is unavailable on this host")
	}
	namePtr, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return "", err
	}
	// Both SID out-params are PSID arrays, so each is a **SID. Declaring them that
	// way rather than as uintptr keeps the dereference below a plain pointer read
	// -- go vet flags the uintptr form as a possible misuse, and it is genuinely
	// unsafe if the GC ever moves the allocation.
	var (
		groupSids   **syscall.SID
		groupCount  uint32
		capSids     **syscall.SID
		capSidCount uint32
	)
	ret, _, callErr := procDeriveCapabilitySidsFromName.Call(
		uintptr(unsafe.Pointer(namePtr)),
		uintptr(unsafe.Pointer(&groupSids)),
		uintptr(unsafe.Pointer(&groupCount)),
		uintptr(unsafe.Pointer(&capSids)),
		uintptr(unsafe.Pointer(&capSidCount)),
	)
	if ret == 0 {
		return "", fmt.Errorf("DeriveCapabilitySidsFromName(%q): %v", name, callErr)
	}
	if capSidCount == 0 || capSids == nil {
		return "", fmt.Errorf("DeriveCapabilitySidsFromName(%q) returned no capability SIDs", name)
	}
	// Only the first capability SID is used; a single name yields exactly one.
	return appContainerSidToString(uintptr(unsafe.Pointer(*capSids)))
}

// scopeCapabilitySID returns the capability SID that identifies scopeDir,
// deriving it once per process.
func scopeCapabilitySID(scopeDir string) (string, error) {
	if scopeDir == "" {
		return "", fmt.Errorf("no project scope for this session")
	}
	key := strings.ToLower(filepath.Clean(scopeDir))

	scopeCapabilityMu.Lock()
	defer scopeCapabilityMu.Unlock()
	if sid, ok := scopeCapabilityCache[key]; ok {
		return sid, nil
	}
	sid, err := deriveCapabilitySIDString(scopeCapabilityName(key))
	if err != nil {
		return "", err
	}
	scopeCapabilityCache[key] = sid
	return sid, nil
}

// removeStaleAppContainerGrant deletes an explicit ACE for the shared package SID
// from a path now governed by a per-project capability.
//
// Without this the fix would do nothing for anyone upgrading: every project nvx
// has already run in still carries a (M) ACE for the shared SID, which every
// future session still holds. Inherited ACEs are untouched by /remove:g, so the
// drive-root grants `nvx setup` adds are not affected.
//
// Best-effort. Failing to clean an old grant leaves the previous behaviour for
// that one path, which is worth a log line and not worth refusing to run.
func removeStaleAppContainerGrant(packageSIDStr, path string) {
	if path == "" || !appContainerHasGrant(packageSIDStr, path) {
		return
	}
	out, err := runWinCmd(30*time.Second, "icacls", path, "/remove:g", "*"+packageSIDStr, "/c", "/q")
	if err != nil {
		LogWarn("Could not remove a stale sandbox permission on %q: %v (%s)", path, err, strings.TrimSpace(string(out)))
		return
	}
	LogInfo("Removed a shared sandbox permission left on %q by an earlier nvx; this project now has its own.", path)
}
