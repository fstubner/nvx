//go:build !windows

package main

import (
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

// childTerminationGrace is how long a sandboxed child is given to exit on its
// own after nvx is asked to terminate, before it is killed outright.
const childTerminationGrace = 5 * time.Second

// runChildForwardingSignals starts cmd, forwards interrupt and terminate to it,
// and waits. Its return is exactly what cmd.Run would have returned.
//
// Without this, nvx exiting on a signal left the sandboxed process running.
// macOS has no kill-on-parent-death primitive -- no job objects, no PID
// namespaces -- so the child of a `kill`ed nvx is reparented to launchd and
// keeps running, holding the terminal and, for a long-lived server, its port.
// Windows has the job object and Linux the PID namespace; macOS had neither
// this nor the process-group cleanup its own design document called for.
//
// What it does NOT cover, and cannot: SIGKILL, which is not deliverable to a
// handler, so `kill -9 nvx` still orphans the child; and the child's own
// descendants, which are signalled only if the child passes the signal on. The
// process group would cover the descendants, and is deliberately not used --
// putting the child in its own group takes it out of the terminal's foreground
// group, where reading stdin raises SIGTTIN and stops an interactive process
// (a `node` REPL) dead. Losing interactive use is a worse outcome than the leak.
//
// Interrupt is forwarded and then waited on, never escalated: at a terminal the
// child already has it (same process group), and for a REPL it means "abandon
// this line", not "exit". Killing it five seconds later because it was still
// running would be wrong. Terminate does escalate, because that one does mean
// exit.
func runChildForwardingSignals(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case sig := <-sigs:
				proc := cmd.Process
				if proc == nil {
					continue
				}
				_ = proc.Signal(sig)
				if sig == syscall.SIGTERM {
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
