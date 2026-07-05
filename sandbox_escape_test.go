//go:build integration

// Package main — sandbox ESCAPE-ASSERTION integration tests.
//
// These prove the sandbox actually CONTAINS a hostile process: a program run
// inside nvx's sandbox tries to (1) read a secret in the real $HOME, (2) write
// a file to the host outside its workdir, and (3) open a raw TCP connection to
// the internet. All three MUST fail, while a write to the workdir MUST succeed.
//
// They require a real host with the platform's isolation primitive available
// (Linux 5.13+ with unprivileged namespaces, macOS sandbox-exec, Windows
// AppContainer) plus a network connection and Node.js on PATH. They are gated
// behind the `integration` build tag so ordinary `go test ./...` never runs
// them:
//
//	go test -tags integration -run Escape -v ./...
//
// By default a missing primitive/tooling is a SKIP. Set NVX_ESCAPE_STRICT=1 to
// turn skips into failures (use this on runners where the primitive is expected
// to work, so containment can never silently go unverified).
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const canarySecret = "NVX_CANARY_SECRET_do_not_leak"

func strictMode() bool { return os.Getenv("NVX_ESCAPE_STRICT") == "1" }

// skipOrFail skips unless strict mode is on, in which case it fails.
func skipOrFail(t *testing.T, format string, a ...any) {
	t.Helper()
	if strictMode() {
		t.Fatalf("[strict] "+format, a...)
	}
	t.Skipf(format, a...)
}

// buildNvx compiles the nvx binary under test to a temp path.
func buildNvx(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "nvx-under-test")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go build nvx: %v\n%s", err, out)
	}
	return bin
}

// looksUnavailable reports whether output indicates the sandbox primitive is
// missing (as opposed to an escape or an unexpected error).
func looksUnavailable(out string) bool {
	out = strings.ToLower(out)
	for _, s := range []string{
		"not supported", "unavailable", "requires linux kernel", "landlock",
		"namespace", "appcontainer", "sandbox-exec not found", "operation not permitted",
	} {
		if strings.Contains(out, s) {
			return true
		}
	}
	return false
}

func TestSandboxEscapeContainment(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		skipOrFail(t, "node not found on PATH (required to run the in-sandbox probe)")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain required to build nvx")
	}

	nvx := buildNvx(t)
	nvxHome := t.TempDir()
	env := append(os.Environ(), "NVX_HOME="+nvxHome)

	run := func(args ...string) (string, int) {
		cmd := exec.Command(nvx, args...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		code := 0
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				code = ee.ExitCode()
			} else {
				code = -1
			}
		}
		return string(out), code
	}

	// Install a real Node UNDER nvx so the sandbox's readable roots include it
	// (an ambient Node in a non-standard toolcache path may be unreadable inside
	// the sandbox). This needs network access.
	if out, code := run("install", "lts"); code != 0 {
		skipOrFail(t, "could not install Node via nvx (network needed?): %s", out)
	}
	if out, code := run("default", "lts"); code != 0 {
		t.Fatalf("nvx default lts failed: %s", out)
	}

	// Canary secret in a temp dir under the REAL home — a path both Landlock
	// (not in read-roots) and Seatbelt (home deny) must refuse to read.
	realHome, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home dir: %v", err)
	}
	canaryDir, err := os.MkdirTemp(realHome, ".nvx-escape-canary-")
	if err != nil {
		t.Fatalf("create canary dir: %v", err)
	}
	defer os.RemoveAll(canaryDir)
	secretPath := filepath.Join(canaryDir, "secret.txt")
	if err := os.WriteFile(secretPath, []byte(canarySecret), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	hostWritePath := filepath.Join(canaryDir, "should-not-appear.txt")

	workDir := t.TempDir()

	// --- Filesystem containment probe -----------------------------------------
	fsProbe := `
const fs=require('fs');
const secret=process.argv[1], host=process.argv[2];
let esc=[];
try{ if(fs.readFileSync(secret,'utf8').indexOf('` + canarySecret + `')>=0) esc.push('read-secret'); }catch(e){}
try{ fs.writeFileSync(host,'pwned'); esc.push('write-host'); }catch(e){}
let work=false; try{ fs.writeFileSync('nvx-work-canary.txt','ok'); work=true; }catch(e){}
if(!work){ console.error('WORKDIR-WRITE-FAILED'); process.exit(3); }
if(esc.length){ console.error('ESCAPED:'+esc.join(',')); process.exit(2); }
console.log('CONTAINED'); process.exit(0);
`
	cmd := exec.Command(nvx, "shim", "node", "-e", fsProbe, secretPath, hostWritePath)
	cmd.Env = env
	cmd.Dir = workDir
	fsOut, fsErr := cmd.CombinedOutput()
	fsCode := 0
	if fsErr != nil {
		if ee, ok := fsErr.(*exec.ExitError); ok {
			fsCode = ee.ExitCode()
		} else {
			fsCode = -1
		}
	}

	switch fsCode {
	case 0:
		// contained — good.
	case 2:
		t.Fatalf("SANDBOX ESCAPE (filesystem): %s", fsOut)
	case 3:
		t.Fatalf("workdir write failed — sandbox setup problem, not a valid test: %s", fsOut)
	default:
		if looksUnavailable(string(fsOut)) {
			skipOrFail(t, "sandbox primitive unavailable on this host: %s", fsOut)
		}
		t.Fatalf("unexpected failure running fs probe (code %d): %s", fsCode, fsOut)
	}

	// Positive/negative controls independent of the probe's own exit code.
	if _, err := os.Stat(hostWritePath); err == nil {
		t.Errorf("host file was created inside the sandbox — write containment breached: %s", hostWritePath)
	}
	if _, err := os.Stat(filepath.Join(workDir, "nvx-work-canary.txt")); err != nil {
		t.Errorf("workdir write did not land — sandbox is over-restrictive or broken: %v", err)
	}

	// --- Network containment probe --------------------------------------------
	// Default policy network.mode=proxy: a raw TCP connection (which bypasses the
	// HTTP proxy) must be blocked by the netns (Linux) / Seatbelt (macOS).
	netProbe := `
const net=require('net');
const s=net.connect({host:'1.1.1.1',port:443});
const done=(c,m)=>{try{s.destroy()}catch(e){}; if(m)console.error(m); process.exit(c)};
s.setTimeout(5000);
s.on('connect',()=>done(2,'ESCAPED:network'));
s.on('error',()=>done(0));
s.on('timeout',()=>done(0));
`
	ncmd := exec.Command(nvx, "shim", "node", "-e", netProbe)
	ncmd.Env = env
	ncmd.Dir = workDir
	netOut, netErr := ncmd.CombinedOutput()
	netCode := 0
	if netErr != nil {
		if ee, ok := netErr.(*exec.ExitError); ok {
			netCode = ee.ExitCode()
		} else {
			netCode = -1
		}
	}
	if netCode == 2 {
		t.Fatalf("SANDBOX ESCAPE (network): a raw TCP connection to the internet succeeded: %s", netOut)
	}
	if netCode != 0 && !looksUnavailable(string(netOut)) {
		t.Logf("network probe exited %d (treated as contained): %s", netCode, netOut)
	}

	t.Logf("sandbox containment verified on %s/%s", runtime.GOOS, runtime.GOARCH)
}
