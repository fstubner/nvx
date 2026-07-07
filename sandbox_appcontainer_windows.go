//go:build windows

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
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
	for _, dir := range []string{guestHome, workDir} {
		if dir == "" {
			continue
		}
		if err := grantAppContainerPath(sid, dir); err != nil {
			return err
		}
	}
	if guestHome != "" {
		if err := labelLowIntegrity(guestHome); err != nil {
			return fmt.Errorf("integrity label for %q: %w", guestHome, err)
		}
	}
	grantWorkdirAncestors(sid, workDir)
	return nil
}

// grantWorkdirAncestors grants the AppContainer this-folder RX on each ancestor
// directory of workDir, so tools that stat ancestors (npm walking up to find a
// project root) succeed. This-folder-only ACEs let the container stat/traverse
// each directory without reading sibling contents. User-owned ancestors are
// granted here at runtime (no admin); system-owned ones (C:\, C:\Users) are
// expected to fail and are handled once by `nvx setup`, so failures are ignored.
func grantWorkdirAncestors(sid uintptr, workDir string) {
	if workDir == "" {
		return
	}
	dir := filepath.Dir(filepath.Clean(workDir))
	for i := 0; i < 40; i++ {
		parent := filepath.Dir(dir)
		if parent == dir {
			break // reached the drive root
		}
		_ = grantAppContainerPathReadExec(sid, dir)
		dir = parent
	}
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
	out, err := exec.Command("icacls", path, "/grant", grantArg, "/t", "/c", "/q").CombinedOutput()
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
	out, err := exec.Command("icacls", path, "/grant", grantArg, "/t", "/q").CombinedOutput()
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
	out, err := exec.Command("icacls", path, "/grant", grantArg, "/c", "/q").CombinedOutput()
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
