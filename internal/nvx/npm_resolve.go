package nvx

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Asking npm what an install will put on disk.
//
// npm's own resolver runs first, with --package-lock-only and --ignore-scripts.
// It reads package metadata, writes a lockfile and runs no install scripts.
// It runs the way the install will, in the same sandbox under the same egress
// rules, with the project folder as its working directory. It writes into a
// scratch copy of package.json, the lockfile and .npmrc under
// node_modules/.cache, so the project's own files are not touched. The
// lockfile it writes lists every package at the version npm chose, with its
// tarball URL and hash, and the checks run on what is not installed already.
// The install then starts from that same lockfile and does not resolve again.
// See npm_resolved_install.go.
//
// A copy of those files cannot stand in for a project whose package.json names
// workspaces or local folders, so those are not resolved, and the run says so.
//
// Measured 2026-10-01 on Windows, a 5-dependency project with no lockfile,
// five runs each. `nvx npm install` took 5283 to 5573 ms with this step, and
// 9950 ms on the first of the five. Without it, 3064 to 3219 ms. It runs only where reading the command line and
// the lockfile would miss packages: named installs, update, dedupe, and a bare
// install whose lockfile does not match package.json.

// npmResolveVerbs are the npm commands that resolve new versions. ci and
// rebuild install what the lockfile already says, which is read directly.
var npmResolveVerbs = append(append([]string{}, refreshVerbs...),
	"install", "i", "in", "ins", "inst", "insta", "instal", "isnt", "isnta", "isntal", "isntall", "add",
	"install-test", "it")

var launchNpmResolution = runNpmResolution

// npmResolvedTargets returns what npm will install for this command, or nil
// when the command is not one npm resolves for, or cannot be resolved on a
// copy, and the caller should read the command line and project instead. A
// non-zero code is a refusal. args is the command as npm reads it (see
// readCommand), and req.pmArgs as typed is what npm's resolution step runs.
func npmResolvedTargets(req verifyRequest, args []string, platform nodePlatform) ([]verifyTarget, int, string) {
	i := commandVerbIndex(args, npmResolveVerbs...)
	if i < 0 {
		return nil, 0, ""
	}
	verb := strings.ToLower(args[i])
	if verb == "rebuild" || verb == "rb" {
		return nil, 0, ""
	}
	named := positionalsAfter(args, i)
	// A named package on the blocklist is refused before npm is asked about
	// it. The full checks run on the resolved set below.
	if policy, err := LoadPolicy(req.nvxHome); err == nil {
		for _, spec := range named {
			if name := targetPackageName(verifyTarget{spec: spec}); name != "" && refuseBlocked(policy, req.nvxHome, name) {
				return nil, 1, blockedReason
			}
		}
	}
	root := projectManifestDir()
	manifest, hasManifest := readManifestDeps(root)

	// A bare install whose lockfile was written for this package.json installs
	// that lockfile, which the checks read as it is.
	if len(named) == 0 && installAliases[verb] && hasManifest {
		if lock, ok, _ := readProjectLockfile(root); ok && lockMatchesManifest(lock, manifest) {
			return nil, 0, ""
		}
	}

	if why := npmResolutionBlocker(req.pmArgs, named, manifest); why != "" {
		LogWarn("nvx checks the packages named on the command line or in the project's files, and not the dependencies npm resolves for them, because %s.", why)
		return nil, 0, ""
	}

	targets, err := resolveNpmInstall(req, root, platform)
	// The sandbox never started npm, so nothing was resolved and nothing was
	// learned about the install. That is nvx failing, and no answer to a
	// question about npm may wave it through. See resolutionNotStarted.
	var notStarted resolutionNotStarted
	if errors.As(err, &notStarted) {
		LogError("nvx could not start npm's resolution step in the sandbox (%s), so it cannot check the packages this install brings in.", notStarted.reason)
		LogInfo("Run the command again. -y and NVX_YES do not approve this, because nothing was checked.")
		recordCheckRefused(req.nvxHome, checkInfo{check: checkResolution, detail: notStarted.Error()})
		return nil, exitRefused, resolutionSetupReason
	}
	if err == nil {
		// The user chose what they named, or with no names the project's own
		// dependencies. Everything else in the tree came with those.
		chosen := named
		if len(chosen) == 0 {
			chosen = manifestSpecs(manifest)
		}
		return markTransitive(targets, chosen), 0, ""
	}
	msg := fmt.Sprintf("npm could not work out what this command installs (%v), so nvx can check only the packages named on the command line or in the project's files. Proceed?", err)
	if !askCheck(req.nvxHome, checkInfo{check: checkResolution, detail: err.Error(),
		what:    "the install goes ahead with only the named packages checked",
		aborted: "Installation aborted: npm's dependency resolution failed and proceeding was not approved."},
		msg, checkRemedy{text: resolutionRemedy}) {
		// npm exiting non-zero is npm's failure. The resolver is npm running the
		// command the person typed, so what stopped it stops the real install the
		// same way. A project whose devEngines asks for another Node.js got
		// EBADDEVENGINES from npm and exit 77 from nvx, the code for nvx itself
		// refusing. Everything else that goes wrong here is nvx's, and stays a refusal.
		var failed resolutionFailed
		if errors.As(err, &failed) && failed.code > 0 {
			return nil, failed.code, resolutionFailedReason
		}
		return nil, 1, resolutionSetupReason
	}
	return nil, 0, ""
}

