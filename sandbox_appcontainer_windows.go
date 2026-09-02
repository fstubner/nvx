//go:build windows

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

var (
	modUserenv = syscall.NewLazyDLL("userenv.dll")

	procCreateAppContainerProfile                 = modUserenv.NewProc("CreateAppContainerProfile")
	procDeriveAppContainerSidFromAppContainerName = modUserenv.NewProc("DeriveAppContainerSidFromAppContainerName")
	procDeleteAppContainerProfile                 = modUserenv.NewProc("DeleteAppContainerProfile")
)

// prepareAppContainerFilesystem grants the AppContainer SID write access to
// guestHome and workDir -- and nothing else. That pair is sandboxWritableRoots;
// see its comment for why nvxHome must never be added here. The two are granted
// separately rather than in a loop because their failure handling differs: the
// guest home is required and also takes an integrity label, while the working
// directory is best-effort. Anything beyond these two is a write-containment
// escape, not a convenience.
//
// guestHome gets a low mandatory integrity label for compatibility with legacy
// constrained launches; workDir stays default integrity so a normal AppContainer
// child can use it as cwd.
// It returns the capability SIDs the launch must carry to make use of those
// grants: the writable roots are granted to a per-project capability rather than
// to the shared AppContainer SID, so a session in one project cannot reach
// another's. See sandbox_scope_identity_windows.go for why.
func prepareAppContainerFilesystem(sid uintptr, nvxHome, guestHome, workDir string) ([]string, error) {
	packageSIDStr, err := appContainerSidToString(sid)
	if err != nil {
		return nil, err
	}

	// Fresh homes get a traverse-only grant; homes created before 0.5.0 carry a
	// broader one that lets the sandbox list ~/.nvx. Runs once per home.
	narrowLegacyHomeGrant(packageSIDStr, nvxHome)

	// The identity that owns this session's writable roots. Derived from the
	// project, so it is the same every run here and different from every other
	// project.
	capSID, err := scopeCapabilitySID(sandboxScopeForWorkDir(workDir))
	if err != nil {
		return nil, fmt.Errorf("derive this project's sandbox identity: %w", err)
	}
	caps := []string{capSID}

	// Windows grants the same pair sandboxWritableRoots declares, but cannot share
	// its loop: the guest home is required and takes an integrity label, while the
	// working directory is best-effort and skipped at the profile root. Writing the
	// pair out again is what let the two drift -- an acceptance pass widened
	// sandboxWritableRoots to include the working directory's PARENT, watched the
	// unit tests go red, and watched the real Windows containment probe stay green,
	// because nothing on this path read the declaration.
	//
	// So the declaration is read here, and anything in it this function does not
	// know how to grant is a hard failure. That is the F22 shape -- two callers
	// disagreeing about what may be written -- caught on Windows too, and caught
	// fail-closed: a writable root nvx cannot implement must stop the launch, not
	// silently make Windows narrower than the platform whose behaviour the tests
	// describe.
	for _, root := range sandboxWritableRoots(guestHome, workDir) {
		if !dirsEqual(root, guestHome) && !dirsEqual(root, workDir) {
			return nil, fmt.Errorf("sandboxWritableRoots declares %q writable and the Windows sandbox "+
				"does not implement it; refusing to launch narrower than the declared policy", root)
		}
	}

	// The guest home must be writable; it is nvx-owned and safe to grant.
	if guestHome != "" {
		if err := grantSandboxModify(capSID, guestHome); err != nil {
			return nil, err
		}
		removeStaleAppContainerGrant(packageSIDStr, guestHome)
		if err := labelLowIntegrity(guestHome); err != nil {
			return nil, fmt.Errorf("integrity label for %q: %w", guestHome, err)
		}
	}

	// The working-directory grant is best-effort. Many commands (e.g. npx) never
	// write the cwd, and the profile root both cannot be granted in any useful time
	// (its ACL write propagates over the whole profile tree) and already grants ALL
	// APPLICATION PACKAGES for stat/traverse. Sandbox writes go to the guest home
	// regardless, so a failed workdir grant should not abort the run.
	if workDir != "" && !isProfileRoot(workDir) {
		if err := grantSandboxModify(capSID, workDir); err != nil {
			LogWarn("Could not grant the sandbox write access to %q: %v", workDir, err)
			LogInfo("Commands that write the current folder may fail here; run from a project subfolder, or use --no-sandbox.")
		}
		removeStaleAppContainerGrant(packageSIDStr, workDir)
	}
	// Tools stat the ancestors of both the working directory and the guest home
	// (which is HOME inside the sandbox), so grant traverse on both chains.
	//
	// These stay on the shared package SID rather than the per-project capability.
	// They are this-folder-only traverse+stat (X,RA) -- enough to walk through a
	// directory, not to list it -- so sharing them leaks nothing: a sibling project's
	// contents are still gated by its own capability. Keeping them shared also
	// keeps them idempotent across projects, which is what stops the ancestor walk
	// from re-granting the same chain for every project on the machine.
	aWork, eWork := grantWorkdirAncestors(sid, nvxHome, workDir)
	if skipped := eWork - aWork; skipped > 0 {
		// Only the project chain is advisory: the command runs without those, and
		// skipping a known-slow one is what keeps startup fast. Silence would hide a
		// genuinely slow filesystem, so report once per launch.
		LogInfo("Skipped %d of %d ancestor permission checks to keep startup fast.", skipped, eWork)
	}

	// The guest home's own chain is NOT advisory, and treating it as though it were
	// is what broke contained npx on the development machine for eleven days. npm
	// walks up from the guest home and stats every directory on the way; with
	// ~/.nvx/sandbox_home recorded as a failed grant and therefore not retried,
	// every `nvx npx` ended in EPERM on that path -- while nvx reported only that
	// it had skipped some checks to keep startup fast.
	if failed := grantGuestHomeAncestors(sid, guestHome); len(failed) > 0 {
		LogWarn("Could not grant the sandbox stat access on %s.", strings.Join(failed, ", "))
		LogInfo("Tools that walk up from the sandbox's home -- npx does -- will fail there with EPERM. " +
			"This is inside nvx's own directory and needs no elevation; a later run retries it.")
	}
	return caps, nil
}

