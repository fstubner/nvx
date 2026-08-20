//go:build windows

package main

import (
	"syscall"
	"testing"
)

// Nothing verified that nvx actually uses the F46 stdio fix, and this closes that.
//
// The gap, found 2026-08-20 while trying to prove a new MCP test caught the F46
// regression. Two tests look like they cover it and neither does:
//
//   - TestPipedStdioReachesChildOnlyWhenBothFlagsSet proves the Win32 semantics --
//     STARTF_USESTDHANDLES and bInheritHandles are both required -- but builds its
//     own STARTUPINFO from test parameters. It never calls production code, so it
//     cannot fail if nvx stops setting the flags.
//   - TestPipedStdioReachesRealAppContainerChild does drive the real launcher, but
//     cannot detect their absence: with STARTF_USESTDHANDLES and bInheritHandles
//     disabled, and then with prepareInheritableStdio disabled entirely, a
//     contained child still received piped stdio on this host. Verified in both
//     proxy mode (supervisor in the path) and open mode (nvx launching the child
//     directly), with containment confirmed active in each.
//
// So on this Windows build the machinery is not load-bearing, and no end-to-end
// test on this machine can guard it. That is a statement about this host, not
// about every host -- F46 was a measured failure where every MCP server broke, so
// the machinery is not being removed on one machine's evidence.
//
// What is guardable is that nvx still tries. This asserts the decision function
// reports success in an ordinary environment, which fails if someone deletes or
// short-circuits it -- the realistic regression, and exactly what was simulated
// above to expose the gap.
func TestPrepareInheritableStdioReportsSuccessInANormalEnvironment(t *testing.T) {
	// Guard the premise rather than assume it: with no usable standard handles the
	// function is correct to report false, and asserting true would be wrong.
	for _, h := range []int{syscall.STD_INPUT_HANDLE, syscall.STD_OUTPUT_HANDLE, syscall.STD_ERROR_HANDLE} {
		handle, err := syscall.GetStdHandle(h)
		if err != nil || handle == 0 || handle == syscall.InvalidHandle {
			t.Skipf("this environment has no usable standard handle %d; the case under test cannot arise", h)
		}
	}

	stdio := prepareInheritableStdio()
	if !stdio.inheritable {
		t.Fatal("prepareInheritableStdio reported the standard handles as not inheritable in an " +
			"ordinary environment. nvx then launches with neither STARTF_USESTDHANDLES nor " +
			"bInheritHandles, which is the F46 failure: a piped child gets no stdio, so every " +
			"MCP server breaks while terminal use still looks healthy.")
	}
	if stdio.in == 0 || stdio.out == 0 || stdio.err == 0 {
		t.Errorf("reported inheritable but carries a zero handle: in=%v out=%v err=%v",
			stdio.in, stdio.out, stdio.err)
	}
}
