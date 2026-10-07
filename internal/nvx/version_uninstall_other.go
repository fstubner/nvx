//go:build !windows

package nvx

// processesRunningFrom finds nothing outside Windows. A running executable can
// be deleted there, and the process keeps its open copy.
func processesRunningFrom(dir string) []runningProcess { return nil }
