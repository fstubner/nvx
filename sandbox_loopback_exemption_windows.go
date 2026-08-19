//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// A loopback exemption left behind by an older `nvx setup` lets a contained
// process reach every service on the machine's loopback address, whatever the
// egress allowlist says.
//
// Windows normally refuses an AppContainer's connections to 127.0.0.1 outside its
// own package, and 0.5.0's relay depends on that: the proxy is reached over a UNIX
// socket and re-exposed on loopback inside the container, so no exemption is
// needed. Before 0.5.0 there was no relay, the proxy ran on the host's loopback,
// and an elevated `nvx setup` registered an exemption so the sandbox could reach
// it -- which also opened every other loopback listener: a local database, a
// daemon's TCP port, another project's dev server.
//
// 0.5.0 never registers one and removes it in `nvx setup`, but that command needs
// an Administrator terminal, and the same release tells users setup is no longer
// required. So on a machine that ran the older elevated setup the exemption simply
// stays, and nothing said so: the allowlist looked enforced, external hosts really
// were blocked, and loopback was wide open. Found by acceptance testing on
// 2026-08-19, by reading a host listener from inside a contained process.
//
// nvx cannot remove it without elevation, so it warns instead -- on every affected
// launch, not once, because this is a live weakening of the containment the
// command is being asked for and a one-shot notice is easy to miss.

// loopbackExemptRecheckTTL bounds how long a clear result is trusted. Only the
// clear answer is cached: while the exemption is present every launch re-checks,
// so the warning stops the moment an elevated `nvx setup` removes it, rather than
// nagging for a day after the user has done what it asked.
const loopbackExemptRecheckTTL = 24 * time.Hour

type loopbackExemptCheck struct {
	SID       string    `json:"sid"`
	CheckedAt time.Time `json:"checked_at"`
}

func loopbackExemptCachePath(nvxHome string) string {
	return filepath.Join(nvxHome, "loopback-exempt-clear.json")
}

// parseLoopbackExemptSIDs pulls the package SIDs out of `CheckNetIsolation
// LoopbackExempt -s` output. It matches the SIDs themselves rather than the
// "SID:" label, because the label is localized and because an entry whose profile
// has been deleted prints its name as "AppContainer NOT FOUND" while remaining
// exempt.
func parseLoopbackExemptSIDs(out string) []string {
	return appContainerPackageSID.FindAllString(out, -1)
}

func sidListContains(sids []string, sidStr string) bool {
	for _, s := range sids {
		if strings.EqualFold(strings.TrimSpace(s), strings.TrimSpace(sidStr)) {
			return true
		}
	}
	return false
}

// listLoopbackExemptSIDs reads the machine's exemption list. The read needs no
// elevation; only adding or removing does.
func listLoopbackExemptSIDs() ([]string, error) {
	out, err := runWinCmd(15*time.Second, "CheckNetIsolation", "LoopbackExempt", "-s")
	if err != nil {
		return nil, fmt.Errorf("CheckNetIsolation LoopbackExempt -s: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	return parseLoopbackExemptSIDs(string(out)), nil
}

// sandboxIsLoopbackExempt reports whether sidStr currently holds a loopback
// exemption, consulting the cached clear result first so a healthy machine pays
// nothing on the launch path (the check costs ~70ms, against a ~1s launch).
func sandboxIsLoopbackExempt(nvxHome, sidStr string) (bool, error) {
	if loopbackExemptRecentlyClear(nvxHome, sidStr) {
		return false, nil
	}
	sids, err := listLoopbackExemptSIDs()
	if err != nil {
		return false, err
	}
	if sidListContains(sids, sidStr) {
		return true, nil
	}
	markLoopbackExemptClear(nvxHome, sidStr)
	return false, nil
}

func loopbackExemptRecentlyClear(nvxHome, sidStr string) bool {
	data, err := os.ReadFile(loopbackExemptCachePath(nvxHome))
	if err != nil {
		return false
	}
	var c loopbackExemptCheck
	if err := json.Unmarshal(data, &c); err != nil {
		return false
	}
	if !strings.EqualFold(c.SID, sidStr) {
		return false
	}
	return time.Since(c.CheckedAt) < loopbackExemptRecheckTTL
}

func markLoopbackExemptClear(nvxHome, sidStr string) {
	data, err := json.Marshal(loopbackExemptCheck{SID: sidStr, CheckedAt: time.Now()})
	if err != nil {
		return
	}
	_ = os.WriteFile(loopbackExemptCachePath(nvxHome), data, 0o600)
}

// warnIfSandboxLoopbackExempt tells the user their sandbox can reach the whole
// machine's loopback, and how to stop it. Silent under network.mode "open", where
// no egress restriction was asked for in the first place.
func warnIfSandboxLoopbackExempt(nvxHome, sidStr, mode string) {
	if strings.EqualFold(strings.TrimSpace(mode), "open") {
		return
	}
	exempt, err := sandboxIsLoopbackExempt(nvxHome, sidStr)
	if err != nil || !exempt {
		// A failed check is not treated as a finding: CheckNetIsolation is absent
		// on some SKUs, and an unreadable list says nothing about what is in it.
		return
	}
	LogWarn("This machine still has a loopback exemption for the nvx sandbox, left by an older 'nvx setup'.")
	LogWarn("Contained code can reach any service on 127.0.0.1 -- databases, daemons, dev servers -- without an allowlist entry. Egress to other hosts is unaffected.")
	LogInfo("To remove it, from an Administrator terminal:")
	LogInfo("  CheckNetIsolation LoopbackExempt -d -p=%s", sidStr)
	LogInfo("Or run 'nvx setup' elevated, which removes it as well.")
}

// deriveAppContainerSIDString returns the SID string for a profile name without
// registering the profile. `nvx doctor` diagnoses and must not create anything;
// DeriveAppContainerSidFromAppContainerName answers for any valid name whether or
// not a profile exists, which is what the exemption list is keyed by.
func deriveAppContainerSIDString(profileName string) (string, error) {
	name, err := syscall.UTF16PtrFromString(profileName)
	if err != nil {
		return "", err
	}
	var sid uintptr
	hr, _, callErr := procDeriveAppContainerSidFromAppContainerName.Call(
		uintptr(unsafe.Pointer(name)),
		uintptr(unsafe.Pointer(&sid)),
	)
	if hr != 0 || sid == 0 {
		return "", fmt.Errorf("DeriveAppContainerSidFromAppContainerName(%q) hr=0x%X: %v", profileName, hr, callErr)
	}
	defer syscall.LocalFree(syscall.Handle(sid))
	return appContainerSidToString(sid)
}

// reportSandboxWeakeners is the doctor hook for machine state that quietly
// weakens containment. It reports and counts against health -- doctor printing a
// failure line and still calling the install healthy is the shape of dishonesty
// this command keeps being caught by. Removing the exemption needs elevation, but
// it is one command and doctor prints it.
func reportSandboxWeakeners(nvxHome string) bool {
	sidStr, err := deriveAppContainerSIDString(stableSandboxProfile)
	if err != nil {
		return false
	}
	exempt, err := sandboxIsLoopbackExempt(nvxHome, sidStr)
	if err != nil || !exempt {
		return false
	}
	fmt.Println("  [FAIL] the sandbox has a loopback exemption from an older 'nvx setup'")
	fmt.Println("         contained code can reach any service on 127.0.0.1, whatever the egress allowlist says")
	fmt.Printf("         remove it from an Administrator terminal: CheckNetIsolation LoopbackExempt -d -p=%s\n", sidStr)
	return true
}
