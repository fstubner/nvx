package nvx

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The Seatbelt profile allows every read so the dynamic linker can find its
// libraries. The user's credential stores are denied after that allow, so the
// deny wins, and each is named under the real home and under the home with
// links resolved, because Seatbelt matches the resolved path.
func TestSeatbeltProfileDeniesReadingCredentialStores(t *testing.T) {
	home := tempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir reads this on Windows
	if err := os.WriteFile(filepath.Join(home, ".npmrc"), []byte("//registry.npmjs.org/:_authToken=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	guestHome := filepath.Join(home, ".nvx", "sandbox_home", "s1")
	workDir := filepath.Join(home, "projects", "app")

	profile := buildSeatbeltProfile(NetworkLaunchContext{Mode: "proxy"}, guestHome, workDir)

	homes := []string{home}
	if resolved, err := filepath.EvalSymlinks(home); err == nil && resolved != home {
		homes = append(homes, resolved)
	}
	allowAt := strings.Index(profile, "(allow file-read*)\n")
	if allowAt < 0 {
		t.Fatalf("profile has no blanket read allow:\n%s", profile)
	}
	for _, h := range homes {
		for _, want := range []string{
			fmt.Sprintf("(deny file-read* (literal %q))", filepath.Join(h, ".npmrc")),
			fmt.Sprintf("(deny file-read* (literal %q))", filepath.Join(h, ".yarnrc.yml")),
			fmt.Sprintf("(deny file-read* (literal %q))", filepath.Join(h, ".docker", "config.json")),
			fmt.Sprintf("(deny file-read* (literal %q))", filepath.Join(h, ".git-credentials")),
			fmt.Sprintf("(deny file-read* (subpath %q))", filepath.Join(h, ".ssh")),
			fmt.Sprintf("(deny file-read* (subpath %q))", filepath.Join(h, ".aws")),
			fmt.Sprintf("(deny file-read* (subpath %q))", filepath.Join(h, "Library", "Keychains")),
		} {
			at := strings.Index(profile, want)
			if at < 0 {
				t.Errorf("profile does not contain %s", want)
				continue
			}
			if at < allowAt {
				t.Errorf("%s comes before the read allow it has to override", want)
			}
		}
	}

	// What a contained run needs stays readable. No read deny covers the guest
	// home, the project, or the home directory itself.
	denyRe := regexp.MustCompile(`\(deny file-read\* \((?:literal|subpath) ("(?:[^"\\]|\\.)*")\)\)`)
	matches := denyRe.FindAllStringSubmatch(profile, -1)
	if len(matches) == 0 {
		t.Fatalf("profile has no read denies:\n%s", profile)
	}
	for _, m := range matches {
		p, err := strconv.Unquote(m[1])
		if err != nil {
			t.Fatalf("unquote %s: %v", m[1], err)
		}
		for _, needed := range []string{guestHome, workDir, home} {
			if dirWithin(needed, p) {
				t.Errorf("read deny on %s covers %s, which a contained run needs", p, needed)
			}
		}
	}
	if t.Failed() {
		t.Logf("profile:\n%s", profile)
	}
}
