//go:build linux

package nvx

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// What a contained process can reach outside the sandbox by signals and abstract
// sockets, and what npm needs to keep working inside it. These run the chain of
// sandbox_terminal_linux_test.go: a stand-in for nvx, the real supervisor, and a
// stand-in target or a real npm.

// abstractSocketEnv names the abstract socket the "abstract" target dials.
const abstractSocketEnv = "NVX_TEST_ABSTRACT_SOCKET"

// A process group is not inside the PID namespace, and the target shares nvx's
// group, so a contained kill(0, SIGKILL) used to reach nvx and every process
// beside it. Measured 2026-10-07 on Linux 6.18, an npm preinstall did that and
// killed nvx, the shell that started it and a process the shell had started.
//
// The sentinel stands for that shell's other process. It shares nvx's group and
// is outside the sandbox. The target's kill must end the target and nothing else.
func TestContainedProcessCannotSignalOutsideItsSandbox(t *testing.T) {
	for _, branch := range terminalBranches {
		t.Run(branch.name, func(t *testing.T) {
			sentinel := exec.Command("sleep", "600")
			sentinel.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			if err := sentinel.Start(); err != nil {
				t.Fatalf("start the sentinel: %v", err)
			}
			group := sentinel.Process.Pid
			sentinelDone := make(chan struct{})
			go func() {
				_ = sentinel.Wait()
				close(sentinelDone)
			}()
			t.Cleanup(func() {
				_ = syscall.Kill(-group, syscall.SIGKILL)
				<-sentinelDone
			})

			r := startTerminalRun(t, terminalOpts{mode: "killgroup", abi: branch.abi, joinGroup: group})
			if _, ok := r.waitFile("ready", 30*time.Second); !ok {
				r.failOrSkip("the contained target never started")
			}
			if !r.waitExit(30 * time.Second) {
				t.Fatalf("the run was still going 30 seconds on\noutput:\n%s", r.out.String())
			}
			ws, _ := r.cmd.ProcessState.Sys().(syscall.WaitStatus)
			if ws.Signaled() {
				t.Errorf("a contained kill(0, SIGKILL) killed nvx (%v)\noutput:\n%s", ws.Signal(), r.out.String())
			} else if ws.ExitStatus() != 128+int(syscall.SIGKILL) {
				// The target ends itself, so its own group is the one signalled.
				t.Errorf("nvx exited %d, want %d from the target's own death\noutput:\n%s",
					ws.ExitStatus(), 128+int(syscall.SIGKILL), r.out.String())
			}
			select {
			case <-sentinelDone:
				t.Errorf("a contained kill(0, SIGKILL) killed a process outside the sandbox in nvx's group (%v)", sentinel.ProcessState)
			case <-time.After(time.Second):
			}
		})
	}
}

