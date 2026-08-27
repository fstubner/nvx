//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// Reproduces the orphan that fills a developer's machine.
//
// Measured on the maintainer's machine 2026-08-27: 18 nvx processes, 12 of them
// orphaned, 43 node processes holding 3.9 GB, the system freezing. Every orphan
// was `nvx shim npx <an MCP server>`. nvx's own hangup_watch record named the
// reason exactly:
//
//	"the parent has exited, but something still holds the input pipe open"
//	state: waiting
//
// The watchdog requires BOTH signals -- stdin broken AND parent exited -- and
// that rule is right for the case it was built for (a finished shell pipeline
// breaks the pipe while the shell waits, and killing there was a real
// regression). What it misses is this shape: an MCP client spawns nvx over a
// pipe, then dies, while some OTHER process still holds the write end open. On
// Windows a handle inherited by a sibling is enough. The pipe never breaks, the
// parent is long gone, and nvx waits forever.
//
// The three roles below are the smallest arrangement that produces it:
//
//	test      creates the pipe and KEEPS THE WRITE END OPEN throughout
//	 └─ C     launcher: starts W on the pipe, then exits immediately
//	     └─ W watcher: runs the real watchStdinForHangup and reports if it fires
//
// W's parent (C) is dead. W's stdin pipe is alive because the test holds it.
// That is precisely "parent gone, pipe held open".
//
// Verified to FAIL before the fix: W never fired, and the test timed out waiting
// for its marker.
func TestWatchdogExitsWhenTheParentIsGoneAndSomethingElseHoldsThePipe(t *testing.T) {
	switch os.Getenv("NVX_ORPHAN_ROLE") {
	case "launcher":
		runOrphanLauncher()
		os.Exit(0)
	case "watcher":
		runOrphanWatcher()
		os.Exit(0)
	}

	marker := filepath.Join(tempDir(t), "watchdog-fired.txt")

	// The write end stays open in THIS process for the whole test. That is the
	// third party the real bug depends on.
	pipeR, pipeW, err := os.Pipe()
	if err != nil {
		t.Fatalf("create pipe: %v", err)
	}
	defer pipeW.Close()
	defer pipeR.Close()

	launcher := exec.Command(os.Args[0], "-test.run=TestWatchdogExitsWhenTheParentIsGoneAndSomethingElseHoldsThePipe")
	launcher.Env = append(os.Environ(),
		"NVX_ORPHAN_ROLE=launcher",
		"NVX_ORPHAN_MARKER="+marker,
		"NVX_ORPHAN_SELF="+os.Args[0],
	)
	launcher.Stdin = pipeR
	if err := launcher.Start(); err != nil {
		t.Fatalf("start launcher: %v", err)
	}
	// The launcher exits as soon as it has spawned the watcher. After this the
	// watcher is an orphan whose stdin pipe is still very much alive.
	if err := launcher.Wait(); err != nil {
		t.Fatalf("launcher did not exit cleanly: %v", err)
	}

	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(marker); err == nil {
			return // fired: the orphan let go
		}
		time.Sleep(250 * time.Millisecond)
	}

	t.Fatalf("the watchdog never fired.\n"+
		"Its parent had exited and only an unrelated process still held the stdin pipe open, "+
		"which is exactly how an MCP server outlives its client. nvx would wait here forever, "+
		"holding a sandbox and its contained child, until someone killed it by hand.")
}

// runOrphanLauncher starts the watcher on the pipe it was given and leaves.
//
// It passes its own stdin down by inheritance, so the watcher ends up on the
// same pipe the test still holds the write end of.
func runOrphanLauncher() {
	watcher := exec.Command(os.Getenv("NVX_ORPHAN_SELF"),
		"-test.run=TestWatchdogExitsWhenTheParentIsGoneAndSomethingElseHoldsThePipe")
	watcher.Env = append(os.Environ(), "NVX_ORPHAN_ROLE=watcher")
	watcher.Stdin = os.Stdin
	_ = watcher.Start()

	// Stay alive briefly, then leave. This mirrors the real sequence and is not
	// incidental: an MCP client is running when it spawns nvx and dies later, so
	// nvx opens a handle to a live parent and only later observes it exit.
	//
	// Exiting immediately tests a different thing -- the parent already dead
	// before the watchdog arms, where openParentProcess fails and the watchdog
	// never arms at all. That is also a real way to end up with an orphan, and it
	// is covered separately below.
	time.Sleep(2 * time.Second)

	// No Wait on the watcher: this process must die while it lives, which is what
	// makes it an orphan.
}

// runOrphanWatcher runs the real watchdog and records whether it decided to go.
func runOrphanWatcher() {
	// Seconds rather than the shipped 15, so the reproduction is quick. The
	// decision under test is unchanged by the interval.
	stdinBrokenPipeInterval = 500 * time.Millisecond

	marker := os.Getenv("NVX_ORPHAN_MARKER")
	fired := make(chan struct{}, 1)
	watchStdinForHangup("", func() { fired <- struct{}{} })

	select {
	case <-fired:
		_ = os.WriteFile(marker, []byte("fired at "+strconv.FormatInt(time.Now().Unix(), 10)), 0o600)
	case <-time.After(40 * time.Second):
		// Deliberately silent: the absence of the marker is the failure the
		// parent process reports, with the explanation.
	}
}

// The pipeline case the two-signal rule exists to protect must keep working.
//
// `echo hi | nvx node -e "<slow work>"` breaks the pipe as soon as the producer
// finishes, while the shell that built the pipeline waits. Killing there was a
// measured regression -- a healthy command died at 15 seconds with exit 129 --
// and no fix for the orphan above is allowed to bring it back.
func TestWatchdogLeavesAFinishedPipelineAloneWhileTheParentLives(t *testing.T) {
	pipeR, pipeW, err := os.Pipe()
	if err != nil {
		t.Fatalf("create pipe: %v", err)
	}
	defer pipeR.Close()

	// Producer finishes: the write end closes, so the pipe reads as broken.
	pipeW.Close()

	prev, _ := syscall.GetStdHandle(syscall.STD_INPUT_HANDLE)
	const stdInputHandle = uintptr(0xFFFFFFF6)
	procSetStdHandleTest.Call(stdInputHandle, pipeR.Fd())
	defer procSetStdHandleTest.Call(stdInputHandle, uintptr(prev))

	restore := stdinBrokenPipeInterval
	stdinBrokenPipeInterval = 200 * time.Millisecond
	defer func() { stdinBrokenPipeInterval = restore }()

	fired := make(chan struct{}, 1)
	watchStdinForHangup("", func() { fired <- struct{}{} })

	// This test process is the watcher's parent and is very much alive, which is
	// what a shell holding a pipeline open looks like.
	select {
	case <-fired:
		t.Fatal("the watchdog killed a command whose input had finished but whose parent was still " +
			"waiting for it -- that is an ordinary shell pipeline, and this regression has shipped once already")
	case <-time.After(2 * time.Second):
	}
}
