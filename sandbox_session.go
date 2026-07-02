package main

import "sync/atomic"

var sandboxSessionActive int32

func enterSandboxSession() {
	atomic.AddInt32(&sandboxSessionActive, 1)
}

func inSandboxSession() bool {
	return atomic.LoadInt32(&sandboxSessionActive) > 0
}

func leaveSandboxSession() {
	atomic.AddInt32(&sandboxSessionActive, -1)
}
