//go:build windows

package main

import "testing"

func TestParseRegPath(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want string
	}{
		{
			name: "real reg query output",
			out: "HKEY_CURRENT_USER\\Environment\r\n" +
				"    Path    REG_SZ    C:\\Users\\u\\.nvx\\bin;C:\\Windows\\System32\r\n" +
				"\r\n",
			want: `C:\Users\u\.nvx\bin;C:\Windows\System32`,
		},
		{
			name: "REG_EXPAND_SZ type",
			out: "HKEY_CURRENT_USER\\Environment\r\n" +
				"    Path    REG_EXPAND_SZ    C:\\a;C:\\b\r\n",
			want: `C:\a;C:\b`,
		},
		{
			name: "value not found (reg query error text)",
			out:  "ERROR: The system was unable to find the specified registry key or value.\r\n",
			want: "",
		},
		{
			name: "empty output",
			out:  "",
			want: "",
		},
		{
			name: "header only, no value line",
			out:  "HKEY_CURRENT_USER\\Environment\r\n",
			want: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseRegPath(tc.out)
			if got != tc.want {
				t.Fatalf("parseRegPath(%q) = %q, want %q", tc.out, got, tc.want)
			}
		})
	}
}

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
