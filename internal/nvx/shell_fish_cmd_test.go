package nvx

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// withParentShell names the process that "started" nvx for the test, the way a
// real run would see it from the process snapshot.
func withParentShell(t *testing.T, exe string) {
	t.Helper()
	orig := parentShellExe
	parentShellExe = func() string { return exe }
	t.Cleanup(func() { parentShellExe = orig })
}

// `nvx env --shell=fish` used to be "unknown shell". The script has to do what
// the zsh one does: front the shim dir, wrap `use` and `auto` so their output
// is evaluated, switch on every directory change, and switch once at load so a
// terminal opened inside a project does not wait for its first cd.
func TestTheFishSnippetCarriesTheIntegration(t *testing.T) {
	script := envScript("fish", "/opt/nvx/nvx", "/home/u/.nvx/bin")

	for _, want := range []string{
		"set -gx NVX_SHELL_INTEGRATION 1",
		"function nvx",
		"--shell=fish",
		"| source",
		"--on-variable PWD",
		"set -gx PATH $__nvx_bin $__nvx_rest",
		"'/home/u/.nvx/bin'",
		"command '/opt/nvx/nvx'",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("the fish snippet is missing %q:\n%s", want, script)
		}
	}
	// Run once at load: a line that is only the hook's name, after it is defined.
	defined := strings.Index(script, "function __nvx_auto")
	ranOnce := false
	if defined >= 0 {
		for _, line := range strings.Split(script[defined:], "\n") {
			if strings.TrimSpace(line) == "__nvx_auto" {
				ranOnce = true
			}
		}
	}
	if !ranOnce {
		t.Errorf("the fish hook is defined but never run at load:\n%s", script)
	}
	// Another dialect's syntax is not something fish can run.
	for _, bad := range []string{"export ", "eval ", "$(", "$env:", "Invoke-Expression"} {
		if strings.Contains(script, bad) {
			t.Errorf("the fish snippet contains %q, which fish cannot run:\n%s", bad, script)
		}
	}
}

// fish keeps PATH as a list, and a single quote or backslash in a path must not
// end the string early. In single quotes only those two characters are special.
func TestFishAssignmentsAreListsAndQuoted(t *testing.T) {
	got := shellEnvAssignment("fish", "PATH", "/a b/bin:/c'd/bin")
	if got != "set -gx PATH '/a b/bin' '/c\\'d/bin'\n" {
		t.Errorf("PATH assignment = %q", got)
	}
	if got := shellEnvAssignment("fish", "NPM_CONFIG_PREFIX", `/x\y`); got != "set -gx NPM_CONFIG_PREFIX '/x\\\\y'\n" {
		t.Errorf("single-value assignment = %q", got)
	}
}

// `nvx env --shell=cmd` is run by `FOR /f ... DO %i`, so every line must be
// a `set` that cmd runs as written, with the shim dir once and first.
func TestTheCmdSnippetIsSetLines(t *testing.T) {
	shim := `C:\Users\me\.nvx\bin`
	t.Setenv("PATH", `C:\Windows;`+shim+`\;C:\Tools;C:\USERS\ME\.NVX\BIN`)

	script := envScript("cmd", `C:\nvx\nvx.exe`, shim)

	want := `set "PATH=C:\Users\me\.nvx\bin;C:\Windows;C:\Tools"` + "\n"
	if script != want {
		t.Errorf("cmd snippet =\n%q\nwant\n%q", script, want)
	}
	// No function wrapper to define, and the marker must stay unset: it tells
	// `nvx use` that something evaluates its output, which in cmd nothing does.
	if strings.Contains(script, "NVX_SHELL_INTEGRATION") {
		t.Errorf("the cmd snippet sets the integration marker:\n%s", script)
	}
}

// The line is run as written, so a % is not doubled (measured in cmd.exe, a
// doubled %% stays doubled). A value cmd cannot quote is skipped as a comment
// instead of being emitted half-quoted.
func TestCmdSetLineKeepsTheValueLiteral(t *testing.T) {
	if got := shellEnvAssignment("cmd", "A", `C:\100%\x & (y)`); got != `set "A=C:\100%\x & (y)"`+"\n" {
		t.Errorf("percent and metacharacters: %q", got)
	}
	got := shellEnvAssignment("cmd", "A", `say "hi"`)
	if !strings.HasPrefix(got, "rem ") || strings.HasPrefix(got, "set ") {
		t.Errorf("a value holding a quote was not skipped: %q", got)
	}
}

// The parent process names the shell on Windows, ahead of inherited variables.
// A cmd.exe opened from Git Bash still has MSYSTEM set, and was handed POSIX
// `export` lines it cannot run.
func TestTheParentProcessDecidesTheShellOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the parent process is only consulted on Windows")
	}
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

// Off Windows only $SHELL can say, and only fish needs a different syntax.
func TestFishIsDetectedFromShellOffWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows reads the parent process")
	}
	for shell, want := range map[string]string{
		"/usr/bin/fish":          "fish",
		"/opt/homebrew/bin/fish": "fish",
		"/bin/zsh":               "bash",
		"":                       "bash",
	} {
		withEnv(t, "SHELL", shell)
		if got := defaultShell(); got != want {
			t.Errorf("SHELL=%q: defaultShell() = %q, want %q", shell, got, want)
		}
	}
}

