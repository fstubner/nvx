package nvx

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// stableSandboxProfile is the AppContainer profile name nvx used for every
// sandbox until per-project packages landed. It survives as the name whose SID
// `nvx doctor` checks for a leftover pre-0.5.0 loopback exemption, and as the
// identity `nvx setup` still has to clear for anyone who ran an older setup.
// Nothing launches under it any more.
const stableSandboxProfile = "nvx.sandbox"

// setupCapabilityName is the identity an older `nvx setup` granted drive-root
// stat access to. Setup no longer grants anything.
//
// It used to grant the package SID, which worked only while every sandbox shared
// one package. Packages are per-project now, so a grant made at setup time could
// not name them -- they do not exist yet. A capability can be granted once and
// carried by every launch, which is what made that elevated grant outlive the
// change. Launches still carry it, so a grant an older setup left keeps applying
// until `nvx setup` removes it. Capabilities do not affect the loopback rule;
// that is package-scoped, which is the whole point.
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

// windowsSetupState is the record an older `nvx setup` wrote of what it granted.
// Nothing writes it now. It is read so `nvx setup` can find and remove the
// paths it names.
type windowsSetupState struct {
	AppContainerSID string   `json:"appcontainer_sid"`
	GrantedPaths    []string `json:"granted_paths"`
	LoopbackExempt  bool     `json:"loopback_exempt"`
	SetupAt         string   `json:"setup_at"`
}

func windowsSetupMarkerPath(nvxHome string) string {
	return filepath.Join(nvxHome, "windows-setup.json")
}

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

func clearWindowsSetupState(nvxHome string) error {
	err := os.Remove(windowsSetupMarkerPath(nvxHome))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