// sandboxScopeForWorkDir returns the project a working directory belongs to, so
// every session in the same project shares one identity and a subdirectory does
// not become its own isolated island. Mirrors projectScopeDir, but as a function
// of the argument rather than the process's cwd.
func sandboxScopeForWorkDir(workDir string) string {
	if workDir == "" {
		return ""
	}
	if root := findProjectRoot(workDir); root != "" {
		return root
	}
	return filepath.Clean(workDir)
}

// grantSandboxReadExec gives one identity read and execute on path and its
// descendants. Never write, whatever the caller asks.
// See grantAppContainerPath for why the ACE is inheritable rather than /t.
//
// Takes a SID string rather than the package handle so the caller can scope this
// to THIS PROJECT's capability. The first version granted the shared package SID,
// which would have let every sandbox on the machine read and execute from a
// directory one project asked for -- and because these ACEs persist on disk,
// removing the policy entry would not have taken it back. Scoping to the
// capability means only sandboxes holding this project's identity are admitted.
// Returns whether an entry was actually written. The caller records only what
// nvx wrote, because withdrawing is not selective: icacls removes an identity's
// whole granted entry, not one right from it. Recording a grant that was skipped
// -- because a modify entry already covered read and execute -- meant a later
// withdrawal deleted the WRITE access that entry was really there for, and the
// project's own directory became unusable: "chdir: Access is denied", measured.
func grantSandboxReadExec(sidStr, path string) (ours bool, err error) {
	if appContainerHasGrantFor(sidStr, path, grantReadExec) {
		// Already satisfied -- but by what? An entry nvx wrote on an earlier run is
		// this feature's to take back; someone else's broader entry is not.
		//
		// Answering "no" to both was a defect with no way out. A record can be lost
		// -- a corrupt ledger, a deleted grants directory, one failed save -- and
		// every later run then saw its own entry, declined to record it, and left a
		// permission nvx had granted that nothing could ever withdraw. Measured:
		// with the record removed, three further runs re-confirmed the entry and
		// recorded nothing, and emptying the policy withdrew nothing.
		return readExecEntryIsOurs(sidStr, path), nil
	}
	if err := grantACL(path, sidStr, aclMaskReadExec, nvxInheritFlags); err != nil {
		return false, fmt.Errorf("read/execute grant for sandbox identity: %w", err)
	}
	// Confirm it landed. Cheap, and the one thing worth keeping from the era of
	// deciding this from a command's exit code.
	if !readExecEntryIsOurs(sidStr, path) {
		return false, fmt.Errorf("no read/execute permission is present on %s after granting it", path)
	}
	return true, nil
}

