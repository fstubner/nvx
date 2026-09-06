//go:build darwin

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The Seatbelt profile is written somewhere contained code cannot write.
//
// Both macOS launch paths wrote it with os.CreateTemp("", ...), which on macOS
// lands under $TMPDIR, and $TMPDIR is under /private/var/folders -- one of the
// four roots the profile itself grants file-write* on, so that contained code
// has a temp directory. The file is 0600, but a concurrent contained process
// runs as the same user. Between nvx writing the profile and sandbox-exec
// reading it, that process can replace the contents with `(allow default)`, and
// the launch it was racing then runs with no containment at all. A process
// that can watch a directory and rewrite a file is not an exotic attacker; it
// is any package's postinstall script.
//
// So the profile goes under ~/.nvx, which the profile deliberately does not
// grant writes to. This test drives both launch paths through the
// seatbeltExecPath seam with a stand-in sandbox-exec that records its argv, and
// checks where the -f path actually is. The recorded path is resolved through
// symlinks before checking, because $TMPDIR spells /private/var/folders as
// /var/folders.
func TestSeatbeltProfileIsWrittenWhereContainedCodeCannotWrite(t *testing.T) {
	for _, tc := range []struct {
		name   string
		launch func(nvxHome, guestHome, workDir string) (code int, err error)
	}{
		{"native (the default macOS path)", func(nvxHome, guestHome, workDir string) (int, error) {
			config := SandboxConfig{NvxHome: nvxHome, Command: "/bin/sh", Args: []string{"-c", "true"}}
			return platformLaunchNative(config, guestHome, workDir, "/bin/sh", nil, NetworkLaunchContext{Mode: "proxy"})
		}},
		{"sandbox-exec provider (the legacy path)", func(nvxHome, guestHome, workDir string) (int, error) {
			config := SandboxConfig{NvxHome: nvxHome, Command: "/bin/sh", Args: []string{"-c", "true"}, WorkDir: workDir}
			return runSeatbeltSandbox(config, NetworkLaunchContext{Mode: "proxy"}), nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			argvFile := filepath.Join(tempDir(t), "argv")
			fake := filepath.Join(tempDir(t), "sandbox-exec")
			// The path is baked into the script: the launcher hands the child a
			// scrubbed environment, so nothing could be passed through it.
			script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argvFile + "\nexit 0\n"
			if err := os.WriteFile(fake, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			orig := seatbeltExecPath
			defer func() { seatbeltExecPath = orig }()
			seatbeltExecPath = fake

			nvxHome := filepath.Join(tempDir(t), ".nvx")
			guestHome := filepath.Join(nvxHome, "sandbox_home", "thissession")
			workDir := tempDir(t)
			if err := os.MkdirAll(guestHome, 0o700); err != nil {
				t.Fatal(err)
			}

			if code, err := tc.launch(nvxHome, guestHome, workDir); code != 0 || err != nil {
				t.Fatalf("launch through the stand-in sandbox-exec failed: code=%d err=%v", code, err)
			}

			argv, err := os.ReadFile(argvFile)
			if err != nil {
				t.Fatalf("the stand-in sandbox-exec was not run: %v", err)
			}
			lines := strings.Split(strings.TrimSpace(string(argv)), "\n")
			profilePath := ""
			for i, l := range lines {
				if l == "-f" && i+1 < len(lines) {
					profilePath = lines[i+1]
				}
			}
			if profilePath == "" {
				t.Fatalf("no -f <profile> in sandbox-exec's argv:\n%s", argv)
			}

			// The file is removed when the launch returns; its directory is not.
			dir, err := filepath.EvalSymlinks(filepath.Dir(profilePath))
			if err != nil {
				t.Fatalf("resolve the profile's directory: %v", err)
			}
			resolve := func(p string) string {
				if r, err := filepath.EvalSymlinks(p); err == nil {
					return r
				}
				return p
			}
			for _, writable := range []string{"/dev", "/private/tmp", "/private/var/tmp", "/private/var/folders", resolve(guestHome), resolve(workDir)} {
				if dir == writable || strings.HasPrefix(dir, writable+"/") {
					t.Fatalf("the Seatbelt profile was written to %s, under %s, which the profile grants file-write* on: "+
						"a concurrent contained process could rewrite it before sandbox-exec reads it", profilePath, writable)
				}
			}
			home := resolve(nvxHome)
			if !(dir == home || strings.HasPrefix(dir, home+"/")) {
				t.Errorf("the Seatbelt profile was written to %s; expected it under the nvx home %s, which contained code cannot write", profilePath, nvxHome)
			}
		})
	}
}
