//go:build windows

package main

// Generic helpers that sandbox_appcontainer_windows.go had accumulated.
//
// Copying a directory tree and converting a UTF-16 pointer have nothing to do
// with AppContainers; they were simply first needed there. An independent
// review pointed out that the file's stated subject -- ACL grants, SID
// lifecycle, profile management, executable staging -- was already more than
// one thing, and these made it harder to see what the file is actually about.
// A pure move: no behaviour changes here.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

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
