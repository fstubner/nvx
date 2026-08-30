package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// isPackageManagerCommand reports whether cmd is a JS package manager/executor
// that walks ancestor directories (and therefore needs the Windows sandbox
// setup to run under AppContainer isolation).
func isPackageManagerCommand(cmd string) bool {
	base := strings.ToLower(filepath.Base(cmd))
	base = strings.TrimSuffix(strings.TrimSuffix(base, ".cmd"), ".exe")
	switch base {
	case "npm", "npx", "yarn", "pnpm":
		return true
	}
	return false
}

// stableSandboxProfile is the AppContainer profile name nvx used for every
// sandbox until per-project packages landed. It survives as the name whose SID
// `nvx doctor` checks for a leftover pre-0.5.0 loopback exemption, and as the
// identity `nvx setup --undo` still has to revoke for anyone who ran an older
// setup. Nothing launches under it any more.
const stableSandboxProfile = "nvx.sandbox"

// setupCapabilityName is the durable identity `nvx setup` grants drive-root stat
// access to.
//
// It used to grant the package SID, which worked only while every sandbox shared
// one package. Packages are per-project now, so a grant made at setup time could
// not name them -- they do not exist yet. A capability can be granted once and
// carried by every launch, which is what makes the elevated grant outlive the
// change. Capabilities do not affect the loopback rule; that is package-scoped,
// which is the whole point.
const setupCapabilityName = "nvx.setup.driveroots"

// sandboxPackageName returns the AppContainer package a session runs under.
//
// One package per project, not one per machine. Windows permits loopback WITHIN
// an AppContainer package, so while every nvx sandbox shared a single package,
// any port a contained process bound was reachable from every other contained
// process on the machine -- across unrelated projects, with an empty allowlist
// and no --connect. Measured 2026-08-29: sandbox B read a listener inside
// sandbox A, while the same sandbox could not reach a listener on the host and
// the host could not reach sandbox A. That is an egress allowlist defeated by
// relay, and a channel between two projects the filesystem isolation is built to
// keep apart.
//
// Confirmed to be the package, not the capability: with the profile name varied
// per launch and nothing else changed, the same connection was refused
// (ECONNREFUSED) for two different names and succeeded when both sides shared
// one.
//
// Per project rather than per session, deliberately. Two runs of the same
// project are the same trust domain -- the same dependencies, the same policy --
// and a stable name per project keeps the per-project filesystem ACLs and the
// grant cache meaningful across runs. Two runs in ONE project can still reach
// each other; that boundary is documented rather than claimed away.
//
// Falls back to the session id when there is no project scope, which is tighter
// still: a session that belongs to no project shares its package with nothing.
func sandboxPackageName(scopeDir, sandboxID string) string {
	seed := strings.ToLower(filepath.Clean(scopeDir))
	if strings.TrimSpace(scopeDir) == "" {
		seed = "session\x00" + sandboxID
	}
	sum := sha256.Sum256([]byte(seed))
	// 16 hex characters keeps the whole name at 28, well inside the 64-character
	// limit CreateAppContainerProfile enforces, and only alphanumerics and periods
	// appear, which is the character set it accepts.
	return stableSandboxProfile + "." + hex.EncodeToString(sum[:])[:16]
}

// windowsSetupState records what `nvx setup` granted, so the sandbox can switch
// to the allowlisted-proxy path and `nvx setup --undo` can reverse it.
type windowsSetupState struct {
	AppContainerSID string   `json:"appcontainer_sid"`
	GrantedPaths    []string `json:"granted_paths"`
	LoopbackExempt  bool     `json:"loopback_exempt"`
	SetupAt         string   `json:"setup_at"`
}

func windowsSetupMarkerPath(nvxHome string) string {
	return filepath.Join(nvxHome, "windows-setup.json")
}

// windowsSandboxSetupDone is deliberately absent.
//
// It reported whether `nvx setup` had completed, by testing LoopbackExempt --
// a state setup no longer creates, and deliberately removes. So it answered
// "has setup run" with "does this machine still carry the thing setup exists to
// take away", which by now can only be false. Nothing called it, and anything
// that started to would have been reading a wrong answer confidently.
//
// What replaced it is not a boolean: whether the grants setup made still apply
// is a question about the CURRENT sandbox identity, which noteMissingElevatedGrants
// and reportStrandedSetupGrant each ask against the real ACLs.

func readWindowsSetupState(nvxHome string) (windowsSetupState, bool) {
	data, err := os.ReadFile(windowsSetupMarkerPath(nvxHome))
	if err != nil {
		return windowsSetupState{}, false
	}
	var s windowsSetupState
	if err := json.Unmarshal(data, &s); err != nil {
		return windowsSetupState{}, false
	}
	return s, true
}

func writeWindowsSetupState(nvxHome string, s windowsSetupState) error {
	if s.SetupAt == "" {
		s.SetupAt = time.Now().UTC().Format(time.RFC3339)
	}
	if err := os.MkdirAll(nvxHome, 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(windowsSetupMarkerPath(nvxHome), append(data, '\n'), 0600)
}

func clearWindowsSetupState(nvxHome string) error {
	err := os.Remove(windowsSetupMarkerPath(nvxHome))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
