//go:build windows

package nvx

import (
	"strings"
	"testing"
)

// Doctor reports what an older `nvx setup` left as something `nvx setup` removes,
// and never as grants that are missing.
//
// It used to FAIL, and make doctor exit non-zero, when a recorded grant was no
// longer on disk, and told the person to run `nvx setup` to put it back. Nothing
// needs the grant, so that sent people to an elevated ACL write to restore
// something nvx does not use.
func TestDoctorReportsLeftoverSetupAccessAsRemovable(t *testing.T) {
	out := captureStdout(t, func() {
		reportSetupLeftoversWith(tempDir(t), tempDir(t), func(sid, path string) bool { return true })
	})
	if !strings.Contains(out, "[note]") || strings.Contains(out, "[FAIL]") {
		t.Errorf("leftover access should be a note, not a failure:\n%s", out)
	}
	if !strings.Contains(out, "nvx setup") || !strings.Contains(out, "remove") {
		t.Errorf("the note does not say that `nvx setup` removes it:\n%s", out)
	}
	if strings.Contains(out, "re-run") || strings.Contains(out, "grants it") {
		t.Errorf("the note asks for a grant instead of a removal:\n%s", out)
	}
}

// A machine an older setup never touched, or that already ran this one, gets
// no line at all.
func TestDoctorSaysNothingAboutSetupWhenNothingIsLeft(t *testing.T) {
	out := captureStdout(t, func() {
		reportSetupLeftoversWith(tempDir(t), tempDir(t), func(sid, path string) bool { return false })
	})
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected silence, got:\n%s", out)
	}
}
