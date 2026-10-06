//go:build windows

package nvx

// yarn classic reads .yarnrc and .npmrc from every directory between the
// project and the drive root. Under the user's profile that includes the real
// home, whose files the sandbox refuses to open, and yarn treats the resulting
// EPERM as fatal. The walk-up preload reports ENOENT for such a read, and only
// for such a read. This evaluates that in node, outside a container, by making
// the underlying fs calls refuse everything, so the test sees exactly which
// refusals the preload rewrites and which it leaves alone.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWalkUpShimHidesRefusedRcFilesOnlyInCoveredAncestors(t *testing.T) {
	node := realNodeForTest(t)
	guestHome := tempDir(t)
	workDir := tempDir(t)
	shim, err := writeWalkupShim(guestHome)
	if err != nil {
		t.Fatal(err)
	}

	workParent := filepath.Dir(workDir)
	homeParent := filepath.Dir(guestHome)
	grandParent := filepath.Dir(workParent)
	if grandParent == workParent {
		t.Skip("temp directories are laid out so the cases below would overlap")
	}

	// Refused reads that must become ENOENT: an rc file directly inside an
	// ancestor of the working directory or of the home.
	hidden := []string{
		filepath.Join(workParent, ".npmrc"),
		filepath.Join(workParent, ".yarnrc"),
		filepath.Join(homeParent, ".yarnrc.yml"),
		filepath.Join(workParent, ".NPMRC"),
	}
	// Refused reads that must stay EPERM: another name in an ancestor, an rc
	// file in the chain endpoints and in unrelated places, and one nested a
	// level below an ancestor.
	refused := []string{
		filepath.Join(workParent, "secrets.txt"),
		filepath.Join(workParent, "npmrc"),
		filepath.Join(workParent, ".npmrc.bak"),
		filepath.Join(workDir, ".yarnrc"),
		filepath.Join(guestHome, ".npmrc"),
		filepath.Join(workParent, "sibling", ".npmrc"),
		`C:\Windows\System32\.yarnrc`,
	}
	// A hidden rc file the process can open: it exists, and must keep saying so.
	readable := filepath.Join(grandParent, ".yarnrc.yml")

	probe := filepath.Join(workDir, "rcfile.js")
	script := `
const fs = require('fs');
const fsp = fs.promises;
const readable = process.argv[3];
const eperm = (p) => Object.assign(
  new Error("EPERM: operation not permitted, open '" + p + "'"),
  { code: 'EPERM', syscall: 'open', path: p });
const probed = new Set(JSON.parse(process.argv[4]));
// The sandbox's refusal, as the fs functions deliver it, for the probed paths
// only. Installed before the preload loads, so the preload wraps these.
const readFileSync = fs.readFileSync;
const readFile = fs.readFile;
const readFilePromise = fsp.readFile;
const existsSync = fs.existsSync;
const exists = fs.exists;
const openSync = fs.openSync;
const closeSync = fs.closeSync;
fs.readFileSync = (p, ...r) => { if (probed.has(p)) throw eperm(p); return readFileSync(p, ...r); };
fs.readFile = (p, ...r) => {
  if (!probed.has(p)) return readFile(p, ...r);
  const cb = r.pop();
  process.nextTick(() => cb(eperm(p)));
};
fsp.readFile = async (p, ...r) => { if (probed.has(p)) throw eperm(p); return readFilePromise(p, ...r); };
fs.existsSync = (p) => probed.has(p) || existsSync(p);
fs.exists = (p, cb) => (probed.has(p) ? process.nextTick(() => cb(true)) : exists(p, cb));
fs.openSync = (p, ...r) => {
  if (!probed.has(p)) return openSync(p, ...r);
  if (p === readable) return -1;
  throw eperm(p);
};
fs.closeSync = (fd) => (fd === -1 ? undefined : closeSync(fd));
require(process.argv[2]);

(async () => {
  const answer = {};
  for (const p of JSON.parse(process.argv[4])) {
    const a = {};
    try { fs.readFileSync(p); a.sync = 'ok'; } catch (e) { a.sync = e.code; }
    a.cb = await new Promise((res) => fs.readFile(p, 'utf8', (e) => res(e ? e.code : 'ok')));
    a.promise = await fsp.readFile(p).then(() => 'ok', (e) => e.code);
    a.existsSync = fs.existsSync(p);
    a.existsCb = await new Promise((res) => fs.exists(p, res));
    answer[p] = a;
  }
  console.log('RESULT ' + JSON.stringify(answer));
})();
`
	if err := os.WriteFile(probe, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}

	all := append(append(append([]string{}, hidden...), refused...), readable)
	encoded, _ := json.Marshal(all)
	cmd := exec.Command(node, probe, shim, readable, string(encoded))
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), "USERPROFILE="+guestHome, "HOME="+guestHome, "NODE_OPTIONS=")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("evaluating the preload failed: %v\n%s", err, out)
	}
	line := strings.TrimSpace(string(out))
	if i := strings.LastIndex(line, "RESULT "); i >= 0 {
		line = line[i+len("RESULT "):]
	}
	type answer struct {
		Sync, Cb, Promise    string
		ExistsSync, ExistsCb bool
	}
	raw := map[string]map[string]any{}
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		t.Fatalf("could not read the preload's answers: %v\n%s", err, out)
	}
	read := func(p string) answer {
		m := raw[p]
		s := func(k string) string { v, _ := m[k].(string); return v }
		b := func(k string) bool { v, _ := m[k].(bool); return v }
		return answer{s("sync"), s("cb"), s("promise"), b("existsSync"), b("existsCb")}
	}

	for _, p := range hidden {
		a := read(p)
		if a.Sync != "ENOENT" || a.Cb != "ENOENT" || a.Promise != "ENOENT" {
			t.Errorf("a refused read of %s should report ENOENT, got sync=%s callback=%s promise=%s "+
				"(yarn stops on the EPERM)", p, a.Sync, a.Cb, a.Promise)
		}
		if a.ExistsSync || a.ExistsCb {
			t.Errorf("%s cannot be opened, so it should not be reported as existing "+
				"(yarn checks fs.exists before it reads ~/.npmrc): existsSync=%v exists=%v",
				p, a.ExistsSync, a.ExistsCb)
		}
	}
	for _, p := range refused {
		a := read(p)
		if a.Sync != "EPERM" || a.Cb != "EPERM" || a.Promise != "EPERM" {
			t.Errorf("a refused read of %s must stay EPERM, got sync=%s callback=%s promise=%s. "+
				"The preload would be hiding a refusal outside the one place yarn looks.",
				p, a.Sync, a.Cb, a.Promise)
		}
		if !a.ExistsSync || !a.ExistsCb {
			t.Errorf("exists for %s changed: existsSync=%v exists=%v", p, a.ExistsSync, a.ExistsCb)
		}
	}
	a := read(readable)
	if !a.ExistsSync || !a.ExistsCb {
		t.Errorf("%s can be opened, so it must still be reported as existing: existsSync=%v exists=%v",
			readable, a.ExistsSync, a.ExistsCb)
	}
}

