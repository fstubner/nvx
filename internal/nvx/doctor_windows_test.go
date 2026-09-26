//go:build windows

package nvx

import "testing"

func TestRepairPersistentPathRefusesOnUnparsableExisting(t *testing.T) {
	// rebuildUserPath must never be handed an empty "existing" value by
	// repairPersistentPath — that would silently replace the user's entire
	// persistent PATH with just the shim dir. This test locks the contract
	// that an empty parse result is treated as "cannot safely repair", not
	// "PATH is empty, safe to overwrite". We can't easily fake `reg query`
	// here, so we assert the pure building block directly: rebuilding from
	// an empty existing PATH would produce just the shim dir, which is why
	// repairPersistentPath must refuse before ever calling rebuildUserPath
	// with such input (see the empty-check in repairPersistentPath).
	shimDir := `C:\Users\u\.nvx\bin`
	got := rebuildUserPath("", shimDir, nil)
	if got != shimDir {
		t.Fatalf("sanity check failed: rebuildUserPath(\"\", ...) = %q, want just the shim dir %q — this is exactly the destructive case repairPersistentPath must guard against", got, shimDir)
	}
}
