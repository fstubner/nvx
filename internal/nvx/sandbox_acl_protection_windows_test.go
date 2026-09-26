//go:build windows

package nvx

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// A permission write must leave a directory's inheritance protection as it
// found it.
//
// Every write switched inheritance back on until 2026-09-26. Windows ships
// C:\Users and each profile folder protected, so they do not take C:\'s
// "Authenticated Users: Modify" for subfolders; one grant from `nvx setup`, or
// one traverse grant on a profile, lifted that, and every signed-in account on
// the machine could modify the whole profile. The check reads protection back
// through PowerShell rather than through nvx's own reader, so a bug shared by
// the writer and the reader cannot hide itself.
func TestPermissionWritesKeepInheritanceProtection(t *testing.T) {
	const sid = "S-1-15-3-1024-1111111111-2222222222-3333333333-4444444444-5555555555-6666666666-7777777777"
	writes := []struct {
		name  string
		write func(dir string) error
	}{
		{"inheritable grant", func(d string) error { return grantACL(d, sid, aclMaskReadExec, nvxInheritFlags) }},
		{"this-folder grant", func(d string) error { return grantACL(d, sid, aclMaskTraverse, 0) }},
		{"traverse entry", func(d string) error { return writeThisFolderEntry(d, sid, aclMaskTraverse) }},
		{"revoke", func(d string) error {
			if err := grantACL(d, sid, aclMaskTraverse, 0); err != nil {
				return err
			}
			return revokeACL(d, sid)
		}},
	}
	for _, w := range writes {
		for _, protected := range []bool{true, false} {
			d := tempDir(t)
			if protected {
				if out, err := exec.Command("icacls", d, "/inheritance:d").CombinedOutput(); err != nil {
					t.Fatalf("protect %s: %v %s", d, err, out)
				}
			}
			if got := psDACLProtected(t, d); got != protected {
				t.Fatalf("test setup: %s protected=%v, want %v", d, got, protected)
			}
			if err := w.write(d); err != nil {
				t.Fatalf("%s: %v", w.name, err)
			}
			if got := psDACLProtected(t, d); got != protected {
				t.Errorf("%s changed inheritance protection from %v to %v", w.name, protected, got)
			}
		}
	}
}

func psDACLProtected(t *testing.T, dir string) bool {
	t.Helper()
	// .NET directly rather than Get-Acl: Get-Acl lives in a module, and a Windows
	// PowerShell started from pwsh -- as CI's steps are -- inherits a module path
	// it cannot load that module from, and fails.
	cmd := exec.Command("powershell", "-NoProfile", "-Command",
		"(New-Object System.IO.DirectoryInfo '"+strings.ReplaceAll(dir, "'", "''")+"').GetAccessControl().AreAccessRulesProtected")
	cmd.Env = withoutEnv(os.Environ(), "PSModulePath")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("read protection of %s: %v: %s", dir, err, out)
	}
	return strings.TrimSpace(string(out)) == "True"
}

func withoutEnv(env []string, name string) []string {
	out := env[:0:0]
	for _, kv := range env {
		if !strings.HasPrefix(strings.ToUpper(kv), strings.ToUpper(name)+"=") {
			out = append(out, kv)
		}
	}
	return out
}
