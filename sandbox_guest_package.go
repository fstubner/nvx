package main

import (
	"os"
	"path/filepath"
	"strings"
)

// guestHomePackageFile records which AppContainer package a session launched
// under, inside the guest home itself.
//
// It lives there for the same reason the session owner record does: a directory
// and the facts about it cannot then disagree, and removing the directory
// removes the record with it. That pairing is what lets the package sweep ask
// "is any LIVE session using this package" using the liveness rule that already
// exists, rather than inventing a second one.
//
// Dotted so it sorts away from the profile skeleton, and named for what it holds
// rather than for the sweep that reads it.
const guestHomePackageFile = ".nvx-package"

// writeGuestHomePackage records the package this session runs under.
//
// Best-effort, like the owner marker: a sandbox whose package cannot be recorded
// still works, and the cost is that the sweep falls back to the retention window
// for it instead of protecting it outright. Refusing to launch over a
// housekeeping file would be the wrong trade.
//
// Written before the profile is registered, so there is no window in which a
// package exists that nothing claims. The reverse order would leave a profile
// briefly unattributed, which is exactly when a concurrent sweep would look.
func writeGuestHomePackage(guestHome, pkgName string) {
	if guestHome == "" || pkgName == "" {
		return
	}
	_ = os.WriteFile(filepath.Join(guestHome, guestHomePackageFile), []byte(pkgName), 0600)
}

// readGuestHomePackage returns the package a guest home was launched under.
func readGuestHomePackage(guestHome string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(guestHome, guestHomePackageFile))
	if err != nil {
		return "", false
	}
	name := strings.TrimSpace(string(data))
	if name == "" {
		return "", false
	}
	return name, true
}
