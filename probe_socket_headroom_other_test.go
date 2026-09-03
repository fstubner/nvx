//go:build !windows

package main

// probeSocketHeadroomProblem has nothing to check off Windows: the probes that
// bind a socket under NVX_HOME are the AppContainer ones, and they do not build
// here. See the Windows file for what it guards against.
//
// A separate file rather than a runtime.GOOS branch, because the Windows version
// names windowsEgressSocketPath and unixSocketPathMax, and referencing a
// Windows-only symbol from an untagged file compiles here and breaks the other
// two platforms' test builds -- `go build` does not compile test files, so it
// passes locally and fails in CI.
func probeSocketHeadroomProblem(string) string { return "" }
