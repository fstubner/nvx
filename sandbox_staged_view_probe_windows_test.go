//go:build windows

package main

// Opt-in prototype (NVX_PROBE=1) for the staged-view idea.
//
// Deny ACEs failed to hide .env inside a granted directory, but the home-directory
// probe showed the complement works: what is never granted is unreachable. So the
// mechanism is not "grant the project and subtract secrets", it is "never grant the
// project; show a constructed view containing only what the install needs".
//
// This builds that view -- package.json and the lockfile, plus a junction pointing
// node_modules at the real project -- runs a contained process in it, and checks the
// two things that decide whether the approach is viable:
//
//   1. Security: .env and src/ in the real project are unreachable.
//   2. Function: writes into node_modules land in the REAL project through the
//      junction, so an install is not stranded in a throwaway directory.
//
// (2) is the part I expect to break, not (1).

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestStagedViewHidesProjectButWritesThrough(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run")
	}

	if os.Getenv("NVX_STAGED_CHILD") == "1" {
		// Try to reach the real project's secrets from inside the staged view.
		for _, spec := range strings.Split(os.Getenv("NVX_PROBE_TARGETS"), "|") {
			parts := strings.SplitN(spec, "=", 2)
			if len(parts) != 2 {
				continue
			}
			if _, err := os.ReadFile(parts[1]); err != nil {
				fmt.Printf("%s=DENIED\n", parts[0])
			} else {
				fmt.Printf("%s=READ\n", parts[0])
			}
		}
		// What the install legitimately needs: read the manifest, write a package.
		if _, err := os.ReadFile("package.json"); err != nil {
			fmt.Printf("MANIFEST=DENIED\n")
		} else {
			fmt.Printf("MANIFEST=READ\n")
		}
		if err := os.MkdirAll(filepath.Join("node_modules", "left-pad"), 0o700); err != nil {
			fmt.Printf("INSTALL=DENIED:%v\n", err)
		} else if err := os.WriteFile(filepath.Join("node_modules", "left-pad", "index.js"), []byte("module.exports=1"), 0o600); err != nil {
			fmt.Printf("INSTALL=DENIED:%v\n", err)
		} else {
			fmt.Printf("INSTALL=OK\n")
		}
		os.Exit(0)
	}

	const probeProfile = "nvx.sandbox.stagedprobe"
	sid, err := ensureAppContainerSID(probeProfile)
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	defer syscall.LocalFree(syscall.Handle(sid))
	defer deleteAppContainerProfile(probeProfile)

	guestHome := t.TempDir()

	// --- the real project, which the sandbox must NOT see ---
	project := t.TempDir()
	writeFile := func(p, s string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(filepath.Join(project, ".env"), "API_KEY=super-secret-value-12345")
	writeFile(filepath.Join(project, "src", "secret.js"), "// proprietary source")
	writeFile(filepath.Join(project, "package.json"), `{"name":"victim","version":"1.0.0"}`)
	writeFile(filepath.Join(project, "package-lock.json"), `{"lockfileVersion":3}`)
	realNodeModules := filepath.Join(project, "node_modules")
	if err := os.MkdirAll(realNodeModules, 0o700); err != nil {
		t.Fatal(err)
	}

	// --- the staged view: only what an install needs ---
	staged := filepath.Join(guestHome, "staged-project")
	if err := os.MkdirAll(staged, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"package.json", "package-lock.json"} {
		b, err := os.ReadFile(filepath.Join(project, name))
		if err != nil {
			t.Fatal(err)
		}
		writeFile(filepath.Join(staged, name), string(b))
	}
	// node_modules is a junction back to the real project, so the install writes
	// through instead of into a directory that gets thrown away.
	link := filepath.Join(staged, "node_modules")
	if out, err := runWinCmd(20*time.Second, "cmd", "/c", "mklink", "/J", link, realNodeModules); err != nil {
		t.Skipf("cannot create junction (needed for write-through): %v (%s)", err, strings.TrimSpace(string(out)))
	}

	// The grant covers the STAGED view and the real node_modules (the junction
	// target must be granted in its own right -- a junction is not a bypass).
	scopeCaps, err := prepareAppContainerFilesystem(sid, guestHome, staged)
	if err != nil {
		t.Fatalf("filesystem prep: %v", err)
	}
	if err := grantAppContainerPath(sid, realNodeModules); err != nil {
		t.Fatalf("grant node_modules: %v", err)
	}

	self, _ := os.Executable()
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	childExe := filepath.Join(guestHome, "stagedprobe.exe")
	if err := os.WriteFile(childExe, data, 0o700); err != nil {
		t.Fatal(err)
	}

	read, write := makeTestPipe(t)
	defer syscall.CloseHandle(read)
	prevOut, _ := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	const stdOutputHandle = uintptr(0xFFFFFFF5)
	procSetStdHandleTest.Call(stdOutputHandle, uintptr(write))

	targets := "PROJECTENV=" + filepath.Join(project, ".env") +
		"|PROJECTSRC=" + filepath.Join(project, "src", "secret.js")
	env := append(scrubEnvironment(guestHome),
		"NVX_PROBE=1", "NVX_STAGED_CHILD=1", "NVX_PROBE_TARGETS="+targets,
	)
	_, launchErr := launchAppContainerProcess(childExe,
		[]string{"-test.run=TestStagedViewHidesProjectButWritesThrough"},
		env, staged, sid, 0, scopeCaps)

	procSetStdHandleTest.Call(stdOutputHandle, uintptr(prevOut))
	syscall.CloseHandle(write)
	got := readWithTimeout(t, read)
	requireAppContainerLaunch(t, launchErr)
	t.Logf("child output: %q", got)

	// 1. Security: the real project's secrets and source must be out of reach.
	for _, key := range []string{"PROJECTENV", "PROJECTSRC"} {
		if contains(got, key+"=READ") {
			t.Errorf("%s was READABLE from the staged view; the view is not actually isolating the project", key)
		} else if !contains(got, key+"=DENIED") {
			t.Errorf("inconclusive result for %s in %q", key, got)
		}
	}

	// 2. Function: the manifest is visible and the install writes through.
	if !contains(got, "MANIFEST=READ") {
		t.Error("package.json is not readable in the staged view; an install could not run")
	}
	if !contains(got, "INSTALL=OK") {
		t.Errorf("the contained process could not write into node_modules: %q", got)
	}
	if _, err := os.Stat(filepath.Join(realNodeModules, "left-pad", "index.js")); err != nil {
		t.Errorf("the install did not land in the REAL project's node_modules: %v", err)
	} else {
		t.Log("write-through works: the package landed in the real project via the junction")
	}
}