// Opt-in verification (NVX_PROBE=1): a .yarnrc in an ancestor of the working
// directory is refused with EPERM inside an AppContainer, as yarn meets the
// one in the real home, and the preload turns that into ENOENT. The file is
// created in the temp directory, which is an ancestor of the probe's working
// directory, and removed afterwards.
func TestWalkUpShimHidesRefusedRcFileInContainer(t *testing.T) {
	run := walkupProbe(t, "nvx.sandbox.rcfile.probe", `
const fs = require('fs');
const path = require('path');
const rc = path.join(path.dirname(process.cwd()), '.yarnrc');
let out;
try { fs.readFileSync(rc); out = 'ok'; } catch (e) { out = e.code; }
fs.writeFileSync(process.argv[2], out);
`)
	rc := filepath.Join(os.TempDir(), ".yarnrc")
	if _, err := os.Stat(rc); err == nil {
		t.Skipf("%s already exists and is not this test's to remove", rc)
	}
	if err := os.WriteFile(rc, []byte("# nvx probe\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(rc) })

	without := run(false)
	if without != "EPERM" {
		t.Skipf("premise not met: the read of %s gave %q without the preload, so there is no refusal to rewrite", rc, without)
	}
	if with := run(true); with != "ENOENT" {
		t.Fatalf("with the preload, the refused read of %s gave %q, and yarn stops on anything but ENOENT", rc, with)
	}
}
