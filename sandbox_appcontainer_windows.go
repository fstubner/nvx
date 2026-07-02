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
	appContainerName = "nvx.sandbox"
)

var (
	modUserenv = syscall.NewLazyDLL("userenv.dll")

	procCreateAppContainerProfile                 = modUserenv.NewProc("CreateAppContainerProfile")
	procDeriveAppContainerSidFromAppContainerName = modUserenv.NewProc("DeriveAppContainerSidFromAppContainerName")
)

// prepareAppContainerFilesystem grants the AppContainer SID write access to
// guestHome and workDir and applies Low integrity labels so stacked Low IL
// tokens can write there as well.
func prepareAppContainerFilesystem(sid uintptr, guestHome, workDir string) error {
	for _, dir := range []string{guestHome, workDir} {
		if dir == "" {
			continue
		}
		if err := grantAppContainerPath(sid, dir); err != nil {
			return err
		}
		if err := labelLowIntegrity(dir); err != nil {
			return fmt.Errorf("integrity label for %q: %w", dir, err)
		}
	}
	return nil
}

func ensureAppContainerSID() (uintptr, error) {
	name, err := syscall.UTF16PtrFromString(appContainerName)
	if err != nil {
		return 0, err
	}

	var sid uintptr
	hr, _, _ := procDeriveAppContainerSidFromAppContainerName.Call(
		uintptr(unsafe.Pointer(name)),
		uintptr(unsafe.Pointer(&sid)),
	)
	if hr == 0 && sid != 0 {
		return sid, nil
	}

	display, _ := syscall.UTF16PtrFromString("nvx sandbox")
	desc, _ := syscall.UTF16PtrFromString("Ephemeral nvx execution sandbox")
	var newSid uintptr
	hr, _, callErr := procCreateAppContainerProfile.Call(
		uintptr(unsafe.Pointer(name)),
		uintptr(unsafe.Pointer(display)),
		uintptr(unsafe.Pointer(desc)),
		0, 0,
		uintptr(unsafe.Pointer(&newSid)),
	)
	if hr == 0 && newSid != 0 {
		return newSid, nil
	}

	// 0x80073D10 = HRESULT_FROM_WIN32(ERROR_ALREADY_EXISTS)
	if hr == 0x80073D10 {
		hr, _, callErr = procDeriveAppContainerSidFromAppContainerName.Call(
			uintptr(unsafe.Pointer(name)),
			uintptr(unsafe.Pointer(&sid)),
		)
		if hr == 0 && sid != 0 {
			return sid, nil
		}
	}

	return 0, fmt.Errorf("DeriveAppContainerSid failed (hr=0x%X): %v", hr, callErr)
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
	if err := labelLowIntegrity(dir); err != nil {
		return "", fmt.Errorf("low integrity label for runtime %q: %w", dir, err)
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
	if err := os.MkdirAll(destDir, 0755); err != nil {
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
			return os.MkdirAll(target, info.Mode().Perm())
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
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

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode.Perm())
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
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
