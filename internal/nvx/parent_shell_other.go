//go:build !windows

package nvx

// parentShellExe has no answer off Windows, where defaultShell reads SHELL.
// It exists so a test that names a parent compiles on every platform.
var parentShellExe = func() string { return "" }
