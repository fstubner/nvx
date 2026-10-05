//go:build windows

package nvx

import (
	"strings"
	"testing"
)

// bun's package manager fails with a bare EBADF in a contained project that is
// not on the system drive. The note that names the cause is keyed on three
// things, and each one missing must silence it, or it fires on every failed
// `bun test` and every failure on C:.
func TestBunEBADFNoteNeedsBunPackageManagerOffSystemDriveAndFailure(t *testing.T) {
	const sys = `C:\Windows\System32`
	cases := []struct {
		name    string
		command string
		args    []string
		workDir string
		exit    int
		want    bool
	}{
		{"install off the system drive", `C:\nvx\bun.exe`, []string{"install"}, `H:\proj`, 1, true},
		{"add, lower-case drive", "bun", []string{"add", "left-pad"}, `h:\proj`, 1, true},
		{"flag before the subcommand", "bun", []string{"--silent", "install"}, `D:\proj`, 1, true},
		{"pm", "bun.exe", []string{"pm", "ls"}, `D:\proj`, 1, true},
		{"succeeded", "bun", []string{"install"}, `H:\proj`, 0, false},
		{"on the system drive", "bun", []string{"install"}, `c:\proj`, 1, false},
		{"not a package-manager subcommand", "bun", []string{"test"}, `H:\proj`, 1, false},
		{"run", "bun", []string{"run", "build"}, `H:\proj`, 1, false},
		{"bunx fails differently", "bunx", []string{"cowsay"}, `H:\proj`, 1, false},
		{"npm", "npm", []string{"install"}, `H:\proj`, 1, false},
		{"UNC working directory", "bun", []string{"install"}, `\\server\share\proj`, 1, false},
		{"no arguments", "bun", nil, `H:\proj`, 1, false},
	}
	for _, c := range cases {
		if got := bunPackageManagerOffSystemDrive(c.command, c.args, c.workDir, sys, c.exit); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
	if bunPackageManagerOffSystemDrive("bun", []string{"install"}, `H:\proj`, `\\?\GLOBALROOT\x`, 1) {
		t.Error("an unreadable system directory must not produce the note")
	}
}

// The note names the real system drive and the escape hatch, once.
func TestBunEBADFNoteNamesSystemDriveAndEscapeHatch(t *testing.T) {
	sysDir, err := systemDirectory()
	if err != nil {
		t.Skip(err)
	}
	sysDrive, _ := driveLetterOf(sysDir)
	other := `Z:\proj`
	if sysDrive == 'Z' {
		other = `Y:\proj`
	}
	quietFlag = false
	out := captureStderr(t, func() { noteBunOffSystemDrive("bun", []string{"install"}, other, 1) })
	for _, want := range []string{string(sysDrive) + ":", "oven-sh/bun#38365", "nvx --no-sandbox bun", "EBADF"} {
		if !strings.Contains(out, want) {
			t.Errorf("note lacks %q:\n%s", want, out)
		}
	}
	if got := captureStderr(t, func() { noteBunOffSystemDrive("bun", []string{"install"}, other, 0) }); got != "" {
		t.Errorf("note printed for a successful run:\n%s", got)
	}
}