// In network.mode open the sandbox shares the host's network namespace, and an
// abstract socket has no path for the sandbox's filesystem view to hide.
// Measured 2026-10-07 on Linux 6.18, a contained process connected to one a host
// process listened on and read its reply. Xvfb listens on one. The Landlock
// scope refuses the connection.
func TestContainedProcessCannotReachAHostAbstractSocket(t *testing.T) {
	// Asked of the kernel, not of landlockScopesForABI, which is under test.
	if abi := landlockABIVersion(); abi < 6 {
		t.Skipf("this kernel speaks Landlock ABI v%d, and scoping abstract sockets needs v6 (Linux 6.12)", abi)
	}
	name := fmt.Sprintf("@nvx-test-abstract-%d", os.Getpid())
	ln, err := net.Listen("unix", name)
	if err != nil {
		t.Fatalf("listen on %s: %v", name, err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()

	r := startTerminalRun(t, terminalOpts{mode: "abstract", env: []string{abstractSocketEnv + "=" + name}})
	got, ok := r.waitFile("abstract", 30*time.Second)
	if !ok {
		r.failOrSkip("the contained target never reported")
	}
	if !strings.Contains(got, "operation not permitted") {
		t.Errorf("connecting to the host's abstract socket %s from the sandbox gave %q, want it refused with EPERM\noutput:\n%s",
			name, got, r.out.String())
	}
}

// Ctrl-C at a terminal stops a contained npm script and an npx tool, on both
// branches.
//
// The commands are real npm. npm passes SIGINT to the shell that runs the script,
// and the shell waits for its child without passing it on. So the server at the
// end of the chain gets Ctrl-C only from the terminal, or from a supervisor that
// signals the target's whole group. Measured 2026-10-07, with the target in a
// group of its own and only the target signalled, the server never got Ctrl-C.
func TestContainedProcessStopsOnCtrlCUnderNpm(t *testing.T) {
	npm, npx, roots := realNpm(t)
	for _, branch := range terminalBranches {
		for _, tc := range []struct {
			name    string
			command []string
		}{
			{"npm run", []string{npm, "run", "serve"}},
			{"npx", []string{npx, "-y", "serve-probe"}},
		} {
			t.Run(branch.name+"/"+tc.name, func(t *testing.T) {
				r := startTerminalRun(t, terminalOpts{
					terminal: true, abi: branch.abi,
					command: tc.command, execRoots: roots, env: npmTestEnv, prepare: writeNpmProject,
				})
				if _, ok := r.waitFile("ready", 60*time.Second); !ok {
					r.failOrSkip("the contained server never started")
				}
				r.typeBytes("\x03")
				if _, ok := r.waitFile("interrupted", 10*time.Second); !ok {
					t.Fatalf("the server never got Ctrl-C 10 seconds on\noutput:\n%s", r.out.String())
				}
				if !r.waitExit(10 * time.Second) {
					t.Errorf("the run was still going 10 seconds after Ctrl-C\noutput:\n%s", r.out.String())
				}
			})
		}
	}
}

// npm passes a signal on to the script it runs, and the signal scope must leave
// that alone. The script is exec'd, so npm's child is node. Where the kernel
// scopes signals the target stays in nvx's group and the supervisor signals npm
// alone, so only npm can stop node.
func TestContainedProcessNpmPassesSignalsToItsScript(t *testing.T) {
	npm, _, roots := realNpm(t)
	r := startTerminalRun(t, terminalOpts{
		command: []string{npm, "run", "hold"}, execRoots: roots, env: npmTestEnv, prepare: writeNpmProject,
	})
	if _, ok := r.waitFile("ready", 60*time.Second); !ok {
		r.failOrSkip("the contained script never started")
	}
	r.signalNvx(syscall.SIGTERM)
	if _, ok := r.waitFile("terminated", 10*time.Second); !ok {
		t.Fatalf("the script never got the SIGTERM npm passes on\noutput:\n%s", r.out.String())
	}
	if !r.waitExit(10 * time.Second) {
		t.Errorf("the run was still going 10 seconds after SIGTERM\noutput:\n%s", r.out.String())
	}
}

// npmTestEnv keeps npm off the network and quiet.
var npmTestEnv = []string{
	"npm_config_update_notifier=false",
	"npm_config_fund=false",
	"npm_config_audit=false",
	"npm_config_offline=true",
}

// realNpm finds node, npm and npx, and the directories a contained run must read
// and execute them from. Skips when there is no npm.
func realNpm(t *testing.T) (npm, npx string, roots []string) {
	t.Helper()
	paths := map[string]string{}
	for _, name := range []string{"node", "npm", "npx"} {
		p, err := exec.LookPath(name)
		if err != nil {
			t.Skipf("%s is not on PATH: %v", name, err)
		}
		resolved, err := filepath.EvalSymlinks(p)
		if err != nil {
			t.Skipf("resolve %s: %v", p, err)
		}
		paths[name] = p
		// bin/node is in Node's prefix, and npm's bin/npm-cli.js in its package.
		roots = append(roots, filepath.Dir(p), filepath.Dir(filepath.Dir(resolved)))
	}
	return paths["npm"], paths["npx"], roots
}

// npmServer is a server like http-server. It says when it is listening and what
// stopped it.
const npmServer = `#!/usr/bin/env node
const fs = require('fs')
const path = require('path')
const dir = process.env.NVX_TEST_TERMINAL_DIR
process.on('SIGINT', () => { fs.writeFileSync(path.join(dir, 'interrupted'), ''); process.exit(130) })
process.on('SIGTERM', () => { fs.writeFileSync(path.join(dir, 'terminated'), ''); process.exit(0) })
fs.writeFileSync(path.join(dir, 'ready'), '')
setInterval(() => {}, 1000)
`

// writeNpmProject writes a project with the server as an npm script and as a
// package's bin for npx, and the temp directory the guest home has in a real run.
func writeNpmProject(work, guest string) {
	files := map[string]string{
		"package.json":                          `{"name":"signal-probe","version":"1.0.0","scripts":{"serve":"node server.js","hold":"exec node server.js"}}`,
		"server.js":                             npmServer,
		"node_modules/serve-probe/package.json": `{"name":"serve-probe","version":"1.0.0","bin":{"serve-probe":"server.js"}}`,
		"node_modules/serve-probe/server.js":    npmServer,
	}
	for name, body := range files {
		p := filepath.Join(work, name)
		_ = os.MkdirAll(filepath.Dir(p), 0o700)
		_ = os.WriteFile(p, []byte(body), 0o700)
	}
	_ = os.MkdirAll(filepath.Join(work, "node_modules", ".bin"), 0o700)
	_ = os.Symlink("../serve-probe/server.js", filepath.Join(work, "node_modules", ".bin", "serve-probe"))
	_ = os.MkdirAll(filepath.Join(guest, "tmp"), 0o700)
}