// readExecGrantWouldBeOurs reports whether the read/execute permission on path
// is, or is about to be, the one nvx wrote for sidStr.
//
// Asked BEFORE the record is written, because the record has to be written before
// the permission is granted -- a permission nvx cannot record is one it could
// never withdraw -- and recording one that will not be nvx's is how a withdrawal
// came to delete write access nvx had granted for a different reason.
//
// Three cases: an entry that is exactly nvx's is ours; a broader entry that
// merely covers read and execute is not, and nvx will skip granting over it; and
// no satisfying entry at all means nvx is about to write its own.
func readExecGrantWouldBeOurs(sidStr, path string) bool {
	if !appContainerHasGrantFor(sidStr, path, grantReadExec) {
		return true
	}
	return readExecEntryIsOurs(sidStr, path)
}

// nvxInheritFlags is the inheritance nvx applies to the permissions it grants:
// the entry applies to this directory and is inherited by files and directories
// under it. Together with an exact access mask this is the signature that
// identifies an entry as nvx's own.
const nvxInheritFlags = objectInheritACE | containerInheritACE

// readExecEntryIsOurs reports whether path carries the precise entry
// grantSandboxReadExec writes for sidStr.
//
// Exact, not "contains RX". A broader entry that happens to include read and
// execute -- a modify grant for the working directory, say -- is not this
// feature's to remove, because withdrawing takes the identity's whole entry and
// would delete the write access with it. An inherited entry prints with a leading
// (I) and so does not match either, which is right: it lives on an ancestor and
// removing it here would do nothing.
func readExecEntryIsOurs(sidStr, path string) bool {
	e, ok, err := aclEntryFor(path, sidStr)
	if err != nil || !ok {
		return false
	}
	return !e.Deny && e.Mask == aclMaskReadExec && e.Flags&^inheritedACE == nvxInheritFlags
}

// grantSandboxModify gives sidStr modify access to path and its descendants.
func grantSandboxModify(sidStr, path string) error {
	if appContainerHasGrantFor(sidStr, path, grantModify) {
		return nil
	}
	if err := grantACL(path, sidStr, aclMaskModify, nvxInheritFlags); err != nil {
		return fmt.Errorf("modify grant for sandbox identity: %w", err)
	}
	return nil
}

func isProfileRoot(dir string) bool {
	up := os.Getenv("USERPROFILE")
	return up != "" && strings.EqualFold(filepath.Clean(dir), filepath.Clean(up))
}

// grantWorkdirAncestors grants the AppContainer this-folder traverse+stat on each ancestor
// directory of workDir that sits strictly below the user profile root, so tools
// that stat ancestors (npm walking up to find a project root) succeed. It stops
// at the profile root: that root already grants ALL APPLICATION PACKAGES (and
// writing its ACL propagates over the entire profile, which takes minutes), and C:\ /
// C:\Users are handled once by `nvx setup`. Best-effort and time-boxed.
// grantWorkdirAncestors returns how many ancestor grants it attempted and how many
// were eligible, so the caller can report once for the whole launch rather than
// once per chain.
func grantWorkdirAncestors(sid uintptr, nvxHome, workDir string) (attempted, eligible int) {
	paths := ancestorGrantPaths(workDir, os.Getenv("USERPROFILE"))
	if len(paths) == 0 {
		return 0, 0
	}
	// Skip grants already known to fail on this machine. They cost the whole
	// budget every launch and buy nothing -- see sandbox_ancestor_skip_windows.go.
	attempted = grantAncestorsSkippingKnownFailures(nvxHome, paths, func(p string) error {
		return grantAppContainerPathReadExecTimeboxed(sid, p, ancestorGrantPerPath)
	})
	return attempted, len(paths)
}

