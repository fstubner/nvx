//go:build windows

package nvx

import "testing"

// The parent process names the shell on Windows, ahead of inherited variables.
// A cmd.exe opened from Git Bash still has MSYSTEM set, and was handed POSIX
// `export` lines it cannot run.
//
// Windows only by build tag, not by t.Skip: defaultShell reads the parent
// nowhere else, and a skip on the other platforms would trip the Windows CI
// guard against tests that verify nothing.
func TestTheParentProcessDecidesTheShellOnWindows(t *testing.T) {
	for _, tc := range []struct {
		name, parent, msystem, shell, want string
	}{
		{"cmd launched from git bash", "cmd.exe", "MINGW64", "/usr/bin/bash", "cmd"},
		{"cmd with nothing else set", "cmd.exe", "", "", "cmd"},
		{"upper-case name", "CMD.EXE", "", "", "cmd"},
		{"pwsh with a leaked SHELL", "pwsh.exe", "", "/usr/bin/bash", "powershell"},
		{"bash is the parent", "bash.exe", "", "", "bash"},
		{"fish is the parent", "fish.exe", "", "", "fish"},
		{"unknown parent falls back to MSYSTEM", "node.exe", "MINGW64", "", "bash"},
		{"unknown parent falls back to SHELL", "node.exe", "", "/usr/bin/fish", "fish"},
		{"no parent at all", "", "", "", "powershell"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withParentShell(t, tc.parent)
			withEnv(t, "MSYSTEM", tc.msystem)
			withEnv(t, "SHELL", tc.shell)
			if got := defaultShell(); got != tc.want {
				t.Errorf("defaultShell() = %q, want %q", got, tc.want)
			}
		})
	}
}
