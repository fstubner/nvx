package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
		// pwsh (PowerShell 7) has no fixed location and is found on PATH, as
		// the user would find it. Windows PowerShell has a fixed home under the
		// system directory and is taken from there, not from PATH: see
		// systemToolPath.
		candidates := []string{"pwsh"}
		if runtime.GOOS == "windows" {
			if p, err := systemToolPath(`WindowsPowerShell\v1.0\powershell.exe`); err == nil {
				candidates = append(candidates, p)
			}
		} else {
			candidates = append(candidates, "powershell")
		}
		for _, exe := range candidates {
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

// addIntegrationToProfile is a variable so tests can stop it touching the real
// machine, for the same reason repairPersistentPath is one.
//
// It appends to the profile of whichever shell the host actually uses, and a
// throwaway HOME does not reliably move that path. On Windows profilePathFor asks
// pwsh for $PROFILE, and whether pwsh follows USERPROFILE depends on the machine:
// a GitHub runner's does, while one whose Documents folder is redirected to
// OneDrive does not, and there the answer is the developer's real profile. A test
// cannot tell the two apart, so any test reaching runDoctor(home, true) could
// append outside its own temp tree -- and did, to CI's profile: the Windows job's
// unit-test step logged "Added the shell integration to
// C:\Users\runneradmin\Documents\PowerShell\Microsoft.PowerShell_profile.ps1",
// after which every later pwsh step in that job printed "The term 'nvx' is not
// recognized" while loading the line a test had planted.
//
// It went unnoticed on developer machines for the two reasons that make a local
// reproduction fail: MSYSTEM is set under Git Bash, so defaultShell() answers
// "bash" and the PowerShell branch never runs, and a developer's profile already
// loads nvx, so profileLoadsIntegration short-circuits before the write. Neither
// is protection -- both are accidents of the machine it was run on.
//
// The seam is at the write alone. Everything above it -- finding the profile,
// deciding whether the integration is present, and the report the user reads --
// still runs for real in tests.
var addIntegrationToProfile = addIntegrationToProfileImpl

// addIntegrationToProfileImpl appends the line, creating the file if needed.
func addIntegrationToProfileImpl(path, shell string) error {
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
