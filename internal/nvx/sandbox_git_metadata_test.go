package nvx

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestGitMetadataPathsFindsTheGitDirectory(t *testing.T) {
	work := tempDir(t)
	if got := gitMetadataPaths(work); got != nil {
		t.Fatalf("no .git yet, got %v", got)
	}
	if err := os.Mkdir(filepath.Join(work, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, want := gitMetadataPaths(work), []string{filepath.Join(work, ".git")}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// A .git file names the real git directory. It is protected when it is inside
// the project, and the .git file is protected either way, because rewriting it
// to name another directory has the same effect as writing hooks.
func TestGitMetadataPathsFollowsAGitFile(t *testing.T) {
	work := tempDir(t)
	meta := filepath.Join(work, "repo-meta")
	if err := os.Mkdir(meta, 0o755); err != nil {
		t.Fatal(err)
	}
	dotGit := filepath.Join(work, ".git")
	if err := os.WriteFile(dotGit, []byte("gitdir: repo-meta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, want := gitMetadataPaths(work), []string{dotGit, meta}; !reflect.DeepEqual(got, want) {
		t.Fatalf("inside the project: got %v, want %v", got, want)
	}

	// A linked worktree's git directory is outside the project, which is not a
	// writable root, so only the .git file needs protecting.
	outside := tempDir(t)
	if err := os.WriteFile(dotGit, []byte("gitdir: "+outside+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, want := gitMetadataPaths(work), []string{dotGit}; !reflect.DeepEqual(got, want) {
		t.Fatalf("outside the project: got %v, want %v", got, want)
	}
}

// The Seatbelt profile denies writes to .git after allowing the project, so
// the deny is the rule that wins, and the project itself stays writable.
func TestGitMetadataSeatbeltProfileDeniesWrites(t *testing.T) {
	work := tempDir(t)
	if err := os.Mkdir(filepath.Join(work, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	profile := buildSeatbeltProfile(NetworkLaunchContext{Mode: "proxy"}, tempDir(t), work)

	deny := fmt.Sprintf("(deny file-write* (subpath %q))", filepath.Join(work, ".git"))
	allow := fmt.Sprintf("(subpath %q)", work)
	denyAt := strings.Index(profile, deny)
	allowAt := strings.Index(profile, "(allow file-write*")
	if denyAt < 0 {
		t.Fatalf("profile does not deny writes to .git:\n%s", profile)
	}
	if allowAt < 0 || denyAt < allowAt {
		t.Fatalf("the .git deny must come after the file-write* allow it overrides:\n%s", profile)
	}
	if !strings.Contains(seatbeltWriteSection(t, profile), allow) {
		t.Fatalf("the project is no longer a writable root:\n%s", profile)
	}
}
