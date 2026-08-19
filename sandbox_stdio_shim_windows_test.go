//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNodeOptionsRequireIsAddedWithoutLosingExistingFlags(t *testing.T) {
	cases := []struct {
		name string
		env  []string
		want []string // substrings that must all be present in NODE_OPTIONS
	}{
		{
			// The preserve-symlinks flags are load-bearing: without them node
			// realpaths to the drive root, which the sandbox cannot stat.
			name: "keeps the preserve-symlinks flags",
			env:  []string{"NODE_OPTIONS=--preserve-symlinks --preserve-symlinks-main"},
			want: []string{"--preserve-symlinks", "--preserve-symlinks-main", "--require"},
		},
		{
			name: "adds the variable when absent",
			env:  []string{"PATH=C:\\x"},
			want: []string{"--require"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := addNodeOptionsRequire(tc.env, `C:\guest home\shim.js`)
			var opts string
			for _, e := range got {
				if strings.HasPrefix(e, "NODE_OPTIONS=") {
					opts = e
				}
			}
			for _, w := range tc.want {
				if !strings.Contains(opts, w) {
					t.Errorf("NODE_OPTIONS = %q, missing %q", opts, w)
				}
			}
			// A profile path can contain spaces, so the path must be quoted or node
			// reads the second word as another flag.
			if !strings.Contains(opts, `"C:/guest home/shim.js"`) {
				t.Errorf("the shim path is not quoted in %q", opts)
			}
		})
	}
}

// Adding it twice would grow NODE_OPTIONS on every nested contained process.
func TestNodeOptionsRequireIsNotAddedTwice(t *testing.T) {
	once := addNodeOptionsRequire([]string{"NODE_OPTIONS=--preserve-symlinks"}, `C:\g\`+stdioShimName)
	twice := addNodeOptionsRequire(once, `C:\g\`+stdioShimName)
	if len(strings.Split(twice[0], "--require")) != 2 {
		t.Errorf("--require appears more than once: %q", twice[0])
	}
}

// The mechanism itself, against real node: a process that captures a subprocess's
// output must get that output. Run OUTSIDE a sandbox because what is being checked
// here is that the shim is a faithful substitute -- that it returns what piped
// stdio would have returned. Whether it rescues the contained case is proved by
// TestContainedSyncCaptureWorks below.
func TestStdioShimIsAFaithfulSubstituteForPipedCapture(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not on PATH")
	}
	dir := t.TempDir()
	shim, err := writeStdioShim(dir)
	if err != nil {
		t.Fatalf("writeStdioShim: %v", err)
	}

	script := filepath.Join(dir, "probe.js")
	body := `
const cp = require('child_process');
const node = process.execPath;
// 1. spawnSync captures both streams and reports status.
const a = cp.spawnSync(node, ['-e', 'process.stdout.write("OUT");process.stderr.write("ERR");process.exit(3)'], {encoding:'utf8'});
console.log('A', JSON.stringify(a.stdout), JSON.stringify(a.stderr), a.status);
// 2. execFileSync returns stdout, and honours encoding.
console.log('B', JSON.stringify(cp.execFileSync(node, ['-e', 'process.stdout.write("EFS")'], {encoding:'utf8'})));
// 3. a non-zero exit throws, with stdout/stderr attached to the error.
try {
  cp.execFileSync(node, ['-e', 'process.stderr.write("BOOM");process.exit(4)'], {encoding:'utf8'});
  console.log('C', 'DID_NOT_THROW');
} catch (e) {
  console.log('C', e.status, JSON.stringify(String(e.stderr)));
}
// 4. input is delivered to the child's stdin.
console.log('D', JSON.stringify(cp.execFileSync(node,
  ['-e', 'let s="";process.stdin.on("data",d=>s+=d).on("end",()=>process.stdout.write("GOT:"+s))'],
  {encoding:'utf8', input:'FED'})));
// 5. Buffer output when no encoding is given, as node does.
const e5 = cp.spawnSync(node, ['-e', 'process.stdout.write("BUF")']);
console.log('E', Buffer.isBuffer(e5.stdout), JSON.stringify(e5.stdout.toString()));
// 6. stdio:'inherit' is left alone -- nothing to substitute, and stdout stays null.
const f = cp.spawnSync(node, ['-e', 'process.stdout.write("")'], {stdio:'inherit'});
console.log('F', f.status, f.stdout === null);
`
	if err := os.WriteFile(script, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	run := func(withShim bool) string {
		cmd := exec.Command(node, script)
		cmd.Dir = dir
		if withShim {
			cmd.Env = append(os.Environ(), "NODE_OPTIONS=--require "+strings.ReplaceAll(shim, `\`, `/`))
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("node probe (shim=%v) failed: %v\n%s", withShim, err, out)
		}
		return string(out)
	}

	plain := run(false)
	shimmed := run(true)

	// The point of the shim is that a caller cannot tell the difference, so the
	// strongest assertion available is that both runs agree exactly.
	if plain != shimmed {
		t.Errorf("the shim changed observable behaviour.\nwithout shim:\n%s\nwith shim:\n%s", plain, shimmed)
	}
	for _, want := range []string{
		`A "OUT" "ERR" 3`,
		`B "EFS"`,
		`C 4 "BOOM"`,
		`D "GOT:FED"`,
		`E true "BUF"`,
		`F 0 true`,
	} {
		if !strings.Contains(shimmed, want) {
			t.Errorf("missing %q in shimmed output:\n%s", want, shimmed)
		}
	}
}
