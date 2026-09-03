package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Where a shell reads its startup file, and whether nvx's integration is in it.
//
// `nvx doctor` checked shim interception -- is ~/.nvx/bin on PATH, does `npm`
// resolve to it -- and nothing else. That is half of a working install. The
// other half is the shell integration, which is what makes `nvx use` take
// effect and what switches runtimes on `cd`; without it `nvx use 22` changes
// nothing and entering a project pinned to another version runs whatever was
// already there.
//
// Both installers write the integration line, so anyone who ran install.ps1 or
// install.sh has it. Anyone who built from source, or copied a binary, does
// not -- and doctor told them "nvx is intercepting commands correctly", which
// was true and not the whole truth.

// integrationLineFor returns the line a profile needs, in that shell's syntax.
// It is the same line the installers write.
func integrationLineFor(shell string) string {
	switch shell {
	case "powershell":
		return `nvx env --shell=powershell | Out-String | Invoke-Expression`
	case "zsh":
		return `eval "$(nvx env --shell=zsh)"`
	default:
		return `eval "$(nvx env --shell=bash)"`
	}
}

// profilePathFor returns the startup file for a shell, or "" when nvx cannot
// work it out.
//
// PowerShell is asked rather than guessed: $PROFILE moves with the host
// (Windows PowerShell, PowerShell 7, VS Code each have their own), and writing
// to the wrong one leaves the user with a line that never runs.
func profilePathFor(shell string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	switch shell {
	case "powershell":
		for _, exe := range []string{"pwsh", "powershell"} {
			out, err := exec.Command(exe, "-NoProfile", "-Command", "$PROFILE").Output()
			if err == nil {
				if p := strings.TrimSpace(string(out)); p != "" {
					return p
				}
			}
		}
		return ""
	case "zsh":
		return filepath.Join(home, ".zshrc")
	default:
		return filepath.Join(home, ".bashrc")
	}
}

// profileLoadsIntegration reports whether path already loads nvx.
//
// Matched on "nvx env" rather than the exact line, because someone may have
// written it with different spacing, a different shell flag, or wrapped it in a
// conditional. Telling them to add a second copy would be worse than saying
// nothing.
func profileLoadsIntegration(path string) bool {
	if path == "" {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.Contains(trimmed, "nvx env") {
			return true
		}
	}
	return false
}

// addIntegrationToProfile appends the line, creating the file if needed.
func addIntegrationToProfile(path, shell string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { // #nosec G301 -- a profile dir is not secret
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644) // #nosec G302 -- shells require a readable profile
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString("\n# nvx shell integration (runtime switching on cd)\n" + integrationLineFor(shell) + "\n")
	return err
}

// reportShellIntegration says whether runtime switching will actually work,
// and with fix set, makes it work.
//
// Reported after the interception verdict rather than folded into it: a machine
// whose shims intercept correctly but whose shell never loads nvx is not broken
// in the way doctor's exit code means. Commands are still audited and contained
// -- the security half is intact -- but `nvx use` does nothing and `cd` into a
// pinned project does not switch. Saying "intercepting correctly" and stopping
// let someone believe the whole thing worked.
func reportShellIntegration(fix bool) {
	shell := defaultShell()
	loadedHere := os.Getenv("NVX_SHELL_INTEGRATION") != ""
	profile := profilePathFor(shell)
	inProfile := profileLoadsIntegration(profile)

	if loadedHere && inProfile {
		LogSuccess("Shell integration is active, and loads in new shells too.")
		return
	}
	if profile == "" {
		LogInfo("Could not find this shell's profile, so nvx cannot tell whether version switching is set up.")
		LogInfo("The line to load it: %s", integrationLineFor(shell))
		return
	}

	if !inProfile && fix {
		if err := addIntegrationToProfile(profile, shell); err != nil {
			LogWarn("Could not add the shell integration to %s: %v", profile, err)
			LogInfo("Add this line yourself: %s", integrationLineFor(shell))
			return
		}
		LogSuccess("Added the shell integration to %s.", profile)
		LogInfo("It takes effect in new shells. For this one: %s", integrationLineFor(shell))
		return
	}
	if !inProfile {
		LogWarn("Version switching is not set up: %s does not load nvx.", profile)
		LogInfo("Until it does, 'nvx use' changes nothing and entering a project with a .nvmrc will not switch.")
		LogInfo("Run 'nvx doctor --fix' to add it, or add the line yourself: %s", integrationLineFor(shell))
		return
	}
	// In the profile but not in this shell: an existing terminal opened before
	// it was added, which is ordinary and needs no action beyond a new one.
	LogInfo("Shell integration is in %s but not loaded in this terminal; open a new one.", profile)
}
