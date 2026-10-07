package nvx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// devEnginesProject is a directory whose package.json carries devEngines, with a
// home whose default Node.js is v22.1.0.
func devEnginesProject(t *testing.T, devEngines string) (nvxHome string) {
	t.Helper()
	notQuiet(t)
	nvxHome = tempDir(t)
	installStubNodes(t, nvxHome, "v22.1.0", "v22.1.0")
	project := tempDir(t)
	if err := os.WriteFile(filepath.Join(project, "package.json"), []byte(`{"name":"p","devEngines":`+devEngines+`}`), 0o600); err != nil {
		t.Fatal(err)
	}
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
	unsetEnv(t, nvxActiveEnvVar)
	return nvxHome
}

// npm refuses with EBADDEVENGINES while the shim said nothing. Measured
// 2026-10-07, devEngines.runtime asked for 24.21.0, the default was 22, and `node
// -v` and `nvx auto` printed nothing.
func TestTheShimSaysWhenDevEnginesAsksForAnotherNode(t *testing.T) {
	nvxHome := devEnginesProject(t, `{"runtime":{"name":"node","version":"24.21.0","onFail":"error"}}`)

	out := captureStderrHere(t, func() { warnIfDevEnginesDisagree(nvxHome, "node") })
	for _, want := range []string{"devEngines.runtime", "24.21.0", "v22.1.0", ".nvmrc"} {
		if !strings.Contains(out, want) {
			t.Errorf("the message does not contain %q:\n%s", want, out)
		}
	}
	if out := captureStderrHere(t, func() { noteDevEnginesInAuto(nvxHome) }); !strings.Contains(out, "24.21.0") || !strings.Contains(out, "devEngines.runtime") {
		t.Errorf("`nvx auto` stayed silent about devEngines:\n%s", out)
	}
	for _, cmd := range []string{"npm", "npx", "yarn"} {
		if out := captureStderrHere(t, func() { warnIfDevEnginesDisagree(nvxHome, cmd) }); !strings.Contains(out, "24.21.0") {
			t.Errorf("%s did not say it: %q", cmd, out)
		}
	}
	// Bun is not Node.js.
	if out := captureStderrHere(t, func() { warnIfDevEnginesDisagree(nvxHome, "bun") }); out != "" {
		t.Errorf("a bun command was told about the Node.js request: %q", out)
	}
}

