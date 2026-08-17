package main

import (
	"strings"
	"testing"
)

// The macOS Critical (F22) was that the DEFAULT launch path passed nvxHome and the
// runtime binary's directory as writable Seatbelt roots, so a sandboxed process
// could rewrite policy.json, self-approve grants, poison npm_global, read and
// rewrite tool_home credentials, or trojan the node binary.
//
// It survived a July fix to the other caller because every existing test asserted
// only that the intended roots were PRESENT. Those assertions hold just as well
// when extra roots are present, so they passed against the vulnerable profile.
// These tests assert absence instead.

// seatbeltWriteSection returns just the file-write* rule text, so a path appearing
// in the (broad, intentional) read rules cannot mask a missing write assertion.
func seatbeltWriteSection(t *testing.T, profile string) string {
	t.Helper()
	idx := strings.Index(profile, "(allow file-write*")
	if idx < 0 {
		t.Fatalf("profile has no file-write* section:\n%s", profile)
	}
	rest := profile[idx:]
	// The section runs to the closing paren of the file-write* form, which is the
	// first line that is not a subpath/literal entry.
	if end := strings.Index(rest, "\n(allow "); end >= 0 {
		rest = rest[:end]
	}
	return rest
}

func TestSeatbeltProfileDoesNotGrantWriteToNvxHome(t *testing.T) {
	const nvxHome = "/Users/testuser/.nvx"
	profile := buildSeatbeltProfile(
		NetworkLaunchContext{Mode: "proxy"},
		"/Users/testuser/.nvx/sandbox_home/session1",
		"/Users/testuser/projects/app",
	)
	writes := seatbeltWriteSection(t, profile)

	// The guest home legitimately sits UNDER nvxHome, so a substring check for
	// nvxHome would match it. Assert on the exact subpath form instead.
	for _, forbidden := range []string{
		`(subpath "` + nvxHome + `")`,
		`(literal "` + nvxHome + `")`,
		`(subpath "` + nvxHome + `/")`,
	} {
		if strings.Contains(writes, forbidden) {
			t.Errorf("nvxHome is writable via %s -- a sandboxed process could rewrite policy.json, self-approve grants, or trojan the runtime.\nwrite section:\n%s", forbidden, writes)
		}
	}
}

func TestSeatbeltProfileDoesNotGrantWriteToRuntimeBinDir(t *testing.T) {
	profile := buildSeatbeltProfile(
		NetworkLaunchContext{Mode: "proxy"},
		"/Users/testuser/.nvx/sandbox_home/session1",
		"/Users/testuser/projects/app",
	)
	writes := seatbeltWriteSection(t, profile)

	// The directory holding the node binary must not be writable, or a sandboxed
	// process can replace the interpreter every later run executes.
	for _, forbidden := range []string{
		"/Users/testuser/.nvx/versions",
		"/usr/local/bin",
		"/opt/homebrew/bin",
	} {
		if strings.Contains(writes, forbidden) {
			t.Errorf("runtime binary directory %q is writable; the node/npm binaries could be trojaned.\nwrite section:\n%s", forbidden, writes)
		}
	}
}

// TestSeatbeltProfileWritableRootsAreExactlyExpected is the assertion that would
// have failed in July. It pins the whole set, so adding a writable root anywhere --
// by either caller, or inside the builder -- fails here rather than shipping.
func TestSeatbeltProfileWritableRootsAreExactlyExpected(t *testing.T) {
	guestHome := "/Users/testuser/.nvx/sandbox_home/session1"
	workDir := "/Users/testuser/projects/app"
	profile := buildSeatbeltProfile(NetworkLaunchContext{Mode: "proxy"}, guestHome, workDir)
	writes := seatbeltWriteSection(t, profile)

	want := []string{
		"/dev",
		"/private/tmp",
		"/private/var/tmp",
		"/private/var/folders",
		guestHome,
		workDir,
	}

	got := 0
	for _, line := range strings.Split(writes, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, `(subpath "`) || strings.HasPrefix(line, `(literal "`) {
			got++
			found := false
			for _, w := range want {
				if strings.Contains(line, `"`+w+`"`) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("unexpected writable root %s -- if this is deliberate, add it to want and say why in the commit", line)
			}
		}
	}
	if got != len(want) {
		t.Errorf("found %d writable roots, expected %d:\n%s", got, len(want), writes)
	}
}
