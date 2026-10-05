//go:build !windows

package nvx

import (
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"
	"unsafe"
)

// childTerminationGrace is how long a sandboxed child is given to exit on its
// own after nvx is asked to terminate, before it is killed outright.
const childTerminationGrace = 5 * time.Second

// beforeChildStart runs just before the child is started. Tests only.
var beforeChildStart func()

// runChildForwardingSignals starts cmd, forwards interrupt, terminate and
// hangup to it, and waits. Its return is exactly what cmd.Run would have returned.
//
// Without this, nvx exiting on a signal left the child running. macOS has no
// kill-on-parent-death primitive -- no job objects, no PID namespaces -- so the
// child of a `kill`ed nvx is reparented to launchd and keeps running, holding the
// terminal and, for a long-lived server, its port. Windows has the job object and
// Linux the PID namespace; macOS had neither this nor the process-group cleanup
// its own design document called for. The uncontained shim path had the same
// leak on every Unix: measured on Linux 2026-10-01, `kill <nvx>` left `node`
// running, and a node killed by a signal came back as exit 255.
//
// What it does NOT cover, and cannot: SIGKILL, which is not deliverable to a
// handler, so `kill -9 nvx` still orphans the child on macOS (Linux callers
// close that gap with killChildWhenParentDies); and the child's own
// descendants, which are signalled only if the child passes the signal on. The
// process group would cover the descendants, and is deliberately not used --
// putting the child in its own group takes it out of the terminal's foreground
// group, where reading stdin raises SIGTTIN and stops an interactive process
// (a `node` REPL) dead. Losing interactive use is a worse outcome than the leak.
//
// Interrupt is forwarded and then waited on, never escalated: at a terminal the
// child already has it (same process group), and for a REPL it means "abandon
// this line", not "exit". Killing it five seconds later because it was still
// running would be wrong. Terminate and hangup do escalate, because those do
// mean exit.
func runChildForwardingSignals(cmd *exec.Cmd) error {
	return startChildForwardingSignals(cmd, nil)
}

// forwardedSignals returns the signals to pass on, leaving out any this process
// was started ignoring.
//
// An ignored disposition is inherited across exec, and `nohup nvx node x.js`
// relies on that to keep node alive after the terminal closes. signal.Notify
// replaces an ignore with a handler, and handlers reset to the default in the
// child -- so notifying unconditionally would turn nohup's protection off for the
// child. An empty Notify would mean "every signal", so the caller must not call
// it with nothing.
func forwardedSignals() []os.Signal {
	var sigs []os.Signal
	for _, s := range []os.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP} {
		if !signal.Ignored(s) {
			sigs = append(sigs, s)
		}
	}
	return sigs
}

// terminalGaveChildTheInterrupt reports whether Ctrl-C at the terminal has
// already reached the child, which it has when stdin is a terminal whose
// foreground group is ours: the child is in that group too. Forwarding the
// interrupt again would deliver two, and a `node` REPL exits on the second
// consecutive one.
func terminalGaveChildTheInterrupt() bool {
	var pgrp int32
	_, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, os.Stdin.Fd(), syscall.TIOCGPGRP, uintptr(unsafe.Pointer(&pgrp)), 0, 0, 0)
	return errno == 0 && int(pgrp) == syscall.Getpgrp()
}

// startChildForwardingSignals is runChildForwardingSignals with a hook that
// receives the child's pid once it is running.
func startChildForwardingSignals(cmd *exec.Cmd, onStart func(pid int)) error {
	// Pdeathsig, where a caller asks for it, fires when the THREAD that forked
	// the child exits, not when the process does. The Go scheduler is free to
	// retire threads, so without this the child could be killed under a healthy
	// nvx. Held until the child has been waited for.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// Before Start, so a signal that lands while the child is starting waits in
	// the channel and is forwarded once it runs. Registered after Start, a
	// SIGTERM in between took the default action and killed nvx, which orphaned
	// the child this function exists to stop.
	sigs := make(chan os.Signal, 4)
	if watched := forwardedSignals(); len(watched) > 0 {
		signal.Notify(sigs, watched...)
	}
	if beforeChildStart != nil {
		beforeChildStart()
	}
	if err := cmd.Start(); err != nil {
		signal.Stop(sigs)
		return err
	}
	// Read once, here, and hand the value to the watcher. cmd.Process is written
	// by Start, so a watcher that read cmd.Process itself would be racing this
	// function -- caught by the race detector, and a real one: the watcher can
	// observe the field before Start has finished writing it.
	proc := cmd.Process
	if onStart != nil {
		onStart(proc.Pid)
	}
	done := make(chan struct{})
	go func() {
		for {
			select {
			case sig := <-sigs:
				if proc == nil {
					continue
				}
				if sig == syscall.SIGINT && terminalGaveChildTheInterrupt() {
					continue
				}
				_ = proc.Signal(sig)
				if sig == syscall.SIGTERM || sig == syscall.SIGHUP {
					go func() {
						select {
						case <-time.After(childTerminationGrace):
							_ = proc.Kill()
						case <-done:
						}
					}()
				}
			case <-done:
				return
			}
		}
	}()
	err := cmd.Wait()
	close(done)
	signal.Stop(sigs)
	return err
}