// No message where there is nothing to say.
func TestTheDevEnginesMessageIsSilentWhenItWouldBeWrongOrNoise(t *testing.T) {
	for name, tc := range map[string]struct{ devEngines string }{
		"the running version satisfies it": {`{"runtime":{"name":"node","version":">=20","onFail":"error"}}`},
		"onFail is warn":                   {`{"runtime":{"name":"node","version":"24.21.0","onFail":"warn"}}`},
		"onFail is ignore":                 {`{"runtime":{"name":"node","version":"24.21.0","onFail":"ignore"}}`},
		"onFail is download":               {`{"runtime":{"name":"node","version":"24.21.0","onFail":"download"}}`},
		"it names another runtime":         {`{"runtime":{"name":"bun","version":"1.2.0"}}`},
		"a range nvx cannot read":          {`{"runtime":{"name":"node","version":"18 - 24"}}`},
		"it is not an object":              {`{"runtime":"node"}`},
	} {
		t.Run(name, func(t *testing.T) {
			nvxHome := devEnginesProject(t, tc.devEngines)
			if out := captureStderrHere(t, func() { warnIfDevEnginesDisagree(nvxHome, "node") }); out != "" {
				t.Errorf("printed %q", out)
			}
		})
	}

	t.Run("a file nvx reads declares the version", func(t *testing.T) {
		nvxHome := devEnginesProject(t, `{"runtime":{"name":"node","version":"24.21.0"}}`)
		if err := os.WriteFile(".nvmrc", []byte("22\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if out := captureStderrHere(t, func() { warnIfDevEnginesDisagree(nvxHome, "node") }); out != "" {
			t.Errorf("printed %q, though .nvmrc decides and has its own warning", out)
		}
	})
	t.Run("the array form finds the Node.js entry", func(t *testing.T) {
		nvxHome := devEnginesProject(t, `{"runtime":[{"name":"bun","version":"1.2.0"},{"name":"node","version":"24.21.0"}]}`)
		if out := captureStderrHere(t, func() { warnIfDevEnginesDisagree(nvxHome, "node") }); !strings.Contains(out, "24.21.0") {
			t.Errorf("the Node.js entry of an array was not read: %q", out)
		}
	})
}

// A failed resolution exits with npm's code, not with nvx's refusal code.
//
// With devEngines asking for a Node.js that is not running, npm printed
// EBADDEVENGINES from its resolver, nvx could not check the install and asked
// whether to go on, nobody answered, and the run exited 77. 77 means nvx refused
// for what it found, and a pipeline reading it sent the person to the wrong
// place.
func TestAFailedResolutionExitsWithNpmsCode(t *testing.T) {
	w := newVerifyWorld(t, `{"typosquatting":{"enabled":false}}`)
	w.write("package.json", `{"name":"app"}`)
	w.addTsx(fakeRegistryPkg{})
	w.npmExit = 3

	code, reason, _ := verifyBeforeRun(verifyRequest{pmCmd: "npm", pmArgs: []string{"install", "tsx"}, nvxHome: w.home, contain: true,
		launch: SandboxConfig{NvxHome: w.home}})
	if code != 3 || reason != resolutionFailedReason {
		t.Fatalf("verifyBeforeRun = %d, %q; want npm's 3 and the resolution reason", code, reason)
	}
	if got := verifyExitCode(code, reason); got != 3 {
		t.Errorf("the run exits %d, want npm's own 3", got)
	}

	// Every other refusal stays 77, and so does a resolver that exits with it.
	if got := verifyExitCode(1, blockedReason); got != exitRefused {
		t.Errorf("a blocked package exits %d, want %d", got, exitRefused)
	}
	if got := verifyExitCode(0, resolutionFailedReason); got != exitRefused {
		t.Errorf("a zero code exits %d, want %d", got, exitRefused)
	}
	w.npmExit = exitRefused
	code, reason, _ = verifyBeforeRun(verifyRequest{pmCmd: "npm", pmArgs: []string{"install", "tsx"}, nvxHome: w.home, contain: true,
		launch: SandboxConfig{NvxHome: w.home}})
	if verifyExitCode(code, reason) != exitRefused {
		t.Errorf("a resolver that was itself refused no longer exits %d", exitRefused)
	}
}

// The wiring, end to end, for the shim.
func TestTheNodeShimStartsWithTheDevEnginesMessage(t *testing.T) {
	nvxHome := devEnginesProject(t, `{"runtime":{"name":"node","version":"24.21.0","onFail":"error"}}`)
	// The stand-in is this test binary, which prints the path it was started from.
	fakeNodeInstall(t, nvxHome, "v22.1.0")
	t.Setenv("NVX_TEST_FAKE_RUNTIME", "1")
	t.Setenv("PATH", tempDir(t))

	var code int
	stderr := captureStderrHere(t, func() {
		_ = captureStdout(t, func() { code = runShim("node", []string{"-v"}, nvxHome) })
	})
	if code != 0 {
		t.Fatalf("the stand-in node exited %d:\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "devEngines.runtime") || !strings.Contains(stderr, "24.21.0") {
		t.Errorf("the shim did not mention devEngines:\n%s", stderr)
	}
}

// And for the cd hook.
func TestAutoStartsWithTheDevEnginesMessage(t *testing.T) {
	nvxHome := devEnginesProject(t, `{"runtime":{"name":"node","version":"24.21.0"}}`)
	stderr := captureStderrHere(t, func() {
		_ = captureStdout(t, func() { runAuto(nvxHome, "bash") })
	})
	if !strings.Contains(stderr, "devEngines.runtime") || !strings.Contains(stderr, "24.21.0") {
		t.Errorf("`nvx auto` did not mention devEngines:\n%s", stderr)
	}
}

// And for the exit code, through the run itself and not only the function that
// picks it.
func TestARunStoppedByAFailedResolutionExitsWithNpmsCode(t *testing.T) {
	w := newVerifyWorld(t, `{"typosquatting":{"enabled":false}}`)
	w.write("package.json", `{"name":"app"}`)
	w.addTsx(fakeRegistryPkg{})
	w.npmExit = 3
	unsetEnv(t, nvxActiveEnvVar)

	var code int
	stderr := captureStderrHere(t, func() { code = runShim("npm", []string{"install", "tsx"}, w.home) })
	if code != 3 {
		t.Errorf("npm's resolver exited 3 and the run exited %d (77 would say nvx refused)\n%s", code, stderr)
	}
}

// A failure that is nvx's own stays a refusal. Here npm exits 0 and writes a
// lockfile nvx cannot read, so nothing npm did stopped the install.
func TestAResolutionNvxCouldNotReadStillExitsAsARefusal(t *testing.T) {
	w := newVerifyWorld(t, `{"typosquatting":{"enabled":false}}`)
	w.write("package.json", `{"name":"app"}`)
	w.addTsx(fakeRegistryPkg{})
	w.lock = ""
	unsetEnv(t, nvxActiveEnvVar)

	var code int
	stderr := captureStderrHere(t, func() { code = runShim("npm", []string{"install", "tsx"}, w.home) })
	if code != exitRefused {
		t.Errorf("exit %d, want %d: the lockfile npm wrote was nvx's to read, not npm's failure\n%s", code, exitRefused, stderr)
	}
}
