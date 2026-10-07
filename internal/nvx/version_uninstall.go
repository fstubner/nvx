package nvx

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// removeInstalledVersion deletes an installed runtime version, or refuses and
// leaves it whole. label and version are for messages, as in "Node.js v20.20.2".
//
// Windows will not delete a running executable. A plain RemoveAll of a version
// whose node.exe was running deleted every other file, then stopped on node.exe
// with "Access is denied". The version was still listed, and `nvx install` said
// it was already installed. Measured 2026-10-07 on Windows 11 with v20.20.2's
// node.exe running, 1962 files before the uninstall and 1 after.
//
// So a version something is running from is refused before anything is
// deleted. And the directory is renamed out of the versions listing before the
// delete, so any other file that will not go leaves an uninstalled version with
// leftovers. The new name is an install's staging name, which `nvx list`
// already skips and `nvx cleanup` and the next install already sweep once this
// process has exited.
func removeInstalledVersion(label, version, versionDir string) error {
	if running := processesRunningFrom(versionDir); len(running) > 0 {
		return fmt.Errorf("refusing to uninstall %s %s because %s running from it. Stop it and run the uninstall again",
			label, version, describeRunningProcesses(running))
	}
	removing := versionDir + ".tmp." + strconv.Itoa(os.Getpid())
	if err := os.Rename(versionDir, removing); err != nil {
		return fmt.Errorf("could not uninstall %s %s, so it is still installed: %w", label, version, err)
	}
	if err := os.RemoveAll(removing); err != nil {
		LogWarn("%s %s is uninstalled, but some of its files could not be deleted yet: %v", label, version, err)
		LogInfo("They are in %s. 'nvx cleanup' removes them once nothing is using them.", removing)
	}
	return nil
}

// runningProcess is a process whose executable lives in a directory being
// removed.
type runningProcess struct {
	Name string
	PID  uint32
}

// describeRunningProcesses names the processes for a refusal, with the verb,
// as in "node.exe (process 1234) is" or "node.exe (processes 1234, 5678) are".
func describeRunningProcesses(procs []runningProcess) string {
	byName := map[string][]string{}
	var names []string
	for _, p := range procs {
		key := filepath.Base(p.Name)
		if _, seen := byName[key]; !seen {
			names = append(names, key)
		}
		byName[key] = append(byName[key], strconv.FormatUint(uint64(p.PID), 10))
	}
	parts := make([]string, 0, len(names))
	for _, n := range names {
		pids := byName[n]
		word := "process"
		if len(pids) > 1 {
			word = "processes"
		}
		parts = append(parts, fmt.Sprintf("%s (%s %s)", n, word, strings.Join(pids, ", ")))
	}
	verb := "is"
	if len(procs) > 1 {
		verb = "are"
	}
	return strings.Join(parts, ", ") + " " + verb
}