// resolutionFailedReason is the refusal reason when npm's own resolver failed,
// and resolutionSetupReason when nvx could not run it or read what it wrote.
const (
	resolutionFailedReason = "npm could not resolve what this command installs"
	resolutionSetupReason  = "nvx could not check what npm would install"
)

// resolutionFailed is npm's lockfile-only run exiting non-zero, with its code.
type resolutionFailed struct{ code int }

func (e resolutionFailed) Error() string {
	return fmt.Sprintf("its lockfile-only run exited with %d", e.code)
}

// resolutionNotStarted is the sandbox refusing to start npm's resolution step,
// with the refusal's reason.
//
// It was a resolutionFailed carrying nvx's own exit code 77, which made it look
// like npm failing and put it behind the same question. With NVX_YES set,
// measured 2026-10-07: "CreateProcess(AppContainer) ... The parameter is
// incorrect" on the resolution step, then "Approved without asking (NVX_YES):
// the install goes ahead with only the named packages checked".
type resolutionNotStarted struct{ reason string }

func (e resolutionNotStarted) Error() string {
	return "the sandbox did not start it: " + e.reason
}

const resolutionRemedy = "npm's own message, above, says why it could not resolve the install. No policy setting waives this." +
	" To proceed with only the named packages checked, set NVX_YES=true or put -y before the command, which approves every check in the run."

// npmResolutionBlocker says why a command cannot be resolved on a copy of
// package.json and the lockfile, or returns "".
func npmResolutionBlocker(args, named []string, m manifestDeps) string {
	if ws := strings.TrimSpace(string(m.Workspaces)); ws != "" && ws != "null" {
		return "the project uses npm workspaces"
	}
	for _, a := range args {
		if a == "--" {
			break
		}
		flag, _, _ := strings.Cut(a, "=")
		switch flag {
		case "--prefix", "-C", "--workspace", "-w", "--workspaces", "--ws", "--include-workspace-root", "-g", "--global", "--location":
			return "the command names a workspace or another folder"
		}
	}
	for _, spec := range named {
		if isLocalSpec(spec) {
			return "it installs from a local folder or file"
		}
	}
	for _, deps := range m.depMaps() {
		for name, spec := range deps {
			if isLocalSpec(name + "@" + spec) {
				return "package.json has a dependency on a local folder or file"
			}
		}
	}
	return ""
}

// isLocalSpec reports a spec that points at the local disk, which a scratch
// copy elsewhere would resolve differently.
func isLocalSpec(spec string) bool {
	switch nonRegistrySpecKind(spec) {
	case "a file: spec", "a local path", "a tarball", "a link: spec", "a workspace: spec", "a portal: spec":
		return true
	}
	return false
}