// The hints name a command the reader can run in their own shell.
func TestFishAndCmdHints(t *testing.T) {
	if got := shellIntegrationHint("fish"); !strings.Contains(got, "nvx env --shell=fish | source") || !strings.Contains(got, "config.fish") {
		t.Errorf("fish hint: %q", got)
	}
	if got := evalHint("fish", "22"); got != "nvx use 22 | source" {
		t.Errorf("fish eval hint: %q", got)
	}
	want := `FOR /f "tokens=*" %i IN ('nvx use 22 --shell=cmd') DO %i`
	if got := evalHint("cmd", "22"); got != want {
		t.Errorf("cmd eval hint = %q, want %q", got, want)
	}
	if got := integrationLineFor("fish"); got != "nvx env --shell=fish | source" {
		t.Errorf("fish profile line: %q", got)
	}
	if got := shellPathFixLine("linux", "fish", "/home/u/.nvx/bin"); got != "set -gx PATH '/home/u/.nvx/bin' $PATH" {
		t.Errorf("fish PATH fix: %q", got)
	}
	if got := shellPathFixLine("windows", "cmd", `C:\u\.nvx\bin`); got != `set "PATH=C:\u\.nvx\bin;%PATH%"` {
		t.Errorf("cmd PATH fix: %q", got)
	}
}

// fish reads its own conf.d, which belongs to nvx, and follows XDG_CONFIG_HOME.
func TestFishProfileIsNvxsOwnConfDFile(t *testing.T) {
	home := tempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	if got, want := profilePathFor("fish"), filepath.Join(home, ".config", "fish", "conf.d", "nvx.fish"); got != want {
		t.Errorf("profilePathFor(fish) = %q, want %q", got, want)
	}
	xdg := tempDir(t)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	if got, want := profilePathFor("fish"), filepath.Join(xdg, "fish", "conf.d", "nvx.fish"); got != want {
		t.Errorf("with XDG_CONFIG_HOME: profilePathFor(fish) = %q, want %q", got, want)
	}
	if got := profilePathFor("cmd"); got != "" {
		t.Errorf("cmd has no profile, got %q", got)
	}
}

// `nvx use 20` in cmd printed POSIX exports and a ~/.bashrc instruction. Now a
// person at a cmd prompt is told what is true, and the for loop that reads the
// output gets set lines and no warning.
func TestUseInCmdExplainsInsteadOfPrintingPOSIX(t *testing.T) {
	nvxHome := tempDir(t)
	if err := os.MkdirAll(filepath.Join(nvxHome, "versions", "node", "v20.0.0"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NVX_SHELL_INTEGRATION", "")
	orig := stdoutIsTerminal
	t.Cleanup(func() { stdoutIsTerminal = orig })

	stdoutIsTerminal = func() bool { return true }
	var code int
	out := captureStderrHere(t, func() { code = runUse("20", nvxHome, "cmd", false) })
	if code == 0 {
		t.Error("`nvx use 20` at a cmd prompt changed nothing and exited 0")
	}
	for _, want := range []string{"cmd.exe", "nvx default 20", `FOR /f "tokens=*" %i IN ('nvx use 20 --shell=cmd') DO %i`} {
		if !strings.Contains(out, want) {
			t.Errorf("the cmd message lacks %q:\n%s", want, out)
		}
	}
	for _, bad := range []string{"bashrc", "export ", "eval"} {
		if strings.Contains(out, bad) {
			t.Errorf("the cmd message mentions %q:\n%s", bad, out)
		}
	}

	// Read through a pipe, which is what the for loop is: nothing to warn about.
	stdoutIsTerminal = func() bool { return false }
	var block string
	out = captureStderrHere(t, func() {
		block = captureStdout(t, func() { code = runUse("20", nvxHome, "cmd", false) })
	})
	if !strings.HasPrefix(block, `set "PATH=`) {
		t.Errorf("the for loop was given no set lines:\n%s", block)
	}
	if code != 0 || strings.Contains(out, "unchanged") {
		t.Errorf("piped into the for loop: exit %d, output:\n%s", code, out)
	}
}

// fish is not on every machine. When it is, it must at least parse the snippet:
// the text tests above cannot say whether fish accepts it.
func TestTheFishSnippetParsesUnderFish(t *testing.T) {
	fish, err := exec.LookPath("fish")
	if err != nil {
		t.Skip("fish is not installed here, so the snippet was not parsed by fish")
	}
	file := filepath.Join(tempDir(t), "nvx.fish")
	if err := os.WriteFile(file, []byte(envScript("fish", "/opt/nvx/nvx", "/home/u/.nvx/bin")), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(fish, "--no-execute", file).CombinedOutput(); err != nil {
		t.Errorf("fish rejected the snippet: %v\n%s", err, out)
	}
}
