//go:build !windows

package main

// watchStdinForHangup is a no-op off Windows.
//
// The orphan it exists to prevent is a Windows one: nvx blocks waiting on a
// contained child that ignores stdin EOF, and nothing else ends the wait. On
// Linux and macOS a process whose stdio is gone is reached by the ordinary
// signal path -- SIGHUP or SIGPIPE ends it -- and nvx's children are in its own
// process group, so the tree goes together. Adding a poll loop there would be
// machinery guarding against something that has not been observed.
func watchStdinForHangup(nvxHome string, onHangup func()) {}