// grantGuestHomeAncestors grants traverse+stat on the chain above the guest home
// and returns the paths that could not be granted.
//
// Separate from grantWorkdirAncestors because the two chains are not the same
// kind of thing. The project chain is advisory -- a contained command runs
// without it -- so a grant there that has proved slow is worth skipping. This
// chain is what npm walks when it starts in the sandbox's own home, and without
// it `npx` fails outright. See grantRequiredAncestors for the measurement.
func grantGuestHomeAncestors(sid uintptr, guestHome string) []string {
	paths := ancestorGrantPaths(guestHome, os.Getenv("USERPROFILE"))
	if len(paths) == 0 {
		return nil
	}
	return grantRequiredAncestors(paths, func(p string) error {
		return grantAppContainerPathReadExecTimeboxed(sid, p, ancestorGrantPerPath)
	})
}

// isPathStrictlyUnder reports whether path is a proper descendant of base.
func isPathStrictlyUnder(path, base string) bool {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return false
	}
	return rel != "." && !strings.HasPrefix(rel, "..")
}

func ensureAppContainerSID(profileName string) (uintptr, error) {
	name, err := syscall.UTF16PtrFromString(profileName)
	if err != nil {
		return 0, err
	}
	display, _ := syscall.UTF16PtrFromString("nvx sandbox")
	desc, _ := syscall.UTF16PtrFromString("Ephemeral nvx execution sandbox")

	// Register the profile FIRST. DeriveAppContainerSidFromAppContainerName
	// succeeds for any valid name whether or not a profile is registered, so
	// deriving first would skip CreateAppContainerProfile and leave the SID
	// unbacked — CreateProcess then fails with "cannot find the file specified".
	// Creating first is idempotent: an existing profile returns ALREADY_EXISTS,
	// after which we derive the SID for the registered profile.
	var newSid uintptr
	hr, _, createErr := procCreateAppContainerProfile.Call(
		uintptr(unsafe.Pointer(name)),
		uintptr(unsafe.Pointer(display)),
		uintptr(unsafe.Pointer(desc)),
		0, 0,
		uintptr(unsafe.Pointer(&newSid)),
	)
	if hr == 0 && newSid != 0 {
		return newSid, nil
	}

	// Profile already exists (or create failed) — derive the SID for it.
	var sid uintptr
	dhr, _, deriveErr := procDeriveAppContainerSidFromAppContainerName.Call(
		uintptr(unsafe.Pointer(name)),
		uintptr(unsafe.Pointer(&sid)),
	)
	if dhr == 0 && sid != 0 {
		return sid, nil
	}

	return 0, fmt.Errorf("AppContainer profile unavailable (create hr=0x%X: %v; derive hr=0x%X: %v)", hr, createErr, dhr, deriveErr)
}

func deleteAppContainerProfile(profileName string) {
	name, err := syscall.UTF16PtrFromString(profileName)
	if err != nil {
		return
	}
	_, _, _ = procDeleteAppContainerProfile.Call(uintptr(unsafe.Pointer(name)))
}

// appContainerHasGrant reports whether path already carries an allow ACE for
// sidStr, whether set directly or inherited from an ancestor. A non-recursive
// ACL read costs ~50ms even on a tree with tens of thousands of files, which
// makes the far more expensive grant below idempotent: after the first run the
// ACE is already in place and there is nothing to do.
func appContainerHasGrant(sidStr, path string) bool {
	return appContainerHasGrantFor(sidStr, path, grantModify)
}

// grantKind distinguishes the two rights nvx grants, because "does this SID
// appear in the ACL" is not the same question as "does it hold the access we are
// about to skip granting".
//
// Conflating them was a real defect. A directory granted read/execute by
// allow_read_exec, then later used as a working directory, kept its read-only
// ACE for ever: the modify grant saw the SID, concluded the work was done, and
// skipped it. Every write inside that directory failed with EPERM, and nothing
// in the product could clear it -- repeat runs, `grants reset --all`, `doctor
// --fix` and deleting the policy entry all left it broken. Measured 2026-08-28.
type grantKind int

const (
	grantModify grantKind = iota
	grantReadExec
)

