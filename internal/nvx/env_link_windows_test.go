//go:build windows

package nvx

import (
	"os"
	"path/filepath"
	"testing"
)

// `nvx default` used to make its junction with `cmd /c mklink /j`, which cmd
// splits on & even inside Go's quoting. A home directory with & in its path
// ran the text after it as a second command, and the failed mklink was
// reported as success because cmd exits with the last command's status.
func TestCreateLinkIsNotParsedByCmd(t *testing.T) {
	root := filepath.Join(tempDir(t), `a&md,marker&b`)
	target := filepath.Join(root, "versions", "node", "v20.0.0")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "probe"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// cmd would write the marker relative to its working directory.
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	link := filepath.Join(root, "current")
	if err := CreateLink(link, target); err != nil {
		t.Fatalf("CreateLink: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "marker")); err == nil {
		t.Error("text after & in the path ran as a command")
	}
	if _, err := os.Stat(filepath.Join(link, "probe")); err != nil {
		t.Errorf("the link does not reach the target: %v", err)
	}
	if got := getGlobalDefaultVersion(root); got != "v20.0.0" {
		t.Errorf("getGlobalDefaultVersion through the junction = %q, want v20.0.0", got)
	}

	// Replacing it must work, since `nvx default` runs again on every switch.
	other := filepath.Join(root, "versions", "node", "v22.0.0")
	if err := os.MkdirAll(other, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := CreateLink(link, other); err != nil {
		t.Fatalf("replacing the link: %v", err)
	}
	if got := getGlobalDefaultVersion(root); got != "v22.0.0" {
		t.Errorf("after replacing, getGlobalDefaultVersion = %q, want v22.0.0", got)
	}
	if _, err := os.Stat(filepath.Join(target, "probe")); err != nil {
		t.Errorf("replacing the link touched the old target: %v", err)
	}
}
