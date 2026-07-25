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

const (
	appContainerNamePrefix = "nvx.sandbox"
)

var (
	modUserenv = syscall.NewLazyDLL("userenv.dll")

	procCreateAppContainerProfile                 = modUserenv.NewProc("CreateAppContainerProfile")
	procDeriveAppContainerSidFromAppContainerName = modUserenv.NewProc("DeriveAppContainerSidFromAppContainerName")
	procDeleteAppContainerProfile                 = modUserenv.NewProc("DeleteAppContainerProfile")
)

// prepareAppContainerFilesystem grants the AppContainer SID write access to
// guestHome and workDir. guestHome gets a low mandatory integrity label for
// compatibility with legacy constrained launches; workDir stays default
// integrity so a normal AppContainer child can use it as cwd.
func prepareAppContainerFilesystem(sid uintptr, guestHome, workDir string) error {
	// The guest home must be writable; it is nvx-owned and safe to grant.
	if guestHome != "" {
		if err := grantAppContainerPath(sid, guestHome); err != nil {
			return err
		}
		if err := labelLowIntegrity(guestHome); err != nil {
			return fmt.Errorf("integrity label for %q: %w", guestHome, err)
		}
	}

	// The working-directory grant is best-effort. Many commands (e.g. npx) never
	// write the cwd, and the profile root both cannot be granted (its ACL write
	// hangs behind the OneDrive/Defender filter driver) and already grants ALL
	// APPLICATION PACKAGES for stat/traverse. Sandbox writes go to the guest home
	// regardless, so a failed workdir grant should not abort the run.
	if workDir != "" && !isProfileRoot(workDir) {
		if err := grantAppContainerPath(sid, workDir); err != nil {
			LogWarn("Could not grant the sandbox write access to %q: %v", workDir, err)
			LogInfo("Commands that write the current folder may fail here; run from a project subfolder, or use --no-sandbox.")
		}
	}
	// Tools stat the ancestors of both the working directory and the guest home
	// (which is HOME inside the sandbox), so grant traverse on both chains.
	grantWorkdirAncestors(sid, workDir)
	grantWorkdirAncestors(sid, guestHome)
	return nil
}

func isProfileRoot(dir string) bool {
	up := os.Getenv("USERPROFILE")
	return up != "" && strings.EqualFold(filepath.Clean(dir), filepath.Clean(up))
}

// grantWorkdirAncestors grants the AppContainer this-folder RX on each ancestor
// directory of workDir that sits strictly below the user profile root, so tools
// that stat ancestors (npm walking up to find a project root) succeed. It stops
// at the profile root: that root already grants ALL APPLICATION PACKAGES (and
// writing its ACL hangs behind the OneDrive/Defender filter driver), and C:\ /
// C:\Users are handled once by `nvx setup`. Best-effort and time-boxed.
func grantWorkdirAncestors(sid uintptr, workDir string) {
	if workDir == "" {
		return
	}
	profile := ""
	if up := os.Getenv("USERPROFILE"); up != "" {
		profile = filepath.Clean(up)
	}
	dir := filepath.Dir(filepath.Clean(workDir))
	for i := 0; i < 40; i++ {
		parent := filepath.Dir(dir)
		if parent == dir {
			break // reached the drive root
		}
		if profile == "" || !isPathStrictlyUnder(dir, profile) {
			break
		}
		_ = grantAppContainerPathReadExec(sid, dir)
		dir = parent
	}
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

func grantAppContainerPath(sid uintptr, path string) error {
	sidStr, err := appContainerSidToString(sid)
	if err != nil {
		return err
	}
	grantArg := fmt.Sprintf("*%s:(OI)(CI)(M)", sidStr)
	out, err := runWinCmd(20*time.Second, "icacls", path, "/grant", grantArg, "/t", "/c", "/q")
	if err != nil {
		return fmt.Errorf("icacls grant for AppContainer: %v (%s)", err, strings.TrimSpace(string(out)))
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
	grantWorkdirAncestors(sid, dir)
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

func copyDirTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm()&0750)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		return copyFile(path, target, info.Mode())
	})
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

func grantAppContainerPathReadExecTree(sid uintptr, path string) error {
	sidStr, err := appContainerSidToString(sid)
	if err != nil {
		return err
	}
	grantArg := fmt.Sprintf("*%s:(OI)(CI)(RX)", sidStr)
	out, err := runWinCmd(20*time.Second, "icacls", path, "/grant", grantArg, "/t", "/q")
	if err != nil {
		return fmt.Errorf("icacls RX tree grant for AppContainer: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func grantAppContainerPathReadExec(sid uintptr, path string) error {
	sidStr, err := appContainerSidToString(sid)
	if err != nil {
		return err
	}
	grantArg := fmt.Sprintf("*%s:(RX)", sidStr)
	out, err := runWinCmd(15*time.Second, "icacls", path, "/grant", grantArg, "/c", "/q")
	if err != nil {
		return fmt.Errorf("icacls RX grant for AppContainer: %v (%s)", err, strings.TrimSpace(string(out)))
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
