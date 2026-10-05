package nvx

import "os"

// stderrIsTerminal is stdoutIsTerminal for stderr, which is where the Log
// helpers and the download progress write. A variable so a test can answer for
// it.
var stderrIsTerminal = func() bool {
	info, err := os.Stderr.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// colorOn reports whether colour codes belong on a stream: not when NO_COLOR is
// set to anything (no-color.org), and not when the stream is a pipe or a file,
// where the escapes arrive as literal text in a log or in whatever reads it.
func colorOn(isTerminal func() bool) bool {
	return os.Getenv("NO_COLOR") == "" && isTerminal()
}

// paint wraps s in an SGR colour for stderr, or returns it unchanged where
// colour is off. The message text is never altered.
func paint(code, s string) string {
	if !colorOn(stderrIsTerminal) {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

// paintOut is paint for stdout.
func paintOut(code, s string) string {
	if !colorOn(stdoutIsTerminal) {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}
