//go:build !windows

package nvx

import (
	"os"
	"path/filepath"
	"testing"
)

// npm and bun start from the real folder, so a cd through a link is judged where
// it leads. Getwd answers with the path the shell used.
func TestACdThroughALinkIsJudgedWhereItReallyIs(t *testing.T) {
	base := tempDir(t)
	project := filepath.Join(base, "project")
	other := filepath.Join(base, "other")
	writeTestFile(t, filepath.Join(project, "package.json"), `{"name":"project"}`, 0o644)
	writeNpmBin(t, filepath.Join(project, "node_modules", ".bin"), "vitest")
	writeTestFile(t, filepath.Join(project, "src", "app.ts"), "", 0o644)
	writeTestFile(t, filepath.Join(other, "package.json"), `{"name":"other"}`, 0o644)
	writeTestFile(t, filepath.Join(other, "inner", "file.txt"), "", 0o644)
	if err := os.Symlink(filepath.Join(other, "inner"), filepath.Join(project, "link")); err != nil {
		t.Fatalf("cannot make a symlink: %v", err)
	}
	if err := os.Symlink(project, filepath.Join(base, "alias")); err != nil {
		t.Fatalf("cannot make a symlink: %v", err)
	}

	cd := func(dir string) {
		inProjectDir(t, dir)
		t.Setenv("PWD", dir)
	}
	cd(filepath.Join(base, "alias", "src"))
	checkClasses(t, []classRow{
		{"a folder reached through a link to the project", "npx", []string{"vitest"}, classYourCode},
	})
	// Under the project's own path, but really inside another project that has
	// no tools. Read upward by the path typed, the folder above is the project.
	cd(filepath.Join(project, "link"))
	checkClasses(t, []classRow{
		{"a link in the project that leads into another project", "npx", []string{"vitest"}, classAdHocTool},
	})
}
