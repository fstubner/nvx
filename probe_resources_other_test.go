//go:build !windows

package main

// hostMemoryNote has no non-Windows implementation. The exhaustion this reports
// on is a Windows commit-charge failure seen in the AppContainer probes, and
// those only build on Windows; adding a Linux/macOS reader would be writing for
// a caller that does not exist.
func hostMemoryNote() string { return "" }
