//go:build !windows

package nvx

// Nothing records a read/execute grant outside Windows, so no directory has an
// identity to follow. See sandbox_file_identity_windows.go.

func directoryIdentity(string) string { return "" }

func locateGrantedDirectory(readExecGrant) (string, directoryLocation) {
	return "", locationUnknown
}
