//go:build windows

package nvx

// Probe (NVX_PROBE=1): a contained process may read the repository's .git and
// may not write it, while the rest of the project stays writable.
//
// The project's capability holds modify on the working directory, inherited by
// everything beneath, .git included. git never runs contained, so a hook or a
// config entry written there runs as the user on the next commit.
// restrictGitMetadataToReadOnly leaves the capability read and execute on .git
// and nothing more. A deny entry for the capability was tried first and did
// not hold; see that function for the measurement.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

const gitDenyFixtureConfig = "[core]\n\trepositoryformatversion = 0\n"

func TestSandboxCannotWriteGitMetadata(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run (creates a throwaway AppContainer profile)")
	}
	if os.Getenv("NVX_GITDENY_CHILD") == "1" {
		runGitDenyProbeChild(os.Getenv("NVX_PROBE_WORK"), os.Getenv("NVX_PROBE_GITROOT"))
		os.Exit(0)
	}

	project := fixtureProjectDir(t)
	writeGitDenyFixture(t, project)

	got := launchGitDenyProbe(t, "TestSandboxCannotWriteGitMetadata", "nvx.sandbox.gitdeny", project, project, project)
	requireGitDenyProbe(t, got, project)
	requireUncontainedGitWrite(t, project)
}

// From a subdirectory, the project root's .git is still denied. A run from the
// root leaves the capability holding modify there, so a later run from a
// subdirectory can reach the root's .git through it.
func TestSandboxCannotWriteGitMetadataFromSubdirectory(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run (creates a throwaway AppContainer profile)")
	}
	if os.Getenv("NVX_GITDENY_CHILD") == "1" {
		runGitDenyProbeChild(os.Getenv("NVX_PROBE_WORK"), os.Getenv("NVX_PROBE_GITROOT"))
		os.Exit(0)
	}

	project := fixtureProjectDir(t)
	writeGitDenyFixture(t, project)
	sub := filepath.Join(project, "packages", "app")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	// The root grant an earlier nvx left, without any deny on .git.
	capSID, err := scopeCapabilitySID(project)
	if err != nil {
		t.Fatal(err)
	}
	if err := grantSandboxModify(capSID, project); err != nil {
		t.Fatalf("root grant: %v", err)
	}

	got := launchGitDenyProbe(t, "TestSandboxCannotWriteGitMetadataFromSubdirectory", "nvx.sandbox.gitdenysub", project, sub, project)
	requireGitDenyProbe(t, got, project)
}

