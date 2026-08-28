//go:build linux

package main

import "testing"

func TestReadExecRootsAreNeverWritable(t *testing.T) {
	// The claim is that a read/execute root cannot be written to, whatever else a
	// policy says. Assert it against the access mask the Landlock rule is actually
	// built from, which is what enforces it.
	//
	// The previous version of this test could not fail. It checked that a third,
	// unrelated temp directory was absent from sandboxWritableRoots(guest, work) --
	// a function whose only inputs are guest and work, so the answer was always no
	// regardless of what the read-exec code did. Proven by an acceptance pass on
	// 2026-08-28: with the enforcement path deliberately given write permissions,
	// it still passed. Its comment also claimed the Windows side was covered by the
	// enforcement probe; no probe mentions this feature, and that claim is gone
	// rather than restated.
	writeBearing := map[string]uint64{
		"write file":  landlockAccessFSWriteFile,
		"create file": landlockAccessFSMakeReg,
		"create dir":  landlockAccessFSMakeDir,
		"remove file": landlockAccessFSRemoveFile,
		"remove dir":  landlockAccessFSRemoveDir,
		"truncate":    landlockAccessFSTruncate,
	}
	for name, bit := range writeBearing {
		if landlockAccessReadExec&bit != 0 {
			t.Errorf("the read/execute access mask grants %q; a read-exec root must never be writable", name)
		}
	}

	// And it must still actually grant reading and executing, or the feature is
	// enforced into uselessness.
	for name, bit := range map[string]uint64{
		"execute":   landlockAccessFSExecute,
		"read file": landlockAccessFSReadFile,
	} {
		if landlockAccessReadExec&bit == 0 {
			t.Errorf("the read/execute access mask does not grant %q", name)
		}
	}
}
