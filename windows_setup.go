package main

import (
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

// stableSandboxProfile is the durable AppContainer profile name. Because an
// AppContainer SID derives deterministically from its profile name, a stable
// name yields a stable SID that `nvx setup` can grant persistent access to.
const stableSandboxProfile = "nvx.sandbox"

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
