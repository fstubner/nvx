package main

import (
	"os"
	"path/filepath"
	"testing"
)

// A policy file with a UTF-8 byte-order mark must load.
//
// Windows writes one by default -- Notepad does, and so does PowerShell's
// `Set-Content -Encoding utf8` on Windows PowerShell 5.1 -- and Go's JSON
// parser does not skip it. So a policy written the ordinary Windows way failed
// with "invalid character 'ï' looking for beginning of value", and since nvx
// refuses to run when it cannot read its own policy, the command was refused
// with a message about the policy rather than about the encoding.
//
// Driven through readProjectPolicyFile rather than through withoutUTF8BOM,
// because the defect was never in stripping the mark: it was that nothing
// stripped it on the path that reads the file.
func TestAPolicyFileWithAByteOrderMarkLoads(t *testing.T) {
	dir := tempDir(t)
	path := filepath.Join(dir, ".nvx-policy.json")
	body := []byte(`{"isolation":{"enabled":true,"level":"strict"}}`)
	if err := os.WriteFile(path, append([]byte{0xEF, 0xBB, 0xBF}, body...), 0644); err != nil {
		t.Fatal(err)
	}

	lp, data, err := readProjectPolicyFile(path)
	if err != nil {
		t.Fatalf("a policy file written the ordinary Windows way was rejected: %v", err)
	}
	if !lp.Isolation.Enabled {
		t.Fatal("the policy parsed but its settings were lost")
	}

	// The bytes handed back are what pins a trusted project policy and what
	// field-presence detection reads. A mark left on either would put the
	// failure one layer further in rather than removing it: EnabledSet is set
	// by re-parsing these bytes, and it decides whether the project's setting
	// overrides the global one at all.
	if !lp.Isolation.EnabledSet {
		t.Fatal("the setting was not marked as present, so it would not override the global policy")
	}
	if len(data) > 0 && data[0] == 0xEF {
		t.Fatal("the mark survived into the bytes used for the trust pin")
	}
}
