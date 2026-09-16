package nvx

import (
	"os"
	"path/filepath"
	"testing"
)

// A plain version that is simply not installed must classify as "install it",
// not as "an expression nvx cannot read". runAuto and runUse branch on this to
// decide whether to offer a download, and the branch was silently skipped.
//
// The classifier decided by string prefix and recognised one shape,
// "no version matches", from highestMatching. resolveLocalVersion catches that
// error and re-wraps it as "no installed version matches query", which the
// classifier had never heard of, so every caller downstream took the wrong
// branch. A machine with versions 18 through 24 installed and an .nvmrc asking
// for 19 printed a warning and stopped where it should have offered to install.
//
// Same class of bug as errNoVersionsInstalled fixed once before, from the same
// cause: classification by exclusion on a string.
func TestMissingVersionIsNotAnUnsupportedRange(t *testing.T) {
	home := t.TempDir()
	// One installed version, so the "nothing installed" sentinel does not fire
	// and the query genuinely resolves to "not this one".
	if err := os.MkdirAll(filepath.Join(home, "versions", "node", "v20.11.0"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := resolveLocalVersion(NodeProvider{}, "19.9.0", home)
	if err == nil {
		t.Fatal("expected an error for a version that is not installed")
	}
	if isUnsupportedRange(err) {
		t.Fatalf("a missing version was classified as unreadable syntax, so the "+
			"install offer is skipped: %v", err)
	}
}

// The other direction has to keep working: an expression nvx really cannot
// parse must still be reported as such rather than as a missing download.
func TestUnreadableRangeIsStillUnsupported(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "versions", "node", "v20.11.0"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := resolveLocalVersion(NodeProvider{}, "node@18 - 24", home)
	if err == nil {
		t.Fatal("expected an error for an unreadable expression")
	}
	if !isUnsupportedRange(err) {
		t.Fatalf("an unreadable expression was classified as a missing version: %v", err)
	}
}
