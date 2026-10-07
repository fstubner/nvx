//go:build windows

package nvx

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

// A batch file runs inside the sandbox only when cmd.exe is given its path in
// quotes.
//
// Windows runs a .cmd or .bat by starting `cmd.exe /c <command line>`, and
// inside an AppContainer cmd.exe answers an unquoted absolute path with
// "Access is denied." Measured 2026-10-07 on Windows 11 build 26300, with a
// batch file in a working directory the container may read and write:
//
//	cmd /d /c C:\...\x.cmd                    Access is denied.
//	cmd /d /c "C:\...\x.cmd"                  Access is denied. (cmd drops the quotes)
//	cmd /d /c C:\Windows\System32\whoami.exe  Access is denied.
//	cmd /d /s /c ""C:\...\x.cmd" a "b c" d"   runs
//	cmd /d /c .\x.cmd                         runs
//	cmd /d /c call "C:\...\x.cmd"             runs
//	cmd /d /c Q:\x.cmd                        runs, with Q: a subst drive for the same folder
//
// The subst drive is the telling case. The same file runs when it is named
// from a root whose every folder the container may list. Named from C:\, it
// needs list access on C:\, C:\Users and each folder below them, and the
// container has it on none of them. Granting that would need Administrator
// rights on C:\ and C:\Users, and would let every sandbox list the folders
// inside your profile. A quoted path needs neither. pnpm and yarn installed
// with `npm install -g`, and project bin shims under strict isolation, are
// batch files nvx launches by full path, so every one of them failed this way.
//
// Windows' own handling of a batch file cannot be steered into the quoted
// form. It passes the caller's command line to `cmd.exe /c` without /s, and
// cmd.exe then removes the quote in front of the path along with the last
// quote on the line. Measured the same day, CreateProcess on the batch file
// failed with the path quoted in the command line too. So nvx starts cmd.exe
// itself.

// isWindowsBatchFile reports whether path names a batch file.
func isWindowsBatchFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".cmd", ".bat":
		return true
	}
	return false
}

// windowsBatchLaunch returns the cmd.exe to start and the whole command line
// that runs batch with args, with the batch file's path in quotes. Contained
// and uncontained launches of a batch file both use it.
//
// /s makes cmd.exe remove exactly the outer pair of quotes, so the pair around
// the path survives whatever the arguments hold. /d skips AutoRun commands from
// the registry, as npm does for the scripts it runs. /e:ON turns on the
// extensions appendBatchArg relies on for %, and /v:OFF keeps !VAR! as it is.
//
// An argument holding a line break is refused, because cmd.exe ends the command
// there and drops the rest.
func windowsBatchLaunch(batch string, args []string) (exe, cmdLine string, err error) {
	exe, err = systemToolPath("cmd.exe")
	if err != nil {
		return "", "", err
	}
	var b strings.Builder
	b.WriteString(quoteWindowsArg(exe) + ` /d /e:ON /v:OFF /s /c ""` + batch + `"`)
	for _, a := range args {
		if strings.ContainsAny(a, "\r\n") {
			return "", "", fmt.Errorf("%s cannot be given an argument that holds a line break, because cmd.exe would drop everything after it", filepath.Base(batch))
		}
		b.WriteByte(' ')
		appendBatchArg(&b, a)
	}
	b.WriteByte('"')
	return exe, b.String(), nil
}

// appendBatchArg writes arg to a batch file's command line so that cmd.exe
// treats none of it as syntax, and a program the batch file passes %* to reads
// it back unchanged.
//
// cmd.exe reads & | < > ^ ( ) as syntax outside double quotes, and expands
// %VAR% inside them too. Quoting only arguments with a space, and escaping a
// quote as \", which cmd.exe does not know, let an argument such as
// x&echo.INJECTED>file run a second command. Go's os/exec has no escaping for
// batch files. Its documentation says they parse arguments differently and
// leaves the command line to the caller.
//
// This is append_bat_arg from Rust's standard library, which Rust added in
// 1.77.2 to fix CVE-2024-24576, ported from library/std/src/sys/args/windows.rs
// at rust-lang/rust tag 1.88.0, where it is unchanged. An argument is quoted
// unless every character is a letter, a digit or one of #$*+-./:?@\_. A quote
// inside it is doubled, which keeps cmd.exe's count of quotes even. Each %
// becomes %%cd:~,%. cmd.exe expands %cd:~,% to nothing, and in that pass the %
// before it starts no variable name, so %PATH% reaches the batch file as
// written.
func appendBatchArg(b *strings.Builder, arg string) {
	quote := arg == "" || strings.HasSuffix(arg, `\`)
	for _, r := range arg {
		ascii := r < utf8.RuneSelf
		plain := 'a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' || '0' <= r && r <= '9' || strings.ContainsRune(`#$*+-./:?@\_`, r)
		if ascii && !plain || unicode.IsControl(r) {
			quote = true
		}
	}
	if quote {
		b.WriteByte('"')
	}
	backslashes := 0
	for i := 0; i < len(arg); i++ {
		c := arg[i]
		if c == '\\' {
			backslashes++
		} else {
			if c == '"' {
				// The backslashes before a quote are doubled, so they stay
				// backslashes, and the quote is doubled.
				b.WriteString(strings.Repeat(`\`, backslashes))
				b.WriteByte('"')
			} else if c == '%' || c == '\r' {
				b.WriteString(`%%cd:~,`)
			}
			backslashes = 0
		}
		b.WriteByte(c)
	}
	if quote {
		b.WriteString(strings.Repeat(`\`, backslashes))
		b.WriteByte('"')
	}
}