// mask is the access this kind of grant requires. Comparing masks is what
// replaced reading icacls output: a directory named "d(M)x" once reported modify
// access from an entry granting only read and execute, because the line being
// searched began with the path.
func (k grantKind) mask() uint32 {
	if k == grantReadExec {
		return aclMaskReadExec
	}
	return aclMaskModify
}

// appContainerHasGrantFor asks the question appContainerHasGrant should always
// have asked: does sidStr already hold AT LEAST the access `want` on path?
func appContainerHasGrantFor(sidStr, path string, want grantKind) bool {
	// Read/execute is asked afresh every time; only the modify answer is cached.
	//
	// The cache exists for the many ancestor and working-directory checks on the
	// startup path. Read/execute roots are a handful of paths a policy names
	// explicitly, and caching that answer was actively harmful: an entry removed
	// behind nvx's back left the cache reporting it as granted for seven days, so
	// the grant was skipped, the log said it had been made, and the sandbox got
	// EPERM. Reading an ACL is a syscall now rather than a process spawn, so the
	// cache buys much less than it did.
	if want != grantReadExec && grantCacheHas(grantIdentityFor(sidStr, want), path) {
		return true
	}
	entries, err := readDACL(path)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !sidsEqual(e.SID, sidStr) {
			continue
		}
		// An explicit deny for this identity settles it, whatever else is present.
		if e.Deny {
			return false
		}
		if !e.grantsAtLeast(want.mask()) {
			continue
		}
		if want != grantReadExec {
			grantCacheRecord(grantIdentityFor(sidStr, want), path)
		}
		return true
	}
	return false
}

// grantIdentityFor keeps the two rights in separate cache namespaces, by folding
// the right into the identity the cache is keyed on.
func grantIdentityFor(sidStr string, k grantKind) string {
	if k == grantReadExec {
		return sidStr + "|rx"
	}
	return sidStr + "|m"
}

// grantAppContainerPath gives the AppContainer modify access to path and its
// descendants.
//
// (OI)(CI) marks the ACE inheritable, and NTFS propagates it to *existing*
// children as well as new ones, so `/t` is unnecessary. It is also actively
// harmful: `/t` rewrites every descendant's ACL individually, which on a real
// project (measured: 45k files) blows the timeout outright, and it aborts on any
// child whose ACL cannot be rewritten — e.g. a project-local .nvx directory
// carrying ACEs from other tooling, the exact failure users hit as
// "Access is denied" followed by a 20s stall on every single invocation.
func grantAppContainerPath(sid uintptr, path string) error {
	sidStr, err := appContainerSidToString(sid)
	if err != nil {
		return err
	}
	if appContainerHasGrant(sidStr, path) {
		return nil
	}
	if err := grantACL(path, sidStr, aclMaskModify, nvxInheritFlags); err != nil {
		return fmt.Errorf("modify grant for AppContainer: %w", err)
	}
	return nil
}

