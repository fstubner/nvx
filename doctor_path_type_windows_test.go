//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `nvx doctor --fix` writes the User PATH back as the type it found.
//
// Windows ships the User PATH as REG_EXPAND_SZ, which is what makes an entry
// spelled %USERPROFILE%\bin or %JAVA_HOME%\bin resolve: the value is expanded
// when it is read. The repair went through
// [Environment]::SetEnvironmentVariable, which always writes REG_SZ, so every
// machine it ran on had the type converted and every such entry quietly stopped
// resolving -- a repair that broke PATH entries while reporting that it had
// repaired PATH.
//
// Both directions are pinned. A machine whose PATH is genuinely REG_SZ, with a
// literal percent sign it wants left alone, must not be converted the other way
// either.
func TestThePathRepairPreservesTheRegistryValueType(t *testing.T) {
	for _, tc := range []struct {
		name   string
		expand bool
	}{
		{"REG_EXPAND_SZ, as Windows ships it", true},
		{"REG_SZ, if that is what the machine has", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			userHome, err := os.UserHomeDir()
			if err != nil {
				t.Fatal(err)
			}
			nvxHome, err := os.MkdirTemp(userHome, ".nvx-path-type-test-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(nvxHome) })
			runtimeBin := filepath.Join(nvxHome, "versions", "node", "v20.0.0")
			if err := os.MkdirAll(runtimeBin, 0o700); err != nil {
				t.Fatal(err)
			}

			origRead, origSet := readUserPath, setUserPath
			readUserPath = func() (string, bool, error) {
				return strings.Join([]string{runtimeBin, `C:\Windows`}, ";"), tc.expand, nil
			}
			var wroteExpand bool
			var wroteValue string
			var wrote bool
			setUserPath = func(value string, expand bool) error {
				wrote, wroteValue, wroteExpand = true, value, expand
				return nil
			}
			t.Cleanup(func() { readUserPath, setUserPath = origRead, origSet })

			if _, err := repairPersistentPathImpl(nvxHome, true); err != nil {
				t.Fatalf("repair failed: %v", err)
			}
			if !wrote {
				t.Fatal("the repair wrote nothing")
			}
			if wroteExpand != tc.expand {
				t.Fatalf("the repair stored the PATH as expandable=%v, want %v -- it changed the value's type", wroteExpand, tc.expand)
			}
			if !strings.Contains(wroteValue, `C:\Windows`) {
				t.Fatalf("the repair dropped an unrelated entry: %q", wroteValue)
			}
		})
	}
}

// And the writer really writes that type, which is the half a stubbed setter
// cannot show. Against a scratch key of nvx's own, never the real Environment.
func TestTheRegistryWriterStoresTheRequestedType(t *testing.T) {
	subkey := `Software\nvx-test-path-type`
	t.Cleanup(func() {
		_, _ = runWinCmd(15e9, "reg", "delete", `HKCU\`+subkey, "/f")
	})

	for _, tc := range []struct {
		expand bool
		want   string
	}{
		{true, "REG_EXPAND_SZ"},
		{false, "REG_SZ"},
	} {
		if err := setRegistryStringValue(subkey, "Probe", `%USERPROFILE%\bin;C:\Windows`, tc.expand); err != nil {
			t.Fatalf("write (expand=%v) failed: %v", tc.expand, err)
		}
		out, err := runWinCmd(15e9, "reg", "query", `HKCU\`+subkey, "/v", "Probe")
		if err != nil {
			t.Fatalf("read back failed: %v", err)
		}
		if !strings.Contains(string(out), tc.want) {
			t.Fatalf("stored type is not %s:\n%s", tc.want, out)
		}
		if got := parseRegPath(string(out)); got != `%USERPROFILE%\bin;C:\Windows` {
			t.Fatalf("stored value came back as %q", got)
		}
		if parseRegExpandable(string(out)) != tc.expand {
			t.Fatalf("parseRegExpandable disagrees with the stored type:\n%s", out)
		}
	}
}