// resolveNpmInstall runs npm's resolver on a scratch copy of the project and
// returns the lockfile it wrote, as targets.
func resolveNpmInstall(req verifyRequest, root string, platform nodePlatform) ([]verifyTarget, error) {
	cacheDir := filepath.Join(root, "node_modules", ".cache")
	created := missingDirs(cacheDir)
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return nil, fmt.Errorf("could not create a scratch folder: %w", err)
	}
	defer func() {
		// Only the folders made here, and only when empty.
		for _, d := range created {
			_ = os.Remove(d)
		}
	}()
	scratch, err := os.MkdirTemp(cacheDir, "nvx-resolve-")
	if err != nil {
		return nil, fmt.Errorf("could not create a scratch folder: %w", err)
	}
	defer func() { _ = os.RemoveAll(scratch) }()

	// What the pass is given is kept as well, so that the install can tell
	// whether the project changed while the checks ran. See adopt.
	given := map[string][]byte{}
	for _, name := range []string{"package.json", "package-lock.json", "npm-shrinkwrap.json", ".npmrc"} {
		data, err := readIfExists(filepath.Join(root, name))
		if err != nil {
			return nil, fmt.Errorf("could not copy %s: %w", name, err)
		}
		if data == nil {
			continue
		}
		given[name] = data
		if err := os.WriteFile(filepath.Join(scratch, name), data, 0o600); err != nil {
			return nil, fmt.Errorf("could not copy %s: %w", name, err)
		}
	}

	cfg := req.launch
	cfg.Command = "npm"
	cfg.Args = npmResolutionArgs(req.pmArgs, scratch)
	cfg.WorkDir = root
	cfg.ToolName = ""
	// A refusal means npm never ran. Tried once more, since the launch failure
	// seen in practice was transient, and then reported as not started.
	refusal := ""
	cfg.OnRefusal = func(reason string) { refusal = reason }
	LogDetail("Asking npm which packages this command installs, so each one is checked first.")
	code := launchNpmResolution(cfg, req.contain)
	if refusal != "" {
		LogWarn("The sandbox did not start npm's resolution step. Trying once more.")
		refusal = ""
		code = launchNpmResolution(cfg, req.contain)
		if refusal != "" {
			return nil, resolutionNotStarted{reason: refusal}
		}
	}
	if code != 0 {
		return nil, resolutionFailed{code: code}
	}
	lock, ok, err := readProjectLockfile(scratch)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("its lockfile-only run wrote no lockfile")
	}
	// The tree npm wrote holds the whole project. What is installed already at
	// the same version stays as it is, so `npm install left-pad` in a large
	// project checks left-pad and what it brings in.
	targets := lockTargets(lock, platform, root)
	if targets == nil {
		targets = []verifyTarget{}
	}
	// The install starts from the lockfile that was just checked.
	req.resolved.record(root, scratch, given)
	return targets, nil
}

// npmResolutionArgs is the user's command with the flags that make npm only
// resolve. They go before any "--", after which npm reads package names, and
// after the user's own flags, so they win. --save and --package-lock are
// forced on because --no-save would otherwise leave the lockfile unwritten and
// the named packages out of it. --dry-run=false for the same reason.
func npmResolutionArgs(args []string, scratch string) []string {
	ours := []string{
		"--package-lock-only", "--ignore-scripts", "--no-audit", "--no-fund", "--no-update-notifier", "--loglevel=error",
		"--save=true", "--package-lock=true", "--dry-run=false", "--prefix=" + scratch,
	}
	out := make([]string, 0, len(args)+len(ours))
	inserted := false
	for _, a := range args {
		if a == "--" && !inserted {
			out = append(out, ours...)
			inserted = true
		}
		out = append(out, a)
	}
	if !inserted {
		out = append(out, ours...)
	}
	return out
}

// runNpmResolution starts npm contained or not, as the command itself will
// run. npm prints a one-line summary on stdout, and stdout may be carrying
// something else, so it goes nowhere. Errors go to stderr as usual.
func runNpmResolution(cfg SandboxConfig, contain bool) int {
	restore := quietStdout()
	defer restore()
	if contain {
		return runSandbox(cfg)
	}
	cmd, err := directCommand(cfg.Command, cfg.Args, cfg.NvxHome, false)
	if errors.As(err, new(startError)) {
		LogError("Failed to execute %s: %v", cfg.Command, err)
		return 1
	}
	if err != nil {
		reportNoRealExecutable(cfg.Command, cfg.NvxHome)
		return 127
	}
	cmd.Dir = cfg.WorkDir
	cmd.Stdout = io.Discard
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		return 1
	}
	return 0
}

// missingDirs lists dir and each ancestor of it that does not exist yet,
// deepest first, which is the order to remove them in.
func missingDirs(dir string) []string {
	var out []string
	for d := dir; ; d = filepath.Dir(d) {
		if _, err := os.Stat(d); err == nil || filepath.Dir(d) == d {
			return out
		}
		out = append(out, d)
	}
}

// readIfExists returns the file's bytes, nil when there is no such file, and an
// empty slice that is not nil for a file with nothing in it.
func readIfExists(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if data == nil {
		data = []byte{}
	}
	return data, nil
}