// ensureAppContainerCommand grants AppContainer read/execute on cmdPath. Binaries
// outside ~/.nvx/versions are copied into nvxHome first so icacls can succeed
// on paths the runner user owns (e.g. hostedtoolcache on GitHub Actions).
func ensureAppContainerCommand(sid uintptr, nvxHome, cmdPath string) (string, error) {
	if cmdPath == "" {
		return "", fmt.Errorf("empty command path")
	}
	cmdPath = filepath.Clean(cmdPath)
	cmdPath = preferWindowsRuntimeExe(cmdPath)
	// Resolve links before anything reads this path.
	//
	// nvm for Windows -- the usual way node gets installed here -- makes
	// C:\Program Files\nodejs a LINK to the active version under %APPDATA%\nvm,
	// and nvx's own ~/.nvx/current is a link into versions/. Both then reach
	// stageAppContainerExecutable, which walks the containing directory;
	// filepath.Walk reports the walk root via Lstat, so a linked root arrives with
	// IsDir() false, gets treated as a file, and the copy dies trying to open the
	// destination directory for writing. Every sandboxed command failed that way
	// for anyone without an nvx-managed runtime.
	//
	// Resolving here rather than inside the staging helper also fixes the
	// isNvxManagedRuntimePath check below, which compares against versions/ and so
	// answered "no" for a path that reached it through ~/.nvx/current -- staging a
	// copy of a runtime that did not need one.
	if resolved, err := filepath.EvalSymlinks(cmdPath); err == nil {
		cmdPath = resolved
	}
	usePath := cmdPath
	if !isNvxManagedRuntimePath(nvxHome, cmdPath) {
		staged, err := stageAppContainerExecutable(nvxHome, cmdPath)
		if err != nil {
			return "", err
		}
		usePath = staged
	}
	dir := filepath.Dir(usePath)
	if err := grantAppContainerPathReadExecTree(sid, dir); err != nil {
		return "", err
	}
	if dir != usePath {
		if err := grantAppContainerPathReadExec(sid, usePath); err != nil {
			return "", err
		}
	}
	// dir's own subtree is now granted, but dir itself commonly sits several
	// levels below the profile root (e.g. ~/.nvx/versions/node/<version>/) —
	// without traverse rights on those intermediate ancestors, the sandboxed
	// process can resolve the binary's parent but fails to lstat/traverse its
	// way there (Node's own realpathSync on argv[0] hits this during startup).
	// Mirrors the same treatment workDir/guestHome already get.
	_, _ = grantWorkdirAncestors(sid, nvxHome, dir)
	return usePath, nil
}

func isNvxManagedRuntimePath(nvxHome, cmdPath string) bool {
	if nvxHome == "" {
		return false
	}
	versionsRoot := filepath.Join(nvxHome, "versions")
	rel, err := filepath.Rel(versionsRoot, cmdPath)
	if err != nil {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".."
}

func stageAppContainerExecutable(nvxHome, cmdPath string) (string, error) {
	cmdPath = filepath.Clean(cmdPath)
	srcDir := filepath.Dir(cmdPath)
	base := filepath.Base(cmdPath)

	st, err := os.Stat(cmdPath)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(strings.ToLower(srcDir) + fmt.Sprintf(":%d", st.ModTime().UnixNano())))
	destDir := filepath.Join(nvxHome, "sandbox-exec", hex.EncodeToString(sum[:16]))
	destExe := filepath.Join(destDir, base)

	if _, err := os.Stat(destExe); err == nil {
		return destExe, nil
	}
	if err := os.MkdirAll(destDir, 0700); err != nil {
		return "", err
	}
	if err := copyDirTree(srcDir, destDir); err != nil {
		return "", err
	}
	return destExe, nil
}

// maxStageDepth bounds the staging recursion. Directory links are followed, so a
// link pointing back into its own tree would otherwise recurse forever and hang a
// sandbox launch with no output. Real runtime trees are nowhere near this deep.
const maxStageDepth = 64

// copyDirTree copies the directory at src, and everything beneath it, to dst.
//
// It recurses with os.ReadDir rather than using filepath.Walk, because Walk
// inspects each path with Lstat: a directory LINK arrives with IsDir() false, is
// taken for a file, and the copy then tries to open dst -- a directory -- for
// writing. That surfaced as "open <nvxHome>\sandbox-exec\<hash>: is a directory"
// and aborted every sandboxed command for anyone whose node came from nvm for
// Windows, which makes C:\Program Files\nodejs a link to the active version.
//
// Windows has two kinds of directory link and they do not behave alike: a symbolic
// link sets ModeSymlink and filepath.EvalSymlinks resolves it, while a junction
// reports ModeIrregular and EvalSymlinks returns it unchanged with no error at all.
// Resolving the path up front therefore fixes only half the cases. os.ReadDir,
// os.Stat and os.Open follow both, so routing everything through them handles the
// pair without having to tell them apart.
//
// Links are followed rather than recreated because the sandbox needs real files: a
// link inside the staged copy would point outside it, where the AppContainer holds
// no grant.
func copyDirTree(src, dst string) error {
	return copyDirTreeAtDepth(src, dst, 0)
}