func writeGitDenyFixture(t *testing.T, project string) {
	t.Helper()
	gitDir := filepath.Join(project, ".git")
	if err := os.MkdirAll(filepath.Join(gitDir, "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"config":                  gitDenyFixtureConfig,
		"HEAD":                    "ref: refs/heads/main\n",
		"hooks/pre-commit.sample": "#!/bin/sh\n",
	} {
		if err := os.WriteFile(filepath.Join(gitDir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// launchGitDenyProbe prepares the sandbox filesystem for workDir, the way a
// launch does, and runs the probe child in it with workDir as its directory.
func launchGitDenyProbe(t *testing.T, testName, profile, project, workDir, gitRoot string) string {
	t.Helper()
	sid, err := ensureAppContainerSID(profile)
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	defer syscall.LocalFree(syscall.Handle(sid))
	defer deleteAppContainerProfile(profile)

	guestHome := tempDir(t)
	caps, _, err := prepareAppContainerFilesystem(sid, "", guestHome, workDir)
	if err != nil {
		t.Fatalf("filesystem prep: %v", err)
	}
	childExe := stageProbeChild(t, guestHome, "gitdeny.exe")

	read, write := makeTestPipe(t)
	defer syscall.CloseHandle(read)
	prevOut, _ := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	const stdOutputHandle = uintptr(0xFFFFFFF5)
	procSetStdHandleTest.Call(stdOutputHandle, uintptr(write))

	env := append(scrubEnvironment(guestHome),
		"NVX_PROBE=1", "NVX_GITDENY_CHILD=1",
		"NVX_PROBE_WORK="+workDir,
		"NVX_PROBE_GITROOT="+gitRoot,
	)
	_, launchErr := launchAppContainerProcess(childExe,
		[]string{"-test.run=^" + testName + "$"},
		env, workDir, sid, 0, caps)

	procSetStdHandleTest.Call(stdOutputHandle, uintptr(prevOut))
	syscall.CloseHandle(write)
	got := readProbeOutput(t, read)

	requireAppContainerLaunch(t, launchErr)
	t.Logf("child output:\n%s", got)
	return got
}

func runGitDenyProbeChild(work, gitRoot string) {
	report := func(key string, err error) {
		if err != nil {
			fmt.Printf("%s=DENIED\n", key)
			return
		}
		fmt.Printf("%s=OK\n", key)
	}
	write := func(p string) error { return os.WriteFile(p, []byte("rewritten\n"), 0o600) }
	appendTo := func(p string) error {
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_APPEND, 0)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = f.WriteString("[core]\n\thooksPath = elsewhere\n")
		return err
	}
	gitDir := filepath.Join(gitRoot, ".git")
	// Reads first: a rename that got through would make them fail for the
	// wrong reason.
	_, err := os.ReadFile(filepath.Join(gitDir, "config"))
	report("CONFIG_READ", err)
	_, err = os.ReadDir(filepath.Join(gitDir, "hooks"))
	report("HOOKS_LIST", err)
	report("HOOK_CREATE", write(filepath.Join(gitDir, "hooks", "pre-commit")))
	report("HOOK_MODIFY", appendTo(filepath.Join(gitDir, "hooks", "pre-commit.sample")))
	report("CONFIG_WRITE", appendTo(filepath.Join(gitDir, "config")))
	report("CONFIG_DELETE", os.Remove(filepath.Join(gitDir, "config")))
	report("GITDIR_RENAME", os.Rename(gitDir, filepath.Join(gitRoot, ".git-moved")))
	report("PKG_WRITE", write(filepath.Join(work, "package.json")))
	nm := filepath.Join(work, "node_modules", "dep")
	err = os.MkdirAll(nm, 0o755)
	if err == nil {
		err = write(filepath.Join(nm, "index.js"))
	}
	report("NM_WRITE", err)
}

func requireGitDenyProbe(t *testing.T, got, gitRoot string) {
	t.Helper()
	for _, want := range []string{
		"HOOK_CREATE=DENIED", "HOOK_MODIFY=DENIED", "CONFIG_WRITE=DENIED",
		"CONFIG_DELETE=DENIED", "GITDIR_RENAME=DENIED",
		"CONFIG_READ=OK", "HOOKS_LIST=OK", "PKG_WRITE=OK", "NM_WRITE=OK",
	} {
		if !strings.Contains(got, want+"\n") && !strings.HasSuffix(strings.TrimSpace(got), want) {
			t.Errorf("want %s\n%s", want, got)
		}
	}
	gitDir := filepath.Join(gitRoot, ".git")
	if b, err := os.ReadFile(filepath.Join(gitDir, "config")); err != nil || string(b) != gitDenyFixtureConfig {
		t.Errorf(".git/config changed or vanished on disk (err %v): %q", err, b)
	}
	if _, err := os.Stat(filepath.Join(gitDir, "hooks", "pre-commit")); err == nil {
		t.Error(".git/hooks/pre-commit exists on disk: a contained process created a hook")
	}
}

// requireUncontainedGitWrite checks the deny entry does nothing to the user:
// this test process is not contained and must still write .git.
func requireUncontainedGitWrite(t *testing.T, gitRoot string) {
	t.Helper()
	cfg := filepath.Join(gitRoot, ".git", "config")
	if err := os.WriteFile(cfg, []byte(gitDenyFixtureConfig+"# user\n"), 0o600); err != nil {
		t.Errorf("the user can no longer write .git/config after the sandbox's deny entry: %v", err)
	}
	hook := filepath.Join(gitRoot, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\n"), 0o600); err != nil {
		t.Errorf("the user can no longer create a hook after the sandbox's deny entry: %v", err)
	}
}
