package nvx

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The Seatbelt profile denies reading the real home directory and nvxHome after
// its blanket read allow, then reopens the project, the guest home, nvx's
// runtimes and allow_read_exec roots. Until 2026-10-06 it denied only the
// credential stores, and every other project in the home was readable.
func TestSeatbeltProfileDeniesReadsUnderTheHomeOutsideWhatARunNeeds(t *testing.T) {
	home := tempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir reads this on Windows

	for _, tc := range []struct {
		name    string
		nvxHome string
	}{
		{"nvx home under the home", filepath.Join(home, ".nvx")},
		{"nvx home outside the home", tempDir(t)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			guestHome := filepath.Join(tc.nvxHome, "sandbox_home", "s1")
			workDir := filepath.Join(home, "projects", "app")
			readExec := filepath.Join(home, "tools", "browsers")
			profile := buildSeatbeltProfile(NetworkLaunchContext{Mode: "proxy"}, guestHome, workDir, tc.nvxHome, []string{readExec})

			at := func(rule string) int {
				t.Helper()
				i := strings.Index(profile, rule)
				if i < 0 {
					t.Errorf("profile does not contain %s", rule)
				}
				return i
			}
			blanket := at("(allow file-read*)\n")
			for _, denied := range []string{home, tc.nvxHome} {
				deny := at(fmt.Sprintf("(deny file-read* (subpath %q))", denied))
				if deny < blanket {
					t.Errorf("the read deny on %s comes before the blanket allow it has to override", denied)
				}
				if meta := at(fmt.Sprintf("(allow file-read-metadata (subpath %q))", denied)); meta < deny {
					t.Errorf("the metadata allow on %s comes before the deny it reopens", denied)
				}
				for _, needed := range []string{workDir, guestHome, readExec,
					filepath.Join(tc.nvxHome, "versions"), filepath.Join(tc.nvxHome, "bin")} {
					if reopen := at(fmt.Sprintf("(allow file-read* (subpath %q))", needed)); reopen < deny {
						t.Errorf("the read allow on %s comes before the deny on %s it has to override", needed, denied)
					}
				}
			}

			// Nothing reopened reaches the home itself, nvxHome itself, another
			// project, or the parts of nvxHome that hold grants and credentials.
			reopenRe := regexp.MustCompile(`\(allow file-read\* \(subpath ("(?:[^"\\]|\\.)*")\)\)`)
			for _, m := range reopenRe.FindAllStringSubmatch(profile, -1) {
				p, err := strconv.Unquote(m[1])
				if err != nil {
					t.Fatalf("unquote %s: %v", m[1], err)
				}
				for _, private := range []string{
					home, tc.nvxHome,
					filepath.Join(home, "projects", "other"),
					filepath.Join(tc.nvxHome, "grants"),
					filepath.Join(tc.nvxHome, "tool_home"),
					filepath.Join(tc.nvxHome, "policy.json"),
					filepath.Join(tc.nvxHome, "sandbox_home", "s2"),
				} {
					if dirWithin(private, p) {
						t.Errorf("the read allow on %s reopens %s", p, private)
					}
				}
			}
			if t.Failed() {
				t.Logf("profile:\n%s", profile)
			}
		})
	}
}
