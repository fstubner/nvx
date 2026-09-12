package nvx

import (
	"os"
	"strings"
)

// droppedPathEntries lists the entries of existing that fixed no longer holds,
// other than shimDir, which the repair moves rather than removes.
//
// `nvx doctor --fix` rebuilds the User PATH with the shim directory first and
// every raw-runtime directory under ~/.nvx dropped. Dropping them is the fix;
// not saying so was the defect: a PATH shorter by three entries with nothing to
// say what went, or that it was deliberate, or why.
func droppedPathEntries(existing, fixed, shimDir string) []string {
	sep := string(os.PathListSeparator)
	keep := map[string]bool{}
	for _, e := range strings.Split(fixed, sep) {
		keep[strings.ToLower(strings.TrimSpace(e))] = true
	}
	var dropped []string
	for _, e := range strings.Split(existing, sep) {
		if strings.TrimSpace(e) == "" || dirsEqual(e, shimDir) {
			continue
		}
		if !keep[strings.ToLower(strings.TrimSpace(e))] {
			dropped = append(dropped, e)
		}
	}
	return dropped
}