func copyDirTreeAtDepth(src, dst string, depth int) error {
	if depth > maxStageDepth {
		return fmt.Errorf("cannot stage %s for the sandbox: nesting passed %d levels, which usually means a directory link points back into its own tree", src, maxStageDepth)
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o700); err != nil {
		return err
	}
	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())
		// os.Stat, not the DirEntry's own type: the entry describes what the name
		// IS, and staging needs what it POINTS AT.
		info, err := os.Stat(srcPath)
		if err != nil {
			return fmt.Errorf("stage %s for the sandbox: %w", srcPath, err)
		}
		if info.IsDir() {
			if err := copyDirTreeAtDepth(srcPath, dstPath, depth+1); err != nil {
				return err
			}
			continue
		}
		if err := copyFile(srcPath, dstPath, info.Mode()); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode.Perm()&0750)
	if err != nil {
		return err
	}

	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// grantAppContainerPathReadExecTree gives the AppContainer read/execute on path
// and its descendants. Inheritable rather than /t-recursive, for the reasons in
// grantAppContainerPath — this one runs on the runtime version directory, whose
// bundled node_modules alone is thousands of files.
func grantAppContainerPathReadExecTree(sid uintptr, path string) error {
	sidStr, err := appContainerSidToString(sid)
	if err != nil {
		return err
	}
	if appContainerHasGrant(sidStr, path) {
		return nil
	}
	if err := grantACL(path, sidStr, aclMaskReadExec, nvxInheritFlags); err != nil {
		return fmt.Errorf("read/execute tree grant for AppContainer: %w", err)
	}
	return nil
}

// grantAppContainerPathReadExec grants this-folder-only traverse rights on an
// ancestor directory. Skipped when access is already present, so the common case
// costs one cheap ACL read instead of a write that can stall behind a filter
// driver.
//
// Traverse and read-attributes only, NOT read. `(RX)` includes RD -- list
// folder / read data -- so granting it on the ancestors of a project inside the
// user profile let a contained process enumerate the names in %USERPROFILE%:
// `.ssh`, `.aws`, `.1password` and the rest. File contents stayed denied, but the
// listing alone tells an attacker exactly which credential stores exist and what
// to try next, and docs/enforcement-matrix.md claimed the sandbox could walk
// through a parent "without reading what else is inside it".
//
// (X) is pass-through and (RA) is stat. Together they are what the ancestor walk
// was always described as granting; the extra read was never intentional.
func grantAppContainerPathReadExec(sid uintptr, path string) error {
	return grantAppContainerPathReadExecTimeboxed(sid, path, 15*time.Second)
}

// grantAppContainerPathReadExecTimeboxed is grantAppContainerPathReadExec with an
// explicit per-call timeout, so the ancestor walk can bound an individual grant far
// more tightly than a direct, necessary grant would want.
func grantAppContainerPathReadExecTimeboxed(sid uintptr, path string, timeout time.Duration) error {
	sidStr, err := appContainerSidToString(sid)
	if err != nil {
		return err
	}
	if appContainerHasGrant(sidStr, path) {
		return nil
	}
	// Still timeboxed. The call is a syscall now rather than a process spawn, but
	// the reason for the bound was never the process: writing a directory's ACL
	// costs time linear in the entries beneath it, and the chain above a project
	// reaches directories with hundreds of thousands of them. See grantACLWithin.
	if err := grantACLWithin(path, sidStr, aclMaskTraverse, 0, timeout); err != nil {
		return fmt.Errorf("traverse grant for AppContainer: %w", err)
	}
	return nil
}

func appContainerSidToString(sid uintptr) (string, error) {
	var strPtr *uint16
	ret, _, err := modAdvapi32.NewProc("ConvertSidToStringSidW").Call(
		sid,
		uintptr(unsafe.Pointer(&strPtr)),
	)
	if ret == 0 {
		return "", fmt.Errorf("ConvertSidToStringSidW: %v", err)
	}
	defer syscall.LocalFree(syscall.Handle(unsafe.Pointer(strPtr)))
	return utf16PtrToString(strPtr), nil
}

func utf16PtrToString(p *uint16) string {
	if p == nil {
		return ""
	}
	var chars []uint16
	for i := 0; ; i++ {
		c := *(*uint16)(unsafe.Pointer(uintptr(unsafe.Pointer(p)) + uintptr(i*2)))
		if c == 0 {
			break
		}
		chars = append(chars, c)
	}
	return syscall.UTF16ToString(chars)
}
