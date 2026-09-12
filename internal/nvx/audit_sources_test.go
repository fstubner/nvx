package nvx

import (
	"path/filepath"
	"testing"
)

// goSourcesForAudit returns every Go source the source-scanning audits must
// cover: this package, plus the command shim that sits outside it.
//
// Those audits globbed their own directory, which was the whole program when
// every file lived in the repository root. After the split, cmd/nvx/main.go sits
// outside it, and a net.Dial or a LogWarn added there would go unexamined -- a
// check that silently stops covering part of the program is worse than no check.
//
// Fails when it finds nothing rather than passing vacuously: run from the wrong
// directory, every one of these audits would otherwise report success having
// read no files at all.
func goSourcesForAudit(t *testing.T) []string {
	t.Helper()
	var all []string
	for _, pattern := range []string{"*.go", filepath.Join("..", "..", "cmd", "nvx", "*.go")} {
		files, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatalf("glob %s: %v", pattern, err)
		}
		all = append(all, files...)
	}
	if len(all) == 0 {
		t.Fatal("no Go sources found; the audit would scan nothing and pass")
	}
	return all
}
