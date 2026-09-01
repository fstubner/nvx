//go:build !windows

package main

// superviseDirectChild is a no-op off Windows.
//
// Same reasoning as watchStdinForHangup in sandbox_parent_watch_other.go: the
// orphan this reaps is a Windows one. There, nvx blocks on a child that ignores
// stdin EOF and nothing ends the wait, and a child left behind has no signal
// reaching it. On Linux and macOS nvx's children are in its own process group,
// so a hangup or a kill reaches the whole tree through the ordinary signal path
// and the child does not outlive nvx.
//
// Left as a no-op rather than reimplemented with process groups because the leak
// has not been observed there -- machinery guarding a problem nobody has
// measured is the thing this project keeps removing.
func superviseDirectChild(pid int) (cleanup func()) {
	return func() {}
}
